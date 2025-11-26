package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"github.com/pires/go-proxyproto"
	"github.com/sirupsen/logrus"
	"go.mongodb.org/mongo-driver/mongo"

	"io"
	"log"
	"math/rand"
	"mime"
	"mime/multipart"
	"net"
	"net/textproto"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

type MM4Message struct {
	From          string
	To            string
	Content       []byte
	Headers       textproto.MIMEHeader
	Client        *Client
	Route         *Route
	MessageID     string
	Files         []MsgFile
	TransactionID string
}

// MM4Server represents the SMTP server.
type MM4Server struct {
	Addr               string
	routing            *Router
	mu                 sync.RWMutex
	listener           net.Listener
	mongo              *mongo.Client
	connectedClients   map[string]time.Time
	gateway            *Gateway
	MediaTranscodeChan chan *MM4Message
}

// Start begins listening for incoming SMTP connections.
func (s *MM4Server) Start() error {
	s.connectedClients = make(map[string]time.Time)
	s.MediaTranscodeChan = make(chan *MM4Message)

	go s.transcodeMedia()

	lm := s.gateway.LogManager
	lm.SendLog(lm.BuildLog(
		"Server.MM4.Start",
		"MM4ServerStarting",
		logrus.InfoLevel,
		map[string]interface{}{
			"addr":            s.Addr,
			"proxy_protocol":  os.Getenv("HAPROXY_PROXY_PROTOCOL"),
			"mm4_debug":       os.Getenv("MM4_DEBUG"),
			"connected_count": 0,
		},
	))

	listen, err := net.Listen("tcp", s.Addr)
	if err != nil {
		lm.SendLog(lm.BuildLog(
			"Server.MM4.Start",
			"ListenError",
			logrus.ErrorLevel,
			map[string]interface{}{
				"addr": s.Addr,
			},
			err,
		))
		return err
	}
	defer listen.Close()

	var proxyListener net.Listener

	if os.Getenv("HAPROXY_PROXY_PROTOCOL") == "true" {
		proxyListener = &proxyproto.Listener{Listener: listen}
		defer proxyListener.Close()

		// Accept one connection to prove proxy protocol is working
		conn, err := proxyListener.Accept()
		if err != nil {
			lm.SendLog(lm.BuildLog(
				"Server.MM4.Start",
				"ProxyAcceptError",
				logrus.ErrorLevel,
				nil,
				err,
			))
			return err
		}

		lm.SendLog(lm.BuildLog(
			"Server.MM4.Start",
			"ProxyConnectionInfo",
			logrus.InfoLevel,
			map[string]interface{}{
				"local_addr":  conn.LocalAddr().String(),
				"remote_addr": conn.RemoteAddr().String(),
			},
		))

		if err := conn.Close(); err != nil {
			log.Fatal("couldn't close proxy connection")
		}
	} else {
		proxyListener = listen
	}

	s.listener = proxyListener

	lm.SendLog(lm.BuildLog(
		"Server.MM4.Start",
		"MM4ServerListening",
		logrus.InfoLevel,
		map[string]interface{}{
			"addr": s.Addr,
		},
	))

	for {
		conn, err := proxyListener.Accept()
		if err != nil {
			lm.SendLog(lm.BuildLog(
				"Server.MM4.Start",
				"AcceptError",
				logrus.ErrorLevel,
				nil,
				err,
			))
			return err
		}
		go s.handleConnection(conn)
	}
}

