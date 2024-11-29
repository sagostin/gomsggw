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

	listen, err := net.Listen("tcp", s.Addr)
	if err != nil {
		return err
	}
	//s.listener = listen
	defer listen.Close()

	var proxyListener net.Listener

	if os.Getenv("HAPROXY_PROXY_PROTOCOL") == "true" {
		proxyListener = &proxyproto.Listener{Listener: listen}
		defer proxyListener.Close()

		// Wait for a connection and accept it
		conn, err := proxyListener.Accept()
		if err != nil {
			return err
		}

		defer func(conn net.Conn) {
			err := conn.Close()
			if err != nil {
				log.Fatal("couldn't close proxy connection")
			}
		}(conn)

		// Print connection details
		if conn.LocalAddr() == nil {
			log.Fatal("couldn't retrieve local address")
		}
		log.Printf("local address: %q", conn.LocalAddr().String())

		if conn.RemoteAddr() == nil {
			log.Fatal("couldn't retrieve remote address")
		}
		log.Printf("remote address: %q", conn.RemoteAddr().String())
	} else {
		proxyListener = listen
	}

	s.listener = proxyListener

	// Start the cleanup goroutine
	go s.cleanupInactiveClients(2*time.Minute, 1*time.Minute)

	for {
		conn, err := proxyListener.Accept()
		if err != nil {
			return err
		}
		go s.handleConnection(conn)
	}
}

// cleanupInactiveClients periodically removes inactive clients.
func (s *MM4Server) cleanupInactiveClients(timeout time.Duration, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			now := time.Now()
			s.mu.Lock()
			for hashedIP, lastActivity := range s.connectedClients {
				if now.Sub(lastActivity) > timeout {
					var lm = s.gateway.LogManager
					lm.SendLog(lm.BuildLog(
						"Server.MM4.CleanInactive",
						"MM4RemoveInactiveClient",
						logrus.InfoLevel,
						map[string]interface{}{
							"client": hashedIP,
						},
					))

					delete(s.connectedClients, hashedIP)
				}
			}
			s.mu.Unlock()
		}
	}
}

// handleConnection manages an individual SMTP session.
func (s *MM4Server) handleConnection(conn net.Conn) {
	defer conn.Close()
	var lm = s.gateway.LogManager

	remoteAddr := conn.RemoteAddr().String()
	ip, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		lm.SendLog(lm.BuildLog(
			"Server.MM4.HandleConnection",
			"ParseAddressError",
			logrus.InfoLevel,
			map[string]interface{}{
				"client": "unknown",
			},
		))
		return
	}
	hashedIP := hashIP(ip)

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
			return
		} else {
			lm.SendLog(lm.BuildLog(
				"Server.MM4.HandleConnection",
				"AuthFailed",
				logrus.WarnLevel,
				map[string]interface{}{
					"client": "unknown",
					"ip":     ip,
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
		/*logf.Level = logrus.InfoLevel // server (mm4/smpp), success/failed (err), userid, ip
		logf.Message = fmt.Sprintf(LogMessages.Authentication, logf.AdditionalData["type"], "success", client.Username, ip)
		logf.Print()*/

		lm.SendLog(lm.BuildLog(
			"Server.MM4.HandleConnection",
			"AuthSuccess",
			logrus.InfoLevel,
			map[string]interface{}{
				"client": client.Username,
				"ip":     ip,
			},
		))

		// Add to connectedClients map with current timestamp
		s.mu.Lock()
		s.connectedClients[hashedIP] = time.Now()
		s.mu.Unlock()
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
					"inactivity_seconds": inactivityDuration.Seconds(),
				},
			))
		} else {
			// Optional: Log regular reconnections
			lm.SendLog(lm.BuildLog(
				"Server.MM4.HandleConnection",
				"MM4Reconnect",
				logrus.InfoLevel,
				map[string]interface{}{
					"client":             client.Username,
					"ip":                 ip,
					"inactivity_seconds": inactivityDuration.Seconds(),
				},
			))
		}
	}

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
				"client": client.Username,
				"ip":     ip,
			}, err,
		))
		writeResponse(writer, "451 Internal server error")
	}
}

