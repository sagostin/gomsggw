package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"go.mongodb.org/mongo-driver/mongo"
	"io"
	"log"
	"math/rand"
	"mime"
	"mime/multipart"
	"net"
	"net/textproto"
	"strconv"
	"strings"
	"sync"
	"time"
)

// File represents an individual file extracted from the MIME multipart message.
type File struct {
	Filename    string
	ContentType string
	Content     []byte
}

type MM4Message struct {
	From          string
	To            string
	Content       []byte
	Headers       textproto.MIMEHeader
	Client        *Client
	Route         *Route
	TransactionID string
	MessageID     string
	Files         []File
}

// MM4Server represents the SMTP server.
type MM4Server struct {
	Addr              string
	Clients           map[string]*Client // Map of IP to Client
	routing           *Routing
	mu                sync.RWMutex
	listener          net.Listener
	inboundMessageCh  chan *MM4Message
	outboundMessageCh chan *MM4Message
	mongo             *mongo.Client
}

// Start begins listening for incoming SMTP connections.
func (s *MM4Server) Start() error {
	clients, err := loadClients()
	if err != nil {
		return fmt.Errorf("failed to load clients: %v", err)
	}

	clientMap := make(map[string]*Client)
	for i := range clients {
		clientMap[clients[i].Username] = &clients[i]
	}

	s.Clients = clientMap

	s.inboundMessageCh = make(chan *MM4Message)
	s.outboundMessageCh = make(chan *MM4Message)

	listener, err := net.Listen("tcp", s.Addr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %v", s.Addr, err)
	}
	s.listener = listener
	defer listener.Close()
	log.Printf("SMTP server listening on %s", s.Addr)

	// Start background handlers
	go s.handleInboundMessages()
	go s.handleOutboundMessages()

	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Printf("failed to accept connection: %v", err)
			continue
		}
		go s.handleConnection(conn)
	}
}

// handleConnection manages an individual SMTP session.
func (s *MM4Server) handleConnection(conn net.Conn) {
	defer conn.Close()
	remoteAddr := conn.RemoteAddr().String()
	ip, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		log.Printf("failed to parse remote address: %v", err)
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
		log.Printf("Access denied for IP: %s", ip)
		return
	}
	log.Printf("Accepted connection from client: %s (IP: %s)", ip)

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

	if err := session.handleSession(); err != nil {
		log.Printf("session error: %v", err)
		writeResponse(writer, "451 Internal server error")
	}
}

// getClientByIP returns the client associated with the given IP address.
func (s *MM4Server) getClientByIP(ip string) *Client {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Loop through the clients to find a matching IP address
	for _, client := range s.Clients {
		if client.Address == ip {
			log.Printf(ip, " ", client.Address)
			return client
		} else {
			log.Printf(ip, " ", client.Address)
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
	Files      []File
	mongo      *mongo.Client
}

// handleSession processes SMTP commands from the client.
func (s *Session) handleSession() error {
	for {
		// Read client input
		line, err := s.Reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				return nil // Client closed the connection
			}
			return err
		}
		line = strings.TrimSpace(line)
		log.Printf("C: %s", line)

		// Handle the command
		if err := s.handleCommand(line); err != nil {
			return err
		}
	}
}

// handleCommand processes a single SMTP command.
func (s *Session) handleCommand(line string) error {
	// Split command and arguments
	parts := strings.SplitN(line, " ", 2)
	cmd := strings.ToUpper(parts[0])
	var arg string
	if len(parts) > 1 {
		arg = parts[1]
	}

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
		"To",
		"From",
	}
	for _, header := range requiredHeaders {
		if s.Headers.Get(header) == "" {
			return fmt.Errorf("missing required header: %s", header)
		}
	}

	msgType := s.Headers.Get("X-Mms-Message-Type")
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

	// Parse MIME parts to extract files
	if err := mm4Message.parseMIMEParts(); err != nil {
		return fmt.Errorf("failed to parse MIME parts: %v", err)
	}

	// Save files to disk or process them as needed
	if err := mm4Message.saveFiles(s.mongo); err != nil {
		println("failed to save files: %v", err)
	}

	switch msgType {
	case "MM4_forward.REQ":
		s.Server.inboundMessageCh <- mm4Message
	case "MM4_forward.RES":
		// Handle MM4_forward.RES if necessary
		log.Println("Received MM4_forward.RES")
	case "MM4_read_reply_report.REQ":
		// Handle MM4_read_reply_report.REQ if necessary
		log.Println("Received MM4_read_reply_report.REQ")
	case "MM4_read_reply_report.RES":
		// Handle MM4_read_reply_report.RES if necessary
		log.Println("Received MM4_read_reply_report.RES")
	default:
		log.Printf("Unknown MM4 message type: %s", msgType)
	}
	return nil
}