// handleConnection manages an individual SMTP session.
func (s *MM4Server) handleConnection(conn net.Conn) {
	defer conn.Close()
	lm := s.gateway.LogManager

	remoteAddr := conn.RemoteAddr().String()
	ip, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		lm.SendLog(lm.BuildLog(
			"Server.MM4.HandleConnection",
			"ParseAddressError",
			logrus.WarnLevel,
			map[string]interface{}{
				"remote_addr": remoteAddr,
			},
			err,
		))
		return
	}
	hashedIP := hashIP(ip)

	lm.SendLog(lm.BuildLog(
		"Server.MM4.HandleConnection",
		"IncomingConnection",
		logrus.InfoLevel,
		map[string]interface{}{
			"remote_addr": remoteAddr,
			"ip":          ip,
			"ip_hash":     hashedIP,
		},
	))

	// Increase buffer size to handle large headers
	reader := bufio.NewReaderSize(conn, 65536) // 64 KB buffer
	writer := bufio.NewWriter(conn)

	// Send initial greeting
	writeResponse(writer, "220 localhost SMTP server ready")

	// Identify the client based on the IP address
	client := s.getClientByIP(ip)
	if client == nil {
		writeResponse(writer, "550 Access denied")

		if isTrustedProxy(ip, trustedProxies) {
			lm.SendLog(lm.BuildLog(
				"Server.MM4.HandleConnection",
				"AuthDeniedTrustedProxy",
				logrus.WarnLevel,
				map[string]interface{}{
					"ip":      ip,
					"ip_hash": hashedIP,
				},
			))
			return
		} else {
			lm.SendLog(lm.BuildLog(
				"Server.MM4.HandleConnection",
				"AuthFailed",
				logrus.WarnLevel,
				map[string]interface{}{
					"client":  "unknown",
					"ip":      ip,
					"ip_hash": hashedIP,
				},
			))
		}
		return
	}

	// Check if the client is already connected
	s.mu.RLock()
	lastActivity, exists := s.connectedClients[hashedIP]
	s.mu.RUnlock()

	if !exists {
		// New connection
		lm.SendLog(lm.BuildLog(
			"Server.MM4.HandleConnection",
			"AuthSuccess",
			logrus.InfoLevel,
			map[string]interface{}{
				"client":  client.Username,
				"ip":      ip,
				"ip_hash": hashedIP,
				"fresh":   true,
			},
		))
	} else {
		// Existing connection
		inactivityDuration := time.Since(lastActivity)
		if inactivityDuration > 2*time.Minute {
			// Log an alert for reconnecting after inactivity
			lm.SendLog(lm.BuildLog(
				"Server.MM4.HandleConnection",
				"MM4ReconnectInactivity",
				logrus.InfoLevel,
				map[string]interface{}{
					"client":             client.Username,
					"ip":                 ip,
					"ip_hash":            hashedIP,
					"inactivity_seconds": inactivityDuration.Seconds(),
				},
			))
		} else {
			// Log regular reconnections
			lm.SendLog(lm.BuildLog(
				"Server.MM4.HandleConnection",
				"MM4Reconnect",
				logrus.InfoLevel,
				map[string]interface{}{
					"client":             client.Username,
					"ip":                 ip,
					"ip_hash":            hashedIP,
					"inactivity_seconds": inactivityDuration.Seconds(),
				},
			))
		}
	}

	// ALWAYS update the last connection time, regardless of inactivity
	s.mu.Lock()
	s.connectedClients[hashedIP] = time.Now()
	activeCount := len(s.connectedClients)
	s.mu.Unlock()

	lm.SendLog(lm.BuildLog(
		"Server.MM4.HandleConnection",
		"MM4ActiveClientCount",
		logrus.DebugLevel,
		map[string]interface{}{
			"active_clients": activeCount,
		},
	))

	// Create the session
	session := &Session{
		Conn:       conn,
		Reader:     reader,
		Writer:     writer,
		Server:     s,
		Client:     client,
		IPHash:     hashedIP,
		ClientIP:   ip,
		RemoteAddr: remoteAddr,
		mongo:      s.mongo,
	}

	// Handle the session
	if err := session.handleSession(s); err != nil {
		lm.SendLog(lm.BuildLog(
			"Server.MM4.HandleConnection",
			"MM4SessionError",
			logrus.InfoLevel,
			map[string]interface{}{
				"client":     client.Username,
				"ip":         ip,
				"ip_hash":    hashedIP,
				"remoteAddr": remoteAddr,
			},
			err,
		))
		writeResponse(writer, "451 Internal server error")
	}
}

// getClientByIP returns the client associated with the given IP address.
func (s *MM4Server) getClientByIP(ip string) *Client {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, client := range s.gateway.Clients {
		if client.Address == ip {
			return client
		}
	}
	return nil
}

// hashIP hashes an IP address using SHA-256.
func hashIP(ip string) string {
	hash := sha256.Sum256([]byte(ip))
	return fmt.Sprintf("%x", hash)
}

// Session represents an SMTP session.
type Session struct {
	Conn       net.Conn
	Reader     *bufio.Reader
	Writer     *bufio.Writer
	From       string
	To         []string
	Data       []byte
	Headers    textproto.MIMEHeader
	Server     *MM4Server
	Client     *Client
	IPHash     string
	ClientIP   string
	RemoteAddr string
	Files      []MsgFile
	mongo      *mongo.Client
}

