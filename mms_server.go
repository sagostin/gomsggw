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

// MM4Server represents the SMTP/MM4 server.
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

// Start begins listening for incoming SMTP/MM4 connections.
func (s *MM4Server) Start() error {
	lm := s.gateway.LogManager

	s.connectedClients = make(map[string]time.Time)
	s.MediaTranscodeChan = make(chan *MM4Message)

	go s.transcodeMedia()

	listen, err := net.Listen("tcp", s.Addr)
	if err != nil {
		lm.SendLog(lm.BuildLog(
			"Server.MM4.Start",
			"ListenError",
			logrus.FatalLevel,
			map[string]interface{}{
				"addr": s.Addr,
			}, err,
		))
		return err
	}
	defer listen.Close()

	var proxyListener net.Listener
	if os.Getenv("HAPROXY_PROXY_PROTOCOL") == "true" {
		lm.SendLog(lm.BuildLog(
			"Server.MM4.Start",
			"UsingProxyProtocol",
			logrus.InfoLevel,
			map[string]interface{}{
				"addr": s.Addr,
			},
		))
		proxyListener = &proxyproto.Listener{Listener: listen}
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
			if ne, ok := err.(net.Error); ok && ne.Temporary() {
				lm.SendLog(lm.BuildLog(
					"Server.MM4.Start",
					"AcceptTemporaryError",
					logrus.WarnLevel,
					map[string]interface{}{
						"addr": s.Addr,
					}, err,
				))
				continue
			}

			lm.SendLog(lm.BuildLog(
				"Server.MM4.Start",
				"AcceptFatalError",
				logrus.ErrorLevel,
				map[string]interface{}{
					"addr": s.Addr,
				}, err,
			))
			return err
		}

		go s.handleConnection(conn)
	}
}

// handleConnection manages an individual SMTP/MM4 session.
func (s *MM4Server) handleConnection(conn net.Conn) {
	defer conn.Close()
	lm := s.gateway.LogManager

	remoteAddr := conn.RemoteAddr().String()
	ip, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		lm.SendLog(lm.BuildLog(
			"Server.MM4.HandleConnection",
			"ParseAddressError",
			logrus.ErrorLevel,
			map[string]interface{}{
				"remoteAddr": remoteAddr,
			}, err,
		))
		return
	}
	hashedIP := hashIP(ip)

	lm.SendLog(lm.BuildLog(
		"Server.MM4.HandleConnection",
		"ConnectionAccepted",
		logrus.InfoLevel,
		map[string]interface{}{
			"ip":         ip,
			"remoteAddr": remoteAddr,
			"hashedIP":   hashedIP,
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
				"AccessDeniedTrustedProxy",
				logrus.WarnLevel,
				map[string]interface{}{
					"ip": ip,
				},
			))
			return
		}

		lm.SendLog(lm.BuildLog(
			"Server.MM4.HandleConnection",
			"AuthFailedUnknownIP",
			logrus.WarnLevel,
			map[string]interface{}{
				"ip": ip,
			},
		))
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
			"AuthSuccessNewConnection",
			logrus.InfoLevel,
			map[string]interface{}{
				"client": client.Username,
				"ip":     ip,
			},
		))
	} else {
		// Existing connection
		inactivityDuration := time.Since(lastActivity)
		if inactivityDuration > 2*time.Minute {
			lm.SendLog(lm.BuildLog(
				"Server.MM4.HandleConnection",
				"ReconnectAfterInactivity",
				logrus.InfoLevel,
				map[string]interface{}{
					"client":             client.Username,
					"ip":                 ip,
					"inactivity_seconds": inactivityDuration.Seconds(),
				},
			))
		} else {
			lm.SendLog(lm.BuildLog(
				"Server.MM4.HandleConnection",
				"Reconnect",
				logrus.DebugLevel,
				map[string]interface{}{
					"client":             client.Username,
					"ip":                 ip,
					"inactivity_seconds": inactivityDuration.Seconds(),
				},
			))
		}
	}

	// ALWAYS update the last connection time, regardless of inactivity
	s.mu.Lock()
	s.connectedClients[hashedIP] = time.Now()
	s.mu.Unlock()

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

	lm.SendLog(lm.BuildLog(
		"Server.MM4.HandleConnection",
		"SessionStarted",
		logrus.DebugLevel,
		map[string]interface{}{
			"client": client.Username,
			"ip":     ip,
		},
	))

	// Handle the session
	if err := session.handleSession(s); err != nil {
		lm.SendLog(lm.BuildLog(
			"Server.MM4.HandleConnection",
			"SessionError",
			logrus.ErrorLevel,
			map[string]interface{}{
				"client": client.Username,
				"ip":     ip,
			}, err,
		))
		writeResponse(writer, "451 Internal server error")
	}

	lm.SendLog(lm.BuildLog(
		"Server.MM4.HandleConnection",
		"SessionEnded",
		logrus.DebugLevel,
		map[string]interface{}{
			"client": client.Username,
			"ip":     ip,
		},
	))
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