// parseMIMEParts parses the MIME multipart content to extract files.
func (m *MM4Message) parseMIMEParts() error {
	// Use the Headers to get the Content-Type
	contentType := m.Headers.Get("Content-Type")
	if contentType == "" {
		return fmt.Errorf("missing Content-Type header")
	}

	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		return fmt.Errorf("failed to parse Content-Type: %v", err)
	}

	if strings.HasPrefix(mediaType, "multipart/") {
		boundary := params["boundary"]
		if boundary == "" {
			return fmt.Errorf("no boundary parameter in Content-Type")
		}

		// Create a multipart reader
		reader := multipart.NewReader(bytes.NewReader(m.Content), boundary)
		for {
			part, err := reader.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				return fmt.Errorf("failed to read part: %v", err)
			}

			// Read the part
			var buf bytes.Buffer
			if _, err := io.Copy(&buf, part); err != nil {
				return fmt.Errorf("failed to read part content: %v", err)
			}

			// Create a File struct
			file := File{
				Filename:    part.FileName(),
				ContentType: part.Header.Get("Content-Type"),
				Content:     buf.Bytes(),
			}
			m.Files = append(m.Files, file)
		}
	} else {
		// Not a multipart message
		// Handle as a single part
		file := File{
			Filename:    "",
			ContentType: mediaType,
			Content:     m.Content,
		}
		m.Files = append(m.Files, file)
	}

	return nil
}

/*// parseMIMEParts parses the MIME multipart content to extract a single file.
func (m *MM4Message) parseMIMEParts() error {
	// Use the Headers to get the Content-Type
	contentType := m.Headers.Get("Content-Type")
	if contentType == "" {
		return fmt.Errorf("missing Content-Type header")
	}

	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		return fmt.Errorf("failed to parse Content-Type: %v", err)
	}

	if strings.HasPrefix(mediaType, "multipart/") {
		boundary := params["boundary"]
		if boundary == "" {
			return fmt.Errorf("no boundary parameter in Content-Type")
		}

		// Create a multipart reader
		reader := multipart.NewReader(bytes.NewReader(m.Content), boundary)
		for {
			part, err := reader.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				return fmt.Errorf("failed to read part: %v", err)
			}

			// Read the part
			var buf bytes.Buffer
			if _, err := io.Copy(&buf, part); err != nil {
				return fmt.Errorf("failed to read part content: %v", err)
			}

			// Create a File struct
			file := File{
				Filename:    part.FileName(),
				ContentType: part.Header.Get("Content-Type"),
				Content:     buf.Bytes(),
			}
			m.Files = append(m.Files, file)

			// Since only one media item is expected, break after the first file
			break
		}

		// Validate that exactly one file is present
		if len(m.Files) != 1 {
			return fmt.Errorf("expected exactly one media file, got %d", len(m.Files))
		}
	} else {
		// Not a multipart message; handle as a single part
		file := File{
			Filename:    "",
			ContentType: mediaType,
			Content:     m.Content,
		}
		m.Files = append(m.Files, file)
	}

	return nil
}*/

// saveFiles saves each extracted file to disk.
func (m *MM4Message) saveFiles(client *mongo.Client) error {
	for _, file := range m.Files {
		_, err := saveBase64ToMongoDB(client, file.Filename, string(file.Content), file.ContentType)
		if err != nil {
			return err
		}
	}
	return nil
}

// handleInboundMessages processes inbound MM4 messages.
func (s *MM4Server) handleInboundMessages() {
	for msg := range s.inboundMessageCh {
		go func(m *MM4Message) {
			// Determine the route based on the destination
			route := s.findRoute(m.From, m.To)
			if route == nil {
				log.Printf("No route found for message: From=%s, To=%s", m.From, m.To)
				return
			}
			m.Route = route

			switch route.Type {
			case "carrier":
				log.Printf("Routing message via carrier: %s", route.Endpoint)
				err := route.Handler.SendMMS(m)
				if err != nil {
					log.Printf("Error sending message via carrier: %v", err)
				}
			case "mm4":
				err := s.sendMM4ToClient(route.Endpoint, m)
				if err != nil {
					log.Printf("Error sending MM4 message: %v", err)
				}
			default:
				log.Printf("Unknown route type: %s", route.Type)
			}
		}(msg)
	}
}