// debugLog is a helper to send debug logs when MM4_DEBUG is enabled.
func (s *Session) debugLog(action string, fields map[string]interface{}) {
	if strings.ToLower(os.Getenv("MM4_DEBUG")) != "true" {
		return
	}
	lm := s.Server.gateway.LogManager
	base := map[string]interface{}{
		"ip":         s.ClientIP,
		"ip_hash":    s.IPHash,
		"remoteAddr": s.RemoteAddr,
	}
	if s.Client != nil {
		base["client"] = s.Client.Username
	}
	for k, v := range fields {
		base[k] = v
	}
	lm.SendLog(lm.BuildLog(
		"Server.MM4.Session",
		action,
		logrus.DebugLevel,
		base,
	))
}

// handleSession processes SMTP commands from the client.
func (s *Session) handleSession(srv *MM4Server) error {
	s.debugLog("SessionStart", map[string]interface{}{})

	for {
		line, err := s.Reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				s.debugLog("SessionEOF", map[string]interface{}{})
				return nil // client closed the connection
			}
			s.debugLog("SessionReadError", map[string]interface{}{
				"error": err.Error(),
			})
			return err
		}
		line = strings.TrimSpace(line)

		if line == "" {
			continue
		}

		s.debugLog("IncomingCommand", map[string]interface{}{
			"raw": line,
		})

		// Handle the command
		if err := s.handleCommand(line, srv); err != nil {
			s.debugLog("CommandHandlerError", map[string]interface{}{
				"line":  line,
				"error": err.Error(),
			})
			return err
		}
	}
}

// handleCommand processes a single SMTP command.
func (s *Session) handleCommand(line string, srv *MM4Server) error {
	// Split command and arguments
	parts := strings.SplitN(line, " ", 2)
	cmd := strings.ToUpper(parts[0])
	var arg string
	if len(parts) > 1 {
		arg = parts[1]
	}

	// Update the timestamp with current time
	srv.mu.Lock()
	srv.connectedClients[s.IPHash] = time.Now()
	srv.mu.Unlock()

	s.debugLog("ParsedCommand", map[string]interface{}{
		"cmd": cmd,
		"arg": arg,
	})

	switch cmd {
	case "HELO", "EHLO":
		writeResponse(s.Writer, "250 Hello")
	case "MAIL":
		if err := s.handleMail(arg); err != nil {
			s.debugLog("MAILError", map[string]interface{}{
				"arg":   arg,
				"error": err.Error(),
			})
			writeResponse(s.Writer, fmt.Sprintf("550 %v", err))
		} else {
			writeResponse(s.Writer, "250 OK")
		}
	case "RCPT":
		if err := s.handleRcpt(arg); err != nil {
			s.debugLog("RCPTError", map[string]interface{}{
				"arg":   arg,
				"error": err.Error(),
			})
			writeResponse(s.Writer, fmt.Sprintf("550 %v", err))
		} else {
			writeResponse(s.Writer, "250 OK")
		}
	case "DATA":
		writeResponse(s.Writer, "354 End data with <CR><LF>.<CR><LF>")
		if err := s.handleData(); err != nil {
			lm := s.Server.gateway.LogManager
			lm.SendLog(lm.BuildLog(
				"Server.MM4.HandleCommand",
				"HandleData",
				logrus.InfoLevel,
				map[string]interface{}{
					"client": s.Client.Username,
					"ip":     s.ClientIP,
				},
				err,
			))
			writeResponse(s.Writer, fmt.Sprintf("554 %v", err))
		} else {
			writeResponse(s.Writer, "250 OK")
		}
	case "NOOP":
		writeResponse(s.Writer, "250 OK")
	case "RSET":
		s.debugLog("RSET", map[string]interface{}{
			"from": s.From,
			"to":   strings.Join(s.To, ","),
		})
		s.From = ""
		s.To = nil
		s.Data = nil
		s.Headers = nil
		writeResponse(s.Writer, "250 OK")
	case "QUIT":
		writeResponse(s.Writer, "221 Bye")
		s.debugLog("QUIT", map[string]interface{}{})
		return errors.New("client disconnected")
	default:
		s.debugLog("UnknownCommand", map[string]interface{}{
			"cmd": line,
		})
		writeResponse(s.Writer, fmt.Sprintf("502 Command not implemented: %s", cmd))
	}
	return nil
}