// getClientByIP returns the client associated with the given IP address.
func (s *MM4Server) getClientByIP(ip string) *Client {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Loop through the clients to find a matching IP address
	for _, client := range s.gateway.Clients {
		if client.Address == ip {
			//log.Printf(ip, " ", client.Address)
			return client
		} else {
			//log.Printf(ip, " ", client.Address)
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

// handleSession processes SMTP commands from the client.
func (s *Session) handleSession(srv *MM4Server) error {
	for {
		// Read client input
		line, err := s.Reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				return nil // AMPQClient closed the connection
			}
			return err
		}
		line = strings.TrimSpace(line)
		/*
			if strings.ToLower(os.Getenv("MM4_DEBUG")) == "true" {
				logf := LoggingFormat{Type: LogType.MM4 + "_" + LogType.DEBUG}
				logf.Level = logrus.DebugLevel
				logf.Message = fmt.Sprintf("IP: %s - C: %s", s.ClientIP, line)
				logf.Print()
			}*/

		// Handle the command
		if err := s.handleCommand(line, srv); err != nil {
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

	// Handle commands
	switch cmd {
	case "HELO", "EHLO":
		writeResponse(s.Writer, "250 Hello")
	case "MAIL":
		if err := s.handleMail(arg); err != nil {
			writeResponse(s.Writer, fmt.Sprintf("550 %v", err))
		} else {
			writeResponse(s.Writer, "250 OK")
		}
	case "RCPT":
		if err := s.handleRcpt(arg); err != nil {
			writeResponse(s.Writer, fmt.Sprintf("550 %v", err))
		} else {
			writeResponse(s.Writer, "250 OK")
		}
	case "DATA":
		writeResponse(s.Writer, "354 End data with <CR><LF>.<CR><LF>")
		if err := s.handleData(); err != nil {
			var lm = s.Server.gateway.LogManager
			lm.SendLog(lm.BuildLog(
				"Server.MM4.HandleCommand",
				"HandleData",
				logrus.InfoLevel,
				map[string]interface{}{
					"client": s.Client.Username,
					"ip":     s.ClientIP,
				},
			))
			writeResponse(s.Writer, fmt.Sprintf("554 %v", err))
		} else {
			writeResponse(s.Writer, "250 OK")
		}
	case "NOOP":
		writeResponse(s.Writer, "250 OK")
	case "QUIT":
		writeResponse(s.Writer, "221 Bye")
		return errors.New("client disconnected")
	default:
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
	// Use textproto.Reader to handle large messages and dot-stuffing
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
	// Check required MM4 headers
	requiredHeaders := []string{
		"X-Mms-3GPP-MMS-Version",
		"X-Mms-Message-Type",
		"X-Mms-Message-ID",
		"X-Mms-Transaction-ID",
		/*"Text-Type",*/
		"From",
		"To",
	}
	for _, header := range requiredHeaders {
		if s.Headers.Get(header) == "" {
			return fmt.Errorf("missing required header: %s", header)
		}
	}

	//_ := s.Headers.Get("X-Mms-Message-Type")
	transactionID := s.Headers.Get("X-Mms-Transaction-ID")
	messageID := s.Headers.Get("X-Mms-Message-ID")

	mm4Message := &MM4Message{
		From:          s.Headers.Get("From"),
		To:            s.Headers.Get("To"),
		Content:       s.Data,
		Headers:       s.Headers,
		Client:        s.Client,
		TransactionID: transactionID,
		MessageID:     messageID,
	}
	// Existing header checks..

	// todo IMPORTANT convert octet stream to other file?? uerm

	// Parse MIME parts to extract files
	mm, err := mm4Message.parseMIMEParts()

	if err != nil {
		return fmt.Errorf("failed to parse MIME parts: %v", err)
	}

	s.Server.MediaTranscodeChan <- mm

	/*msgItem := MsgQueueItem{
		To:                mm.To,
		From:              mm.From,
		ReceivedTimestamp: time.Now(),
		Type:              MsgQueueItemType.MMS,
		Files:             mm.Files,
		LogID:             transId,
	}

	s.Server.gateway.Router.ClientMsgChan <- msgItem*/

	writeResponse(s.Writer, "250 Message queued for processing")

	/*switch msgType {
	case "MM4_forward.REQ":
		s.Server.msgToClientChannel <- mm4Message
	case "MM4_forward.RES":
		// Handle MM4_forward.RES if necessary
		// log.Println("Received MM4_forward.RES")
	case "MM4_read_reply_report.REQ":
		// Handle MM4_read_reply_report.REQ if necessary
		// Println("Received MM4_read_reply_report.REQ")
	case "MM4_read_reply_report.RES":
		// Handle MM4_read_reply_report.RES if necessary
		// log.Println("Received MM4_read_reply_report.RES")
	default:
		// log.Printf("Unknown MM4 message type: %s", msgType)
	}*/
	return nil
}

// parseMIMEParts parses the MIME multipart content to extract files.
func (m *MM4Message) parseMIMEParts() (*MM4Message, error) {
	// Use the Headers to get the Msg-Type
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

		// Create a multipart reader
		reader := multipart.NewReader(bytes.NewReader(m.Content), boundary)
		for {
			part, err := reader.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, fmt.Errorf("failed to read part: %v", err)
			}

			// Read the part
			var buf bytes.Buffer
			if _, err := io.Copy(&buf, part); err != nil {
				return nil, fmt.Errorf("failed to read part content: %v", err)
			}

			// Create a MsgFile struct
			file := MsgFile{
				Filename:    part.FileName(),
				ContentType: part.Header.Get("Content-Type"),
				Content:     buf.Bytes(),
			}
			m.Files = append(m.Files, file)
		}
	} else {
		// Not a multipart message
		// Handle as a single part
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
	if item.Files == nil {
		return fmt.Errorf("files are nil")
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	newStr := strings.Replace(item.To, "+", "", -1)

	// Find the client by destination IP
	client := s.gateway.getClient(newStr)

	if client == nil {
		return fmt.Errorf("no client found for destination number: %s", item.To)
	}

	// Use default MM4 port if not specified
	port := "25" // Default SMTP port todo

	// Combine address and port
	address := net.JoinHostPort(client.Address, port)

	// Establish a plain TCP connection to the client's MM4 server with a timeout
	conn, err := net.DialTimeout("tcp", address, 10*time.Second)
	if err != nil {
		return fmt.Errorf("failed to connect to client's MM4 server at %s", address)
	}
	defer conn.Close()

	// Set read and write deadlines to prevent hanging
	conn.SetDeadline(time.Now().Add(30 * time.Second))

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
		Files:   mm4Message.Files, // Include media file (only 1)
	}

	// Read server's initial response
	response, err := session.readResponse()
	if err != nil {
		return fmt.Errorf("failed to read server greeting: %v", err)
	}
	if !strings.HasPrefix(response, "220") {
		return fmt.Errorf("unexpected server greeting: %s", response)
	}

	// Send EHLO command
	if err := session.sendCommand("EHLO localhost"); err != nil {
		return err
	}
	response, err = session.readResponse()
	if err != nil {
		return err
	}
	if !strings.HasPrefix(response, "250") {
		return fmt.Errorf("EHLO command failed: %s", response)
	}

	// Proceed to send the MM4 message
	if err := session.sendMM4Message(); err != nil {
		return fmt.Errorf("send MM4 failed: %s", response)
	}

	// Send QUIT command to terminate the session gracefully
	if err := session.sendCommand("QUIT"); err != nil {
		return fmt.Errorf("send QUIT failed: %s", response)
	}
	response, err = session.readResponse()
	if err != nil {
		return err
	}
	if !strings.HasPrefix(response, "221") {
		return fmt.Errorf("QUIT command failed: %s", response)
	}

	return nil
}

// createMM4Message constructs an MM4Message with the provided media files.
func (s *MM4Server) createMM4Message(msgItem MsgQueueItem) *MM4Message {
	headers := textproto.MIMEHeader{}
	headers.Set("To", fmt.Sprintf("%s/TYPE=PLMN", msgItem.To))
	headers.Set("From", fmt.Sprintf("%s/TYPE=PLMN", msgItem.From))
	headers.Set("MIME-Version", "1.0")
	headers.Set("X-Mms-3GPP-Mms-Version", "6.10.0")
	headers.Set("X-Mms-Message-Type", "MM4_forward.REQ")
	headers.Set("X-Mms-Message-Id", fmt.Sprintf("<%s@%s>", msgItem.LogID, os.Getenv("MM4_MSG_ID_HOST"))) // todo Replace 'yourdomain.com' appropriately
	headers.Set("X-Mms-Transaction-Id", msgItem.LogID)
	headers.Set("X-Mms-Ack-Request", "Yes")

	originatorSystem := os.Getenv("MM4_ORIGINATOR_SYSTEM") // e.g., "system@108.165.150.61"
	if originatorSystem == "" {
		originatorSystem = "system@yourdomain.com" // Fallback or default value
	}
	headers.Set("X-Mms-Originator-System", originatorSystem)
	headers.Set("Date", time.Now().UTC().Format(time.RFC1123Z))
	// Msg-Type will be set in sendMM4Message based on whether SMIL is included or not

	files := make([]MsgFile, 0)

	for _, f := range msgItem.Files {
		files = append(files, MsgFile{
			Filename:    f.Filename,
			ContentType: f.ContentType,
			Content:     f.Content,
		})
	}

	return &MM4Message{
		From:          msgItem.From,
		To:            msgItem.To,
		Content:       []byte(msgItem.Message),
		Headers:       headers,
		TransactionID: msgItem.LogID,
		MessageID:     msgItem.LogID,
		Files:         files,
	}
}

// sendCommand sends a command to the SMTP/MM4 server.
func (s *Session) sendCommand(cmd string) error {
	/*logf := LoggingFormat{Type: LogType.MM4}

	if strings.ToLower(os.Getenv("MM4_DEBUG")) == "true" {
		line := strings.TrimSpace(cmd)
		logf.Type = LogType.MM4 + "_" + LogType.DEBUG
		logf.Level = logrus.InfoLevel
		logf.Message = fmt.Sprintf("IP: %s - C: %s", s.ClientIP, line)
		logf.Print()
	}*/

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
	/*logf := LoggingFormat{Type: LogType.MM4}*/

	response, err := s.Reader.ReadString('\n')
	if err != nil {
		return "", fmt.Errorf("failed to read server response: %v", err)
	}
	response = strings.TrimSpace(response)

	/*if strings.ToLower(os.Getenv("MM4_DEBUG")) == "true" {
		logf.Type = LogType.MM4 + "_" + LogType.DEBUG
		logf.Level = logrus.InfoLevel
		logf.Message = fmt.Sprintf("IP: %s - S: %s", s.ClientIP, response)
		logf.Print()
	}*/

	// Check for SMTP error codes (4xx and 5xx)
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
		// Add more media types (e.g., audio, video) as needed
	}

	smilBuffer.WriteString("</body>\n")
	smilBuffer.WriteString("</smil>\n")

	return smilBuffer.Bytes(), nil
}

func (s *Session) sendMM4Message() error {
	if len(s.Files) <= 0 {
		return fmt.Errorf("no files found")
	}

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
			return err
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
		return err
	}

	// Step 4: Building the MIME Multipart Message
	var messageBuffer bytes.Buffer

	// Step 4.1: Generate a Unique Boundary
	boundary := generateBoundary()

	// Step 4.2: Construct the Headers
	messageBuffer.WriteString(fmt.Sprintf("To: %s\r\n", strings.Join(s.To, ", ")))
	messageBuffer.WriteString(fmt.Sprintf("From: %s\r\n", s.From))
	messageBuffer.WriteString(fmt.Sprintf("Content-Type: multipart/related; boundary=\"%s\"\r\n", boundary))
	messageBuffer.WriteString("MIME-Version: 1.0\r\n")

	// Step 4.3: Add Additional Headers
	essentialHeaders := []string{
		"X-Mms-3GPP-Mms-Version",
		"X-Mms-Message-Type",
		"X-Mms-Message-Id",
		"X-Mms-Transaction-Id",
		"X-Mms-Ack-Request",
		"X-Mms-Originator-System",
		"Date",
		"Message-ID",
	}
	for _, header := range essentialHeaders {
		if value := s.Headers.Get(header); value != "" {
			messageBuffer.WriteString(fmt.Sprintf("%s: %s\r\n", header, value))
		}
	}

	messageBuffer.WriteString("\r\n") // End of headers

	// Step 4.4: Generate and Add the SMIL Part
	if len(s.Files) > 0 {
		smilContent, err := generateSMIL(s.Files)
		if err != nil {
			return err
		}

		// Add SMIL part
		messageBuffer.WriteString(fmt.Sprintf("--%s\r\n", boundary))
		messageBuffer.WriteString("Content-Id: <0.smil>\r\n")
		messageBuffer.WriteString("Content-Type: application/smil; name=\"0.smil\"\r\n")
		messageBuffer.WriteString("\r\n")
		messageBuffer.Write(smilContent)
		messageBuffer.WriteString("\r\n")
	}

	// Step 4.5: Add Media Files as MIME Parts
	for _, file := range s.Files {
		if file.ContentType == "application/smil" {
			continue // Skip the SMIL file if already added
		}

		// Add media file as MIME part
		contentID := generateContentID()

		messageBuffer.WriteString(fmt.Sprintf("--%s\r\n", boundary))
		messageBuffer.WriteString(fmt.Sprintf("Content-Id: <%s>\r\n", contentID))
		messageBuffer.WriteString(fmt.Sprintf("Content-Type: %s; name=\"%s\"\r\n", file.ContentType, file.Filename))
		messageBuffer.WriteString(fmt.Sprintf("Content-Disposition: attachment; filename=\"%s\"\r\n", file.Filename))
		messageBuffer.WriteString(fmt.Sprintf("Content-Location: %s\r\n", file.Filename))
		messageBuffer.WriteString("Content-Transfer-Encoding: base64\r\n")
		messageBuffer.WriteString("\r\n")

		// Encode the file content in base64 with line breaks every 76 characters
		encoded := base64.StdEncoding.EncodeToString(file.Content)
		for i := 0; i < len(encoded); i += 76 {
			end := i + 76
			if end > len(encoded) {
				end = len(encoded)
			}
			messageBuffer.WriteString(encoded[i:end] + "\r\n")
		}
	}

	// End the multipart message
	messageBuffer.WriteString(fmt.Sprintf("--%s--\r\n", boundary))

	// Terminate the DATA section with a single dot
	messageBuffer.WriteString(".\r\n")

	// Step 5: Send the message data
	msgData := messageBuffer.String()
	_, err = s.Writer.WriteString(msgData)
	if err != nil {
		return err
	}
	err = s.Writer.Flush()
	if err != nil {
		return err
	}

	// Step 6: Read server's response after sending data
	response, err = s.readResponse()
	if err != nil {
		return err
	}
	if !strings.HasPrefix(response, "250") {
		return err
	}

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
	/*log.Printf("S: %s", response)*/
	writer.WriteString(response + "\r\n")
	writer.Flush()
}