// Session represents an SMTP/MM4 session.
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

// handleSession processes SMTP commands from the client.
func (s *Session) handleSession(srv *MM4Server) error {
	lm := s.Server.gateway.LogManager

	for {
		line, err := s.Reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				lm.SendLog(lm.BuildLog(
					"Server.MM4.Session",
					"ClientDisconnectedEOF",
					logrus.DebugLevel,
					map[string]interface{}{
						"client": s.Client.Username,
						"ip":     s.ClientIP,
					},
				))
				return nil
			}
			lm.SendLog(lm.BuildLog(
				"Server.MM4.Session",
				"ReadLineError",
				logrus.ErrorLevel,
				map[string]interface{}{
					"client": s.Client.Username,
					"ip":     s.ClientIP,
				}, err,
			))
			return err
		}

		line = strings.TrimSpace(line)

		lm.SendLog(lm.BuildLog(
			"Server.MM4.Session",
			"CommandReceived",
			logrus.DebugLevel,
			map[string]interface{}{
				"client":  s.Client.Username,
				"ip":      s.ClientIP,
				"command": line,
			},
		))

		if err := s.handleCommand(line, srv); err != nil {
			return err
		}
	}
}

// handleCommand processes a single SMTP command.
func (s *Session) handleCommand(line string, srv *MM4Server) error {
	lm := s.Server.gateway.LogManager

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

	switch cmd {
	case "HELO", "EHLO":
		writeResponse(s.Writer, "250 Hello")
	case "MAIL":
		if err := s.handleMail(arg); err != nil {
			lm.SendLog(lm.BuildLog(
				"Server.MM4.HandleCommand",
				"MAILError",
				logrus.WarnLevel,
				map[string]interface{}{
					"client": s.Client.Username,
					"ip":     s.ClientIP,
					"arg":    arg,
				}, err,
			))
			writeResponse(s.Writer, fmt.Sprintf("550 %v", err))
		} else {
			writeResponse(s.Writer, "250 OK")
		}
	case "RCPT":
		if err := s.handleRcpt(arg); err != nil {
			lm.SendLog(lm.BuildLog(
				"Server.MM4.HandleCommand",
				"RCPTErrror",
				logrus.WarnLevel,
				map[string]interface{}{
					"client": s.Client.Username,
					"ip":     s.ClientIP,
					"arg":    arg,
				}, err,
			))
			writeResponse(s.Writer, fmt.Sprintf("550 %v", err))
		} else {
			writeResponse(s.Writer, "250 OK")
		}
	case "DATA":
		writeResponse(s.Writer, "354 End data with <CR><LF>.<CR><LF>")
		if err := s.handleData(); err != nil {
			lm.SendLog(lm.BuildLog(
				"Server.MM4.HandleCommand",
				"HandleDataError",
				logrus.ErrorLevel,
				map[string]interface{}{
					"client": s.Client.Username,
					"ip":     s.ClientIP,
				}, err,
			))
			writeResponse(s.Writer, fmt.Sprintf("554 %v", err))
		} else {
			writeResponse(s.Writer, "250 message queued for processing")
		}
	case "NOOP":
		writeResponse(s.Writer, "250 OK")
	case "QUIT":
		writeResponse(s.Writer, "221 Bye")
		return errors.New("client disconnected")
	default:
		lm.SendLog(lm.BuildLog(
			"Server.MM4.HandleCommand",
			"UnknownCommand",
			logrus.DebugLevel,
			map[string]interface{}{
				"client": s.Client.Username,
				"ip":     s.ClientIP,
				"cmd":    cmd,
				"arg":    arg,
			},
		))
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
	return nil
}

// handleRcpt processes the RCPT TO command.
func (s *Session) handleRcpt(arg string) error {
	if !strings.HasPrefix(strings.ToUpper(arg), "TO:") {
		return errors.New("syntax error in RCPT command")
	}
	recipient := strings.TrimSpace(arg[3:])
	s.To = append(s.To, recipient)
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
	return s.handleMM4Message()
}

// handleMM4Message processes the MM4 message based on its type.
func (s *Session) handleMM4Message() error {
	lm := s.Server.gateway.LogManager

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
			lm.SendLog(lm.BuildLog(
				"Server.MM4.handleMM4Message",
				"MissingRequiredHeader",
				logrus.WarnLevel,
				map[string]interface{}{
					"client": s.Client.Username,
					"ip":     s.ClientIP,
					"header": header,
				},
			))
			return fmt.Errorf("missing required header: %s", header)
		}
	}

	transactionID := s.Headers.Get("X-Mms-Transaction-ID")
	messageID := s.Headers.Get("X-Mms-message-ID")
	msgType := s.Headers.Get("X-Mms-message-Type")

	mm4Message := &MM4Message{
		From:          s.Headers.Get("From"),
		To:            s.Headers.Get("To"),
		Content:       s.Data,
		Headers:       s.Headers,
		Client:        s.Client,
		TransactionID: transactionID,
		MessageID:     messageID,
	}

	lm.SendLog(lm.BuildLog(
		"Server.MM4.handleMM4Message",
		"MM4MessageReceived",
		logrus.InfoLevel,
		map[string]interface{}{
			"client":        s.Client.Username,
			"ip":            s.ClientIP,
			"transactionID": transactionID,
			"messageID":     messageID,
			"msgType":       msgType,
		},
	))

	// Parse MIME parts to extract files
	mm, err := mm4Message.parseMIMEParts()
	if err != nil {
		lm.SendLog(lm.BuildLog(
			"Server.MM4.handleMM4Message",
			"ParseMIMEPartsError",
			logrus.ErrorLevel,
			map[string]interface{}{
				"client":        s.Client.Username,
				"ip":            s.ClientIP,
				"transactionID": transactionID,
				"messageID":     messageID,
			}, err,
		))
		return fmt.Errorf("failed to parse MIME parts: %v", err)
	}

	lm.SendLog(lm.BuildLog(
		"Server.MM4.handleMM4Message",
		"MM4QueuedForTranscode",
		logrus.DebugLevel,
		map[string]interface{}{
			"client":        s.Client.Username,
			"ip":            s.ClientIP,
			"transactionID": transactionID,
			"messageID":     messageID,
			"fileCount":     len(mm.Files),
		},
	))

	s.Server.MediaTranscodeChan <- mm

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
		return nil, fmt.Errorf("failed to parse Content-Type: %v", err)
	}

	if strings.HasPrefix(mediaType, "multipart/") {
		boundary := params["boundary"]
		if boundary == "" {
			return nil, fmt.Errorf("no boundary parameter in Content-Type")
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
	lm := s.gateway.LogManager

	if item.files == nil {
		return fmt.Errorf("files are nil")
	}

	newStr := strings.Replace(item.To, "+", "", -1)
	client := s.gateway.getClient(newStr)
	if client == nil {
		lm.SendLog(lm.BuildLog(
			"Server.MM4.sendMM4",
			"NoClientForDestination",
			logrus.ErrorLevel,
			map[string]interface{}{
				"to": item.To,
			},
		))
		return fmt.Errorf("no client found for destination number: %s", item.To)
	}

	port := "25" // TODO: configurable
	address := net.JoinHostPort(client.Address, port)

	lm.SendLog(lm.BuildLog(
		"Server.MM4.sendMM4",
		"ConnectingToClientMM4",
		logrus.InfoLevel,
		map[string]interface{}{
			"client": client.Username,
			"addr":   address,
			"to":     item.To,
			"from":   item.From,
		},
	))

	conn, err := net.DialTimeout("tcp", address, 10*time.Second)
	if err != nil {
		lm.SendLog(lm.BuildLog(
			"Server.MM4.sendMM4",
			"ConnectError",
			logrus.ErrorLevel,
			map[string]interface{}{
				"client": client.Username,
				"addr":   address,
			}, err,
		))
		return fmt.Errorf("failed to connect to client's MM4 server at %s", address)
	}
	defer conn.Close()

	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))

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
			"Server.MM4.sendMM4",
			"GreetingReadError",
			logrus.ErrorLevel,
			map[string]interface{}{
				"client": client.Username,
				"addr":   address,
			}, err,
		))
		return fmt.Errorf("failed to read server greeting: %v", err)
	}
	if !strings.HasPrefix(response, "220") {
		lm.SendLog(lm.BuildLog(
			"Server.MM4.sendMM4",
			"UnexpectedGreeting",
			logrus.ErrorLevel,
			map[string]interface{}{
				"client":    client.Username,
				"addr":      address,
				"response":  response,
				"stage":     "greeting",
				"direction": "recv",
			},
		))
		return fmt.Errorf("unexpected server greeting: %s", response)
	}

	// EHLO
	if err := session.sendCommand("EHLO localhost"); err != nil {
		lm.SendLog(lm.BuildLog(
			"Server.MM4.sendMM4",
			"EHLOSendError",
			logrus.ErrorLevel,
			map[string]interface{}{
				"client": client.Username,
				"addr":   address,
			}, err,
		))
		return err
	}
	response, err = session.readResponse()
	if err != nil {
		lm.SendLog(lm.BuildLog(
			"Server.MM4.sendMM4",
			"EHLOReadError",
			logrus.ErrorLevel,
			map[string]interface{}{
				"client": client.Username,
				"addr":   address,
			}, err,
		))
		return err
	}
	if !strings.HasPrefix(response, "250") {
		lm.SendLog(lm.BuildLog(
			"Server.MM4.sendMM4",
			"EHLOFailed",
			logrus.ErrorLevel,
			map[string]interface{}{
				"client":   client.Username,
				"addr":     address,
				"response": response,
			},
		))
		return fmt.Errorf("EHLO command failed: %s", response)
	}

	// Send MM4 DATA
	if err := session.sendMM4Message(); err != nil {
		lm.SendLog(lm.BuildLog(
			"Server.MM4.sendMM4",
			"SendMM4Error",
			logrus.ErrorLevel,
			map[string]interface{}{
				"client": client.Username,
				"addr":   address,
			}, err,
		))
		return fmt.Errorf("send MM4 failed: %v", err)
	}

	// QUIT
	if err := session.sendCommand("QUIT"); err != nil {
		lm.SendLog(lm.BuildLog(
			"Server.MM4.sendMM4",
			"QUITSendError",
			logrus.ErrorLevel,
			map[string]interface{}{
				"client": client.Username,
				"addr":   address,
			}, err,
		))
		return fmt.Errorf("send QUIT failed: %v", err)
	}
	response, err = session.readResponse()
	if err != nil {
		lm.SendLog(lm.BuildLog(
			"Server.MM4.sendMM4",
			"QUITReadError",
			logrus.ErrorLevel,
			map[string]interface{}{
				"client": client.Username,
				"addr":   address,
			}, err,
		))
		return err
	}
	if !strings.HasPrefix(response, "221") {
		lm.SendLog(lm.BuildLog(
			"Server.MM4.sendMM4",
			"QUITFailed",
			logrus.ErrorLevel,
			map[string]interface{}{
				"client":   client.Username,
				"addr":     address,
				"response": response,
			},
		))
		return fmt.Errorf("QUIT command failed: %s", response)
	}

	lm.SendLog(lm.BuildLog(
		"Server.MM4.sendMM4",
		"MM4SendSuccess",
		logrus.InfoLevel,
		map[string]interface{}{
			"client": client.Username,
			"addr":   address,
			"to":     item.To,
			"from":   item.From,
			"files":  len(item.files),
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

	files := make([]MsgFile, 0, len(msgItem.files))
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
	lm := s.Server.gateway.LogManager

	line := strings.TrimSpace(cmd)
	lm.SendLog(lm.BuildLog(
		"Server.MM4.sendCommand",
		"CommandSend",
		logrus.DebugLevel,
		map[string]interface{}{
			"client": s.Client.Username,
			"ip":     s.ClientIP,
			"cmd":    line,
		},
	))

	if _, err := s.Writer.WriteString(cmd + "\r\n"); err != nil {
		return fmt.Errorf("failed to send command '%s': %v", cmd, err)
	}
	if err := s.Writer.Flush(); err != nil {
		return fmt.Errorf("failed to flush command '%s': %v", cmd, err)
	}
	return nil
}

// readResponse reads the server's response after sending a command.
func (s *Session) readResponse() (string, error) {
	lm := s.Server.gateway.LogManager

	response, err := s.Reader.ReadString('\n')
	if err != nil {
		return "", fmt.Errorf("failed to read server response: %v", err)
	}
	response = strings.TrimSpace(response)

	lm.SendLog(lm.BuildLog(
		"Server.MM4.readResponse",
		"ResponseReceived",
		logrus.DebugLevel,
		map[string]interface{}{
			"client":   s.Client.Username,
			"ip":       s.ClientIP,
			"response": response,
		},
	))

	if len(response) >= 3 {
		codeStr := response[:3]
		code, err := strconv.Atoi(codeStr)
		if err == nil && code >= 400 {
			return response, fmt.Errorf("server responded with error: %s", response)
		}
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
	lm := s.Server.gateway.LogManager

	if len(s.Files) <= 0 {
		return fmt.Errorf("no files found")
	}

	// MAIL FROM
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

	// RCPT TO
	for _, recipient := range s.To {
		rcptToCmd := fmt.Sprintf("RCPT TO:<%s>", recipient)
		if err := s.sendCommand(rcptToCmd); err != nil {
			return err
		}
		response, err = s.readResponse()
		if err != nil {
			return err
		}
		if !strings.HasPrefix(response, "250") {
			return fmt.Errorf("RCPT TO command failed for %s: %s", recipient, response)
		}
	}

	// DATA
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

	var messageBuffer bytes.Buffer

	boundary := generateBoundary()

	// Headers
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

	// SMIL part
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

	// Media parts
	for i, file := range s.Files {
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
		for j := 0; j < len(encoded); j += 76 {
			end := j + 76
			if end > len(encoded) {
				end = len(encoded)
			}
			messageBuffer.WriteString(encoded[j:end] + "\r\n")
		}

		lm.SendLog(lm.BuildLog(
			"Server.MM4.sendMM4Message",
			"AttachedFile",
			logrus.DebugLevel,
			map[string]interface{}{
				"client":      s.Client.Username,
				"ip":          s.ClientIP,
				"index":       i,
				"filename":    file.Filename,
				"contentType": file.ContentType,
				"sizeBytes":   len(file.Content),
			},
		))
	}

	messageBuffer.WriteString(fmt.Sprintf("--%s--\r\n", boundary))
	messageBuffer.WriteString(".\r\n")

	msgData := messageBuffer.String()
	if _, err := s.Writer.WriteString(msgData); err != nil {
		return err
	}
	if err := s.Writer.Flush(); err != nil {
		return err
	}

	response, err = s.readResponse()
	if err != nil {
		return err
	}
	if !strings.HasPrefix(response, "250") {
		return fmt.Errorf("DATA final response not 250: %s", response)
	}

	lm.SendLog(lm.BuildLog(
		"Server.MM4.sendMM4Message",
		"MessageSent",
		logrus.InfoLevel,
		map[string]interface{}{
			"client":    s.Client.Username,
			"ip":        s.ClientIP,
			"to":        s.To,
			"from":      s.From,
			"fileCount": len(s.Files),
		},
	))

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
	// low-level helper; MM4-specific logging happens at call sites
	writer.WriteString(response + "\r\n")
	writer.Flush()
}