// handleMail processes the MAIL FROM command.
func (s *Session) handleMail(arg string) error {
	if !strings.HasPrefix(strings.ToUpper(arg), "FROM:") {
		return errors.New("syntax error in MAIL command")
	}
	s.From = strings.TrimSpace(arg[5:])
	s.debugLog("MAILFROM", map[string]interface{}{
		"from": s.From,
	})
	return nil
}

// handleRcpt processes the RCPT TO command.
func (s *Session) handleRcpt(arg string) error {
	if !strings.HasPrefix(strings.ToUpper(arg), "TO:") {
		return errors.New("syntax error in RCPT command")
	}
	recipient := strings.TrimSpace(arg[3:])
	s.To = append(s.To, recipient)
	s.debugLog("RCPTTO", map[string]interface{}{
		"recipient": recipient,
		"count":     len(s.To),
	})
	return nil
}

// handleData processes the DATA command and reads message content.
func (s *Session) handleData() error {
	tp := textproto.NewReader(s.Reader)

	// Read headers
	headers, err := tp.ReadMIMEHeader()
	if err != nil {
		return err
	}
	s.Headers = headers

	// Read the body using DotReader
	var bodyBuilder strings.Builder
	if _, err := io.Copy(&bodyBuilder, tp.DotReader()); err != nil {
		return err
	}
	s.Data = []byte(bodyBuilder.String())

	// Handle MM4 message
	if err := s.handleMM4Message(); err != nil {
		// 🔴 Catch-all dump for anything that bubbles up
		s.dumpFullMM4("handle_mm4_message_error")
		return err
	}
	return nil
}

// handleMM4Message processes the MM4 message based on its type.
func (s *Session) handleMM4Message() error {
	// Check required MM4 headers
	requiredHeaders := []string{
		"X-Mms-3GPP-MMS-Version",
		"X-Mms-message-Type",
		"X-Mms-message-ID",
		"X-Mms-Transaction-ID",
		"From",
		"To",
	}
	for _, header := range requiredHeaders {
		if s.Headers.Get(header) == "" {
			// 🔴 Dump the full MM4 request before bailing
			s.dumpFullMM4("missing_required_header_" + header)
			return fmt.Errorf("missing required header: %s", header)
		}
	}

	transactionID := s.Headers.Get("X-Mms-Transaction-ID")
	messageID := s.Headers.Get("X-Mms-message-ID")

	s.debugLog("MM4HeadersValidated", map[string]interface{}{
		"transaction_id": transactionID,
		"message_id":     messageID,
		"from":           s.Headers.Get("From"),
		"to":             s.Headers.Get("To"),
	})

	mm4Message := &MM4Message{
		From:          s.Headers.Get("From"),
		To:            s.Headers.Get("To"),
		Content:       s.Data,
		Headers:       s.Headers,
		Client:        s.Client,
		TransactionID: transactionID,
		MessageID:     messageID,
	}

	// Parse MIME parts to extract files
	mm, err := mm4Message.parseMIMEParts()
	if err != nil {
		// 🔴 Dump entire thing if MIME parsing fails
		s.dumpFullMM4("parse_mime_parts_error")
		return fmt.Errorf("failed to parse MIME parts: %v", err)
	}

	s.debugLog("MIMEPartsParsed", map[string]interface{}{
		"file_count": len(mm.Files),
	})

	s.Server.MediaTranscodeChan <- mm

	writeResponse(s.Writer, "250 message queued for processing")
	return nil
}

// parseMIMEParts parses the MIME multipart content to extract files.
func (m *MM4Message) parseMIMEParts() (*MM4Message, error) {
	contentType := m.Headers.Get("Content-Type")
	if contentType == "" {
		return nil, fmt.Errorf("missing Content-Type header")
	}

	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		return nil, fmt.Errorf("failed to parse Msg-Type: %v", err)
	}

	if strings.HasPrefix(mediaType, "multipart/") {
		boundary := params["boundary"]
		if boundary == "" {
			return nil, fmt.Errorf("no boundary parameter in Msg-Type")
		}

		reader := multipart.NewReader(bytes.NewReader(m.Content), boundary)
		for {
			part, err := reader.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, fmt.Errorf("failed to read part: %v", err)
			}

			var buf bytes.Buffer
			if _, err := io.Copy(&buf, part); err != nil {
				return nil, fmt.Errorf("failed to read part content: %v", err)
			}

			file := MsgFile{
				Filename:    part.FileName(),
				ContentType: part.Header.Get("Content-Type"),
				Content:     buf.Bytes(),
			}
			m.Files = append(m.Files, file)
		}
	} else {
		file := MsgFile{
			Filename:    "",
			ContentType: mediaType,
			Content:     m.Content,
		}
		m.Files = append(m.Files, file)
	}

	return m, nil
}