// handleOutboundMessages processes outbound MM4 messages to clients.
func (s *MM4Server) handleOutboundMessages() {
	for msg := range s.outboundMessageCh {
		go func(m *MM4Message) {
			client := s.getClientByNumber(m.To)
			if client == nil {
				log.Printf("No client found for destination number: %s", m.To)
				return
			}
			// Implement the logic to send the message to the client
			log.Printf("Sending message to client: %s", client.Username)
			// For example, enqueue the message to the client's inbound channel
			//client.InboundCh <- m
		}(msg)
	}
}

// findRoute determines the appropriate route for a message.
func (s *MM4Server) findRoute(source, destination string) *Route {
	// First, try to find a route based on the carrier
	carrier, err := s.getCarrierByNumber(source)
	if err == nil && carrier != "" {
		for _, route := range s.routing.Routes {
			if route.Type == "carrier" && route.Endpoint == carrier {
				return route
			}
		}
	} else {
		destinationAddress := s.getIPByRecipient(destination)
		if destinationAddress != "" {
			return &Route{Prefix: "0", Type: "mm4", Endpoint: destinationAddress}
		}
	}

	return nil
}

// sendMM4ToClient sends an MM4 message to a client over plain TCP with base64-encoded media.
func (s *MM4Server) sendMM4ToClient(destinationIP string, mm4Message *MM4Message) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Find the client by destination IP
	var client *Client
	for _, c := range s.Clients {
		if c.Address == destinationIP {
			client = c
			break
		}
	}

	if client == nil {
		log.Printf("No client found for destination IP: %s", destinationIP)
		return fmt.Errorf("no client found for destination IP: %s", destinationIP)
	}

	// Use default MM4 port if not specified
	port := "25" // Default SMTP port

	// Combine address and port
	address := net.JoinHostPort(client.Address, port)

	// Establish a plain TCP connection to the client's MM4 server with a timeout
	conn, err := net.DialTimeout("tcp", address, 10*time.Second)
	if err != nil {
		log.Printf("Failed to connect to client's MM4 server at %s - %v", address, err)
		return err
	}
	defer conn.Close()

	// Set read and write deadlines to prevent hanging
	conn.SetDeadline(time.Now().Add(30 * time.Second))

	reader := bufio.NewReader(conn)
	writer := bufio.NewWriter(conn)

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
		return err
	}

	// Send QUIT command to terminate the session gracefully
	if err := session.sendCommand("QUIT"); err != nil {
		return err
	}
	response, err = session.readResponse()
	if err != nil {
		return err
	}
	if !strings.HasPrefix(response, "221") {
		return fmt.Errorf("QUIT command failed: %s", response)
	}

	log.Printf("MM4 message sent to client %s at %s", client.Username, address)
	return nil
}