// sendMM4 sends an MM4 message to a client over plain TCP with base64-encoded media.
func (s *MM4Server) sendMM4(item MsgQueueItem) error {
	if item.files == nil {
		return fmt.Errorf("files are nil")
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	newStr := strings.Replace(item.To, "+", "", -1)

	client := s.gateway.getClient(newStr)
	if client == nil {
		return fmt.Errorf("no client found for destination number: %s", item.To)
	}

	lm := s.gateway.LogManager
	lm.SendLog(lm.BuildLog(
		"Server.MM4.Outbound",
		"MM4SendStart",
		logrus.InfoLevel,
		map[string]interface{}{
			"to":                 item.To,
			"from":               item.From,
			"file_count":         len(item.files),
			"destination_client": client.Username,
			"destination_addr":   client.Address,
		},
	))

	port := "25" // Default SMTP port todo
	address := net.JoinHostPort(client.Address, port)

	conn, err := net.DialTimeout("tcp", address, 10*time.Second)
	if err != nil {
		lm.SendLog(lm.BuildLog(
			"Server.MM4.Outbound",
			"ConnectError",
			logrus.ErrorLevel,
			map[string]interface{}{
				"address": address,
			},
			err,
		))
		return fmt.Errorf("failed to connect to client's MM4 server at %s", address)
	}
	defer conn.Close()

	if err := conn.SetDeadline(time.Now().Add(30 * time.Second)); err != nil {
		lm.SendLog(lm.BuildLog(
			"Server.MM4.Outbound",
			"SetDeadlineError",
			logrus.WarnLevel,
			map[string]interface{}{
				"address": address,
			},
			err,
		))
	}

	reader := bufio.NewReader(conn)
	writer := bufio.NewWriter(conn)

	mm4Message := s.createMM4Message(item)

	session := &Session{
		Conn:    conn,
		Reader:  reader,
		Writer:  writer,
		Server:  s,
		Client:  client,
		Headers: mm4Message.Headers,
		From:    mm4Message.From,
		To:      []string{mm4Message.To},
		Data:    mm4Message.Content,
		Files:   mm4Message.Files,
	}

	// Read server's initial response
	response, err := session.readResponse()
	if err != nil {
		lm.SendLog(lm.BuildLog(
			"Server.MM4.Outbound",
			"GreetingError",
			logrus.ErrorLevel,
			map[string]interface{}{
				"address": address,
			},
			err,
		))
		return fmt.Errorf("failed to read server greeting: %v", err)
	}
	if !strings.HasPrefix(response, "220") {
		lm.SendLog(lm.BuildLog(
			"Server.MM4.Outbound",
			"UnexpectedGreeting",
			logrus.ErrorLevel,
			map[string]interface{}{
				"address":  address,
				"response": response,
			},
		))
		return fmt.Errorf("unexpected server greeting: %s", response)
	}

	// Send EHLO command
	if err := session.sendCommand("EHLO localhost"); err != nil {
		lm.SendLog(lm.BuildLog(
			"Server.MM4.Outbound",
			"EHLOSendError",
			logrus.ErrorLevel,
			map[string]interface{}{
				"address": address,
			},
			err,
		))
		return err
	}
	response, err = session.readResponse()
	if err != nil {
		lm.SendLog(lm.BuildLog(
			"Server.MM4.Outbound",
			"EHLOReadError",
			logrus.ErrorLevel,
			map[string]interface{}{
				"address": address,
			},
			err,
		))
		return err
	}
	if !strings.HasPrefix(response, "250") {
		lm.SendLog(lm.BuildLog(
			"Server.MM4.Outbound",
			"EHLOFailed",
			logrus.ErrorLevel,
			map[string]interface{}{
				"address":  address,
				"response": response,
			},
		))
		return fmt.Errorf("EHLO command failed: %s", response)
	}

	// Proceed to send the MM4 message
	if err := session.sendMM4Message(); err != nil {
		lm.SendLog(lm.BuildLog(
			"Server.MM4.Outbound",
			"MM4SendError",
			logrus.ErrorLevel,
			map[string]interface{}{
				"address": address,
			},
			err,
		))
		return fmt.Errorf("send MM4 failed: %v", err)
	}

	// Send QUIT command to terminate the session gracefully
	if err := session.sendCommand("QUIT"); err != nil {
		lm.SendLog(lm.BuildLog(
			"Server.MM4.Outbound",
			"QUITSendError",
			logrus.ErrorLevel,
			map[string]interface{}{
				"address": address,
			},
			err,
		))
		return fmt.Errorf("send QUIT failed: %v", err)
	}
	response, err = session.readResponse()
	if err != nil {
		lm.SendLog(lm.BuildLog(
			"Server.MM4.Outbound",
			"QUITReadError",
			logrus.ErrorLevel,
			map[string]interface{}{
				"address": address,
			},
			err,
		))
		return err
	}
	if !strings.HasPrefix(response, "221") {
		lm.SendLog(lm.BuildLog(
			"Server.MM4.Outbound",
			"QUITFailed",
			logrus.ErrorLevel,
			map[string]interface{}{
				"address":  address,
				"response": response,
			},
		))
		return fmt.Errorf("QUIT command failed: %s", response)
	}

	lm.SendLog(lm.BuildLog(
		"Server.MM4.Outbound",
		"MM4SendSuccess",
		logrus.InfoLevel,
		map[string]interface{}{
			"to":               item.To,
			"from":             item.From,
			"file_count":       len(item.files),
			"destination_addr": address,
		},
	))

	return nil
}

// createMM4Message constructs an MM4Message with the provided media files.
func (s *MM4Server) createMM4Message(msgItem MsgQueueItem) *MM4Message {
	headers := textproto.MIMEHeader{}
	headers.Set("To", fmt.Sprintf("%s/TYPE=PLMN", msgItem.To))
	headers.Set("From", fmt.Sprintf("%s/TYPE=PLMN", msgItem.From))
	headers.Set("MIME-Version", "1.0")
	headers.Set("X-Mms-3GPP-Mms-Version", "6.10.0")
	headers.Set("X-Mms-message-Type", "MM4_forward.REQ")
	headers.Set("X-Mms-message-Id", fmt.Sprintf("<%s@%s>", msgItem.LogID, os.Getenv("MM4_MSG_ID_HOST")))
	headers.Set("X-Mms-Transaction-Id", msgItem.LogID)
	headers.Set("X-Mms-Ack-Request", "Yes")

	originatorSystem := os.Getenv("MM4_ORIGINATOR_SYSTEM")
	if originatorSystem == "" {
		originatorSystem = "system@yourdomain.com"
	}
	headers.Set("X-Mms-Originator-System", originatorSystem)
	headers.Set("Date", time.Now().UTC().Format(time.RFC1123Z))

	files := make([]MsgFile, 0)
	for _, f := range msgItem.files {
		files = append(files, MsgFile{
			Filename:    f.Filename,
			ContentType: f.ContentType,
			Content:     f.Content,
		})
	}

	return &MM4Message{
		From:          msgItem.From,
		To:            msgItem.To,
		Content:       []byte(msgItem.message),
		Headers:       headers,
		TransactionID: msgItem.LogID,
		MessageID:     msgItem.LogID,
		Files:         files,
	}
}

// sendCommand sends a command to the SMTP/MM4 server.
func (s *Session) sendCommand(cmd string) error {
	s.debugLog("SendCommand", map[string]interface{}{
		"cmd": cmd,
	})

	_, err := s.Writer.WriteString(cmd + "\r\n")
	if err != nil {
		return fmt.Errorf("failed to send command '%s'", cmd)
	}
	err = s.Writer.Flush()
	if err != nil {
		return fmt.Errorf("failed to flush command '%s': %v", cmd, err)
	}
	return nil
}

// readResponse reads the server's response after sending a command.
func (s *Session) readResponse() (string, error) {
	response, err := s.Reader.ReadString('\n')
	if err != nil {
		return "", fmt.Errorf("failed to read server response: %v", err)
	}
	response = strings.TrimSpace(response)

	s.debugLog("ReadResponse", map[string]interface{}{
		"response": response,
	})

	if len(response) < 3 {
		return response, nil
	}
	codeStr := response[:3]
	code, err := strconv.Atoi(codeStr)
	if err != nil {
		return response, nil
	}
	if code >= 400 {
		return response, fmt.Errorf("server responded with error: %s", response)
	}
	return response, nil
}

// generateContentID creates a unique Msg-ID.
func generateContentID() string {
	rand.Seed(time.Now().UnixNano())
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		rand.Uint32(),
		rand.Uint32()&0xffff,
		rand.Uint32()&0xffff,
		rand.Uint32()&0xffff,
		rand.Uint32())
}

// generateSMIL generates a SMIL content that references the provided media files.
func generateSMIL(files []MsgFile) ([]byte, error) {
	if len(files) == 0 {
		return nil, fmt.Errorf("no media files to include in SMIL")
	}

	var smilBuffer bytes.Buffer
	smilBuffer.WriteString("<smil>\n<head>\n")
	smilBuffer.WriteString("<layout>\n")
	smilBuffer.WriteString("<root-layout width=\"320px\" height=\"480px\"/>\n")
	smilBuffer.WriteString("<region id=\"Image\" width=\"100%\" height=\"100%\" fit=\"meet\"/>\n")
	smilBuffer.WriteString("</layout>\n")
	smilBuffer.WriteString("</head>\n")
	smilBuffer.WriteString("<body>\n")

	for _, file := range files {
		if strings.HasPrefix(file.ContentType, "image/") {
			smilBuffer.WriteString("<par dur=\"5000ms\">\n")
			smilBuffer.WriteString(fmt.Sprintf("<img src=\"%s\" region=\"Image\"/>\n", file.Filename))
			smilBuffer.WriteString("</par>\n")
		}
	}

	smilBuffer.WriteString("</body>\n")
	smilBuffer.WriteString("</smil>\n")

	return smilBuffer.Bytes(), nil
}