// sendCommand sends a command to the SMTP/MM4 server.
func (s *Session) sendCommand(cmd string) error {
	log.Printf("C: %s", cmd)
	_, err := s.Writer.WriteString(cmd + "\r\n")
	if err != nil {
		return fmt.Errorf("failed to send command '%s': %v", cmd, err)
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
	log.Printf("S: %s", response)

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

// generateContentID creates a unique Content-ID.
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
func generateSMIL(files []File) ([]byte, error) {
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

// sendMM4Message sends the MAIL FROM, RCPT TO, DATA commands, constructs message headers,
// includes the SMIL and media files, and terminates with a dot.
func (s *Session) sendMM4Message() error {
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
			return fmt.Errorf("RCPT TO command failed for %s: %s", recipient, response)
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

	// Step 4: Building the MIME Multipart Message
	var messageBuffer bytes.Buffer

	// Step 4.1: Generate a Unique Boundary
	boundary := generateBoundary()

	// Step 4.2: Construct the Content-Type Header
	reconstructedContentType := fmt.Sprintf("multipart/related; Start=0.smil; Type=\"application/smil\"; boundary=\"%s\"", boundary)
	messageBuffer.WriteString(fmt.Sprintf("From: %s\r\n", s.From))
	messageBuffer.WriteString(fmt.Sprintf("To: %s\r\n", strings.Join(s.To, ", ")))
	messageBuffer.WriteString(fmt.Sprintf("Content-Type: %s\r\n", reconstructedContentType))
	messageBuffer.WriteString(fmt.Sprintf("MIME-Version: 1.0\r\n"))

	// Step 4.3: Add Additional Headers
	// Define headers to exclude to avoid duplication
	headerKeysToExclude := map[string]bool{
		"Content-Type":            true,
		"MIME-Version":            true,
		"X-Mms-3GPP-Mms-Version":  true,
		"X-Mms-Message-Type":      true,
		"X-Mms-Message-Id":        true,
		"X-Mms-Transaction-Id":    true,
		"X-Mms-Ack-Request":       true,
		"X-Mms-Originator-System": true,
		"Date":                    true,
		"From":                    true,
		"To":                      true,
		"Subject":                 true,
		"Message-ID":              true,
	}

	// Add headers excluding the ones defined above
	for key, values := range s.Headers {
		if _, exists := headerKeysToExclude[key]; exists {
			continue
		}
		for _, value := range values {
			messageBuffer.WriteString(fmt.Sprintf("%s: %s\r\n", key, value))
		}
	}

	// Re-add essential headers
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
			return fmt.Errorf("failed to generate SMIL content: %v", err)
		}

		smilFile := File{
			Filename:    "0.smil",
			ContentType: "application/smil",
			Content:     smilContent,
		}

		// Start boundary for SMIL
		messageBuffer.WriteString(fmt.Sprintf("--%s\r\n", boundary))
		messageBuffer.WriteString("Content-Id: <0.smil>\r\n")
		messageBuffer.WriteString(fmt.Sprintf("Content-Type: %s; name=\"%s\"\r\n", smilFile.ContentType, smilFile.Filename))
		messageBuffer.WriteString("\r\n")
		messageBuffer.Write(smilFile.Content)
		messageBuffer.WriteString("\r\n")
	}

	// Step 4.5: Add Media Files as Subsequent MIME Parts
	for _, file := range s.Files {
		// Skip adding the SMIL file again if it's already added
		if file.ContentType == "application/smil" && file.Filename == "0.smil" {
			continue
		}

		// Start boundary
		messageBuffer.WriteString(fmt.Sprintf("--%s\r\n", boundary))

		// Assign a unique Content-ID
		contentID := generateContentID()

		messageBuffer.WriteString(fmt.Sprintf("Content-Id: <%s>\r\n", contentID))
		messageBuffer.WriteString(fmt.Sprintf("Content-Type: %s; name=\"%s\"\r\n", file.ContentType, file.Filename))
		messageBuffer.WriteString(fmt.Sprintf("Content-Disposition: attachment; filename=\"%s\"\r\n", file.Filename))
		messageBuffer.WriteString(fmt.Sprintf("Content-Location: %s\r\n", file.Filename))
		messageBuffer.WriteString("Content-Transfer-Encoding: base64\r\n")
		messageBuffer.WriteString("\r\n")

		// Encode the media content in base64 with line breaks every 76 characters as per RFC 2045
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
	log.Printf("C: [DATA]\n%s", msgData)
	_, err = s.Writer.WriteString(msgData)
	if err != nil {
		return fmt.Errorf("failed to send message data: %v", err)
	}
	err = s.Writer.Flush()
	if err != nil {
		return fmt.Errorf("failed to flush message data: %v", err)
	}

	// Step 6: Read server's response after sending data
	response, err = s.readResponse()
	if err != nil {
		return err
	}
	if !strings.HasPrefix(response, "250") {
		return fmt.Errorf("message sending failed: %s", response)
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

/*func (s *MM4Server) handleOutboundMessages() {
	for msg := range s.outboundMessageCh {
		go func(m *MM4Message) {
			// Assume that m.To contains the destination IP or that you have a mapping from the recipient to the client's IP
			destinationIP := s.getIPByRecipient(m.To)
			if destinationIP == "" {
				log.Printf("No destination IP found for recipient: %s", m.To)
				return
			}

			err := s.sendMM4ToClient(destinationIP, m)
			if err != nil {
				log.Printf("Error sending MM4 message: %v", err)
			}
		}(msg)
	}
}*/

// Helper function to get the client's IP by recipient address
func (s *MM4Server) getIPByRecipient(recipient string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, client := range s.Clients {
		for _, num := range client.Numbers {

			if strings.Contains(recipient, num.Number) {
				return client.Address
			}
		}
	}
	return ""
}

// getCarrierByNumber returns the carrier associated with a phone number.
func (s *MM4Server) getCarrierByNumber(number string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, client := range s.Clients {
		for _, num := range client.Numbers {
			if strings.Contains(number, num.Number) {
				return num.Carrier, nil
			}
		}
	}
	return "", fmt.Errorf("no carrier found for number: %s", number)
}

// getClientByNumber returns the client associated with a phone number.
func (s *MM4Server) getClientByNumber(number string) *Client {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, client := range s.Clients {
		for _, num := range client.Numbers {
			if num.Number == number {
				return client
			}
		}
	}
	return nil
}

// writeResponse sends a response to the client.
func writeResponse(writer *bufio.Writer, response string) {
	log.Printf("S: %s", response)
	writer.WriteString(response + "\r\n")
	writer.Flush()
}