func (s *Session) sendMM4Message() error {
	if len(s.Files) <= 0 {
		return fmt.Errorf("no files found")
	}

	s.debugLog("MM4BuildMessage", map[string]interface{}{
		"from":       s.From,
		"to":         strings.Join(s.To, ","),
		"file_count": len(s.Files),
	})

	// Step 1: MAIL FROM Command
	mailFromCmd := fmt.Sprintf("MAIL FROM:<%s>", s.From)
	if err := s.sendCommand(mailFromCmd); err != nil {
		return err
	}
	response, err := s.readResponse()
	if err != nil {
		return err
	}
	if !strings.HasPrefix(response, "250") {
		return fmt.Errorf("MAIL FROM command failed: %s", response)
	}

	// Step 2: RCPT TO Commands
	for _, recipient := range s.To {
		rcptToCmd := fmt.Sprintf("RCPT TO:<%s>", recipient)
		if err := s.sendCommand(rcptToCmd); err != nil {
			return err
		}
		response, err := s.readResponse()
		if err != nil {
			return err
		}
		if !strings.HasPrefix(response, "250") {
			return fmt.Errorf("RCPT TO command failed: %s", response)
		}
	}

	// Step 3: DATA Command
	if err := s.sendCommand("DATA"); err != nil {
		return err
	}
	response, err = s.readResponse()
	if err != nil {
		return err
	}
	if !strings.HasPrefix(response, "354") {
		return fmt.Errorf("DATA command failed: %s", response)
	}

	// Step 4: Building the MIME Multipart message
	var messageBuffer bytes.Buffer

	boundary := generateBoundary()

	messageBuffer.WriteString(fmt.Sprintf("To: %s\r\n", strings.Join(s.To, ", ")))
	messageBuffer.WriteString(fmt.Sprintf("From: %s\r\n", s.From))
	messageBuffer.WriteString(fmt.Sprintf("Content-Type: multipart/related; boundary=\"%s\"\r\n", boundary))
	messageBuffer.WriteString("MIME-Version: 1.0\r\n")

	essentialHeaders := []string{
		"X-Mms-3GPP-Mms-Version",
		"X-Mms-message-Type",
		"X-Mms-message-Id",
		"X-Mms-Transaction-Id",
		"X-Mms-Ack-Request",
		"X-Mms-Originator-System",
		"Date",
		"message-ID",
	}
	for _, header := range essentialHeaders {
		if value := s.Headers.Get(header); value != "" {
			messageBuffer.WriteString(fmt.Sprintf("%s: %s\r\n", header, value))
		}
	}

	messageBuffer.WriteString("\r\n")

	if len(s.Files) > 0 {
		smilContent, err := generateSMIL(s.Files)
		if err != nil {
			return err
		}

		messageBuffer.WriteString(fmt.Sprintf("--%s\r\n", boundary))
		messageBuffer.WriteString("Content-Id: <0.smil>\r\n")
		messageBuffer.WriteString("Content-Type: application/smil; name=\"0.smil\"\r\n")
		messageBuffer.WriteString("\r\n")
		messageBuffer.Write(smilContent)
		messageBuffer.WriteString("\r\n")
	}

	for _, file := range s.Files {
		if file.ContentType == "application/smil" {
			continue
		}

		contentID := generateContentID()

		messageBuffer.WriteString(fmt.Sprintf("--%s\r\n", boundary))
		messageBuffer.WriteString(fmt.Sprintf("Content-Id: <%s>\r\n", contentID))
		messageBuffer.WriteString(fmt.Sprintf("Content-Type: %s; name=\"%s\"\r\n", file.ContentType, file.Filename))
		messageBuffer.WriteString(fmt.Sprintf("Content-Disposition: attachment; filename=\"%s\"\r\n", file.Filename))
		messageBuffer.WriteString(fmt.Sprintf("Content-Location: %s\r\n", file.Filename))
		messageBuffer.WriteString("Content-Transfer-Encoding: base64\r\n")
		messageBuffer.WriteString("\r\n")

		encoded := base64.StdEncoding.EncodeToString(file.Content)
		for i := 0; i < len(encoded); i += 76 {
			end := i + 76
			if end > len(encoded) {
				end = len(encoded)
			}
			messageBuffer.WriteString(encoded[i:end] + "\r\n")
		}
	}

	messageBuffer.WriteString(fmt.Sprintf("--%s--\r\n", boundary))
	messageBuffer.WriteString(".\r\n")

	msgData := messageBuffer.String()
	_, err = s.Writer.WriteString(msgData)
	if err != nil {
		return err
	}
	err = s.Writer.Flush()
	if err != nil {
		return err
	}

	response, err = s.readResponse()
	if err != nil {
		return err
	}
	if !strings.HasPrefix(response, "250") {
		return fmt.Errorf("DATA send failed: %s", response)
	}

	s.debugLog("MM4MessageSent", map[string]interface{}{
		"to":         strings.Join(s.To, ","),
		"file_count": len(s.Files),
	})

	return nil
}

// randomString generates a random alphanumeric string of the given length.
func randomString(length int) string {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	result := make([]byte, length)
	for i := range result {
		result[i] = charset[rand.Intn(len(charset))]
	}
	return string(result)
}

// generateBoundary creates a unique boundary string.
func generateBoundary() string {
	return fmt.Sprintf("===============%s", randomString(16))
}

// writeResponse sends a response to the client.
func writeResponse(writer *bufio.Writer, response string) {
	// this is effectively "server side" debug; keep it light
	// log.Printf("S: %s", response)
	writer.WriteString(response + "\r\n")
	writer.Flush()
}

// dumpFullMM4 logs the full MM4 request (headers + body preview).
// Uses MM4_DEBUG to avoid flooding logs in normal operation.
func (s *Session) dumpFullMM4(reason string) {
	lm := s.Server.gateway.LogManager

	// Turn this on ALWAYS for "reason" like missing headers,
	// but still respect MM4_DEBUG for noisy cases if you want.
	debug := strings.ToLower(os.Getenv("MM4_DEBUG")) == "true"

	// Flatten headers into a simple map[string]string for logging
	headersFlat := make(map[string]string, len(s.Headers))
	for k, v := range s.Headers {
		headersFlat[k] = strings.Join(v, ", ")
	}

	// Body preview (avoid logging huge binary blobs)
	const maxPreview = 4096
	bodyPreview := s.Data
	truncated := false
	if len(bodyPreview) > maxPreview {
		bodyPreview = bodyPreview[:maxPreview]
		truncated = true
	}

	level := logrus.InfoLevel
	if !debug {
		// In non-debug mode, keep it at Info only for serious reasons,
		// you can change this to DebugLevel if it gets too noisy.
		level = logrus.InfoLevel
	} else {
		level = logrus.DebugLevel
	}

	lm.SendLog(lm.BuildLog(
		"Server.MM4.Raw",
		"MM4RawRequest",
		level,
		map[string]interface{}{
			"reason":           reason,
			"client":           safeClientUsername(s.Client),
			"ip":               s.ClientIP,
			"ip_hash":          s.IPHash,
			"remote_addr":      s.RemoteAddr,
			"envelope_from":    s.From,
			"envelope_to":      strings.Join(s.To, ","),
			"headers":          headersFlat,
			"body_len":         len(s.Data),
			"body_truncated":   truncated,
			"body_preview_raw": string(bodyPreview),
		},
	))
}

// helper so we don't nil-deref
func safeClientUsername(c *Client) string {
	if c == nil {
		return ""
	}
	return c.Username
}
