package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"github.com/sirupsen/logrus"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"zultys-smpp-mm4/smpp"
	"zultys-smpp-mm4/smpp/coding"
	"zultys-smpp-mm4/smpp/pdu"
)

type SMPPServer struct {
	TLS              *tls.Config
	conns            map[string]*smpp.Session
	mu               sync.RWMutex
	reconnectChannel chan string
	gateway          *Gateway

	pendingAcks   map[int32]chan *pdu.DeliverSMResp
	pendingAcksMu sync.Mutex
}

func (srv *SMPPServer) Start(gateway *Gateway) {
	handler := NewSimpleHandler(gateway.SMPPServer)
	smppListen := os.Getenv("SMPP_LISTEN")
	if smppListen == "" {
		smppListen = "0.0.0.0:2775"
	}

	srv.gateway = gateway

	srv.pendingAcks = make(map[int32]chan *pdu.DeliverSMResp)

	/*srv.smsQueueCollection = gateway.MongoClient.Database(SMSQueueDBName).Collection(SMSQueueCollectionName)
	 */
	go func() {
		err := smpp.ServeTCP(smppListen, handler, nil)
		if err != nil {
			panic(err)
		}
	}()
	// Start processing the SMS queue
	/*go srv.processReconnectNotifications()*/

	select {}
}

func initSmppServer() (*SMPPServer, error) {
	return &SMPPServer{
		conns:            make(map[string]*smpp.Session),
		reconnectChannel: make(chan string),
	}, nil
}

type SimpleHandler struct {
	server *SMPPServer
}

func NewSimpleHandler(server *SMPPServer) *SimpleHandler {
	return &SimpleHandler{server: server}
}

func (srv *SMPPServer) removeSession(session *smpp.Session) {
	srv.mu.Lock()
	defer srv.mu.Unlock()
	for username, sess := range srv.conns {
		if sess == session {
			delete(srv.conns, username)
			break
		}
	}
}

func (h *SimpleHandler) Serve(session *smpp.Session) {
	ctx, cancel := context.WithCancel(context.Background())
	defer func() {
		cancel()
		_ = session.Close(ctx)
		// Remove session from conns map
		h.server.removeSession(session)
	}()

	go h.enquireLink(session, ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case packet, ok := <-session.PDU():
			if !ok {
				// The receiveQueue is closed; exit the loop
				return
			}
			h.handlePDU(session, packet)
		}
	}
}

func (h *SimpleHandler) enquireLink(session *smpp.Session, ctx context.Context) {
	tick := time.NewTicker(15 * time.Second)
	defer tick.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			err := session.EnquireLink(ctx, 15*time.Second, 5*time.Second)
			if err != nil {
				var lm = h.server.gateway.LogManager
				lm.SendLog(lm.BuildLog(
					"Server.SMPP.EnquireLink",
					"SMPPEnquireLinkError",
					logrus.ErrorLevel,
					nil, err,
				))

				return
			}
		}
	}
}

func (srv *SMPPServer) findAuthdSession(session *smpp.Session) error {
	for _, sess := range srv.conns {
		currentConn, err := srv.GetClientIP(session)
		loggedConn, err := srv.GetClientIP(sess)

		if err != nil {
			return fmt.Errorf("error getting client ip for auth check")
		}

		if currentConn == loggedConn {
			return nil
		}
	}
	return fmt.Errorf("unable to find matching session")
}

func (s *SMPPServer) addPendingAck(seq int32) chan *pdu.DeliverSMResp {
	ackCh := make(chan *pdu.DeliverSMResp, 1)
	s.pendingAcksMu.Lock()
	s.pendingAcks[seq] = ackCh
	s.pendingAcksMu.Unlock()
	return ackCh
}

func (s *SMPPServer) removePendingAck(seq int32) {
	s.pendingAcksMu.Lock()
	delete(s.pendingAcks, seq)
	s.pendingAcksMu.Unlock()
}

func (h *SimpleHandler) handlePDU(session *smpp.Session, packet any) {
	var lm = h.server.gateway.LogManager

	switch p := packet.(type) {
	case *pdu.BindTransceiver:
		h.handleBind(session, p)
	case *pdu.SubmitSM:
		err := h.server.findAuthdSession(session)
		if err != nil {
			lm.SendLog(lm.BuildLog(
				"Server.SMPP.HandlePDU",
				"AuthFailed",
				logrus.ErrorLevel,
				map[string]interface{}{
					"ip": session.Parent.RemoteAddr().String(),
				}, err,
			))
		}
		h.handleSubmitSM(session, p)
	case *pdu.DeliverSMResp:
		seq := p.Header.Sequence
		h.server.pendingAcksMu.Lock()
		ackCh, exists := h.server.pendingAcks[seq]
		h.server.pendingAcksMu.Unlock()
		if exists {
			// Send the response on the channel; use non-blocking send if needed.
			select {
			case ackCh <- packet.(*pdu.DeliverSMResp):
			default:
			}
			// Optionally remove the channel now:
			h.server.removePendingAck(seq)
		}
	case *pdu.DeliverSM:
		h.handleDeliverSM(session, p)
	case *pdu.Unbind:
		h.handleUnbind(session, p)
	case pdu.Responsable:
		err := session.Send(p.Resp())
		if err != nil {
			lm.SendLog(lm.BuildLog(
				"Server.SMPP.HandlePDU",
				"SMPPResponsableError",
				logrus.ErrorLevel,
				map[string]interface{}{
					"ip": session.Parent.RemoteAddr().String(),
				}, err,
			))
		}
	default:
		lm.SendLog(lm.BuildLog(
			"Server.SMPP.HandlePDU",
			"SMPPUnhandledPDU",
			logrus.WarnLevel,
			map[string]interface{}{
				"ip": session.Parent.RemoteAddr().String(),
			}, p,
		))
	}
}

func (h *SimpleHandler) handleBind(session *smpp.Session, bindReq *pdu.BindTransceiver) {
	var lm = h.server.gateway.LogManager

	username := bindReq.SystemID
	password := bindReq.Password

	ip, err := h.server.GetClientIP(session)

	if username == "" || password == "" {
		lm.SendLog(lm.BuildLog(
			"Server.SMPP.HandleBind",
			"AuthFailed",
			logrus.ErrorLevel,
			map[string]interface{}{
				"ip":       ip,
				"username": username,
			},
		))
		return
	}

	authed, err := h.server.gateway.authClient(username, password)
	if err != nil {
		lm.SendLog(lm.BuildLog(
			"Server.SMPP.HandleBind",
			"AuthFailed",
			logrus.ErrorLevel,
			map[string]interface{}{
				"ip":       session.Parent.RemoteAddr().String(),
				"username": username,
			},
		))
		return
	}

	if authed {
		resp := bindReq.Resp()
		err = session.Send(resp)
		if err != nil {
			lm.SendLog(lm.BuildLog(
				"Server.SMPP.HandleBind",
				"SMPPPDUError",
				logrus.ErrorLevel,
				map[string]interface{}{
					"ip":       session.Parent.RemoteAddr().String(),
					"username": username,
				}, "BIND REQ",
			))
		}

		lm.SendLog(lm.BuildLog(
			"Server.SMPP.HandleBind",
			"AuthSuccess",
			logrus.InfoLevel,
			map[string]interface{}{
				"ip":       session.Parent.RemoteAddr().String(),
				"username": username,
			},
		))

		h.server.mu.Lock()
		// Close old session if exists
		if oldSession, exists := h.server.conns[username]; exists {
			_ = oldSession.Close(context.Background())
		}
		h.server.conns[username] = session
		h.server.mu.Unlock()
	} else {
		lm.SendLog(lm.BuildLog(
			"Server.SMPP.HandleBind",
			"AuthFailed",
			logrus.ErrorLevel,
			map[string]interface{}{
				"ip":       session.Parent.RemoteAddr().String(),
				"username": username,
			},
		))
	}
}
func (h *SimpleHandler) handleSubmitSM(session *smpp.Session, submitSM *pdu.SubmitSM) {
	transId := primitive.NewObjectID().Hex()

	// Find the client associated with this session
	var client *Client
	h.server.mu.RLock()
	for username, conn := range h.server.conns {
		if conn == session {
			client = h.server.gateway.Clients[username]
			break
		}
	}
	h.server.mu.RUnlock()

	var lm = h.server.gateway.LogManager

	if client == nil {
		lm.SendLog(lm.BuildLog(
			"Server.SMPP.HandleSubmitSM",
			"SMPPFindSession",
			logrus.ErrorLevel,
			map[string]interface{}{
				"ip": session.Parent.RemoteAddr().String(),
			},
		))
		return
	}

	var decodedMsg = ""

	if submitSM.Message.DataCoding != 0 {
		encoding := coding.ASCIICoding

		if submitSM.Message.DataCoding == 8 { // UTF-16
			encoding = coding.UCS2Coding
		} else if submitSM.Message.DataCoding == 1 { // UTF-16
			encoding = coding.ASCIICoding
		}
		decodedMsg, _ = encoding.Encoding().NewDecoder().String(string(submitSM.Message.Message))
	} else {
		decodedMsg = string(submitSM.Message.Message) // fuk it lol yolo
	}
	//todo test if this is better? we may just need to parse the messages?

	smsMessage := cleanSMSMessage(decodedMsg)

	if decodedMsg == "" {
		lm.SendLog(lm.BuildLog(
			"Server.SMPP.HandleSubmitSM",
			"message contains no information",
			logrus.WarnLevel,
			map[string]interface{}{
				"client":      client.Username,
				"logID":       transId,
				"decoded_msg": decodedMsg,
				"submitsm":    submitSM,
				"clean_msg":   smsMessage,
			},
		))

		resp := submitSM.Resp()
		err := session.Send(resp)
		if err != nil {
			lm.SendLog(lm.BuildLog(
				"Server.SMPP.HandleSubmitSM",
				"SMPPPDUError",
				logrus.ErrorLevel,
				map[string]interface{}{
					"ip": session.Parent.RemoteAddr().String(),
				}, err,
			))
		}

		return
	}

	numData := h.server.gateway.getNumber(submitSM.SourceAddr.String())
	if numData.IgnoreStopCmdSending && decodedMsg == "Reply STOP to end messages." {
		lm.SendLog(lm.BuildLog(
			"Server.SMPP.HandleSubmitSM",
			"Dropping message because contains STOP and matches client number with rule.",
			logrus.WarnLevel,
			map[string]interface{}{
				"ip":     session.Parent.RemoteAddr().String(),
				"client": client.Username,
				"from":   numData.Number,
			},
		))

		return
	}

	msgQueueItem := MsgQueueItem{
		To:                submitSM.DestAddr.String(),
		From:              submitSM.SourceAddr.String(),
		ReceivedTimestamp: time.Now(),
		Type:              MsgQueueItemType.SMS,
		message:           decodedMsg,
		SkipNumberCheck:   false,
		LogID:             transId,
	}

	lm.SendLog(lm.BuildLog(
		"Server.SMPP.HandleSubmitSM",
		"Sending SMS to sending channel",
		logrus.WarnLevel,
		map[string]interface{}{
			"client":      client.Username,
			"logID":       transId,
			"decoded_msg": decodedMsg,
			"submitsm":    submitSM,
			"clean_msg":   smsMessage,
		},
	))

	// send to message queue?
	/*h.server.gateway.Router.ClientMsgChan <- msgQueueItem*/

	// Compute conversation hash.
	convoID := computeCorrelationKey(msgQueueItem.From, msgQueueItem.To)
	// Add the message to the conversation manager.
	h.server.gateway.ConvoManager.AddMessage(convoID, msgQueueItem, h.server.gateway.Router)

	resp := submitSM.Resp()
	err := session.Send(resp)
	if err != nil {
		lm.SendLog(lm.BuildLog(
			"Server.SMPP.HandleSubmitSM",
			"SMPPPDUError",
			logrus.ErrorLevel,
			map[string]interface{}{
				"ip": session.Parent.RemoteAddr().String(),
			}, err,
		))
	}
}

func (h *SimpleHandler) handleDeliverSM(session *smpp.Session, deliverSM *pdu.DeliverSM) {
	logf := LoggingFormat{Type: "handleDeliverSM"}

	resp := deliverSM.Resp()
	err := session.Send(resp)
	if err != nil {
		logf.Level = logrus.ErrorLevel
		logf.Message = fmt.Sprintf("error sending DeliverSM response")
		logf.Error = err
		logf.Print()
	}
}

func (h *SimpleHandler) handleUnbind(session *smpp.Session, unbind *pdu.Unbind) {
	var lm = h.server.gateway.LogManager

	lm.SendLog(lm.BuildLog(
		"Server.SMPP.HandleUnbind",
		"ReceivedUnbind",
		logrus.InfoLevel,
		map[string]interface{}{
			"ip": session.Parent.RemoteAddr().String(),
		},
	))

	resp := unbind.Resp()
	err := session.Send(resp)
	if err != nil {
		lm.SendLog(lm.BuildLog(
			"Server.SMPP.HandleUnbind",
			"SMPPPDUError",
			logrus.ErrorLevel,
			map[string]interface{}{
				"ip": session.Parent.RemoteAddr().String(),
			}, err,
		))
	}

	// Close the session and remove it from conns
	_ = session.Close(context.Background())
	h.server.removeSession(session)
}

// replacementMap defines replacements for non-standard characters.
// For example, smart quotes are replaced with standard ones.
var replacementMap = map[rune]string{
	'\u2018': "'",   // Left single quotation mark
	'\u2019': "'",   // Right single quotation mark
	'\u201A': "'",   // Single low-9 quotation mark
	'\u201C': "\"",  // Left double quotation mark
	'\u201D': "\"",  // Right double quotation mark
	'\u2013': "-",   // En dash replaced with hyphen
	'\u2014': "-",   // Em dash replaced with hyphen
	'\u2026': "...", // Ellipsis replaced with three dots
}

// isEmoji returns true if the rune falls within common emoji ranges.
func isEmoji(r rune) bool {
	// Emoticons (U+1F600 to U+1F64F)
	if r >= 0x1F600 && r <= 0x1F64F {
		return true
	}
	// Miscellaneous Symbols and Pictographs (U+1F300 to U+1F5FF)
	if r >= 0x1F300 && r <= 0x1F5FF {
		return true
	}
	// Transport & Map Symbols (U+1F680 to U+1F6FF)
	if r >= 0x1F680 && r <= 0x1F6FF {
		return true
	}
	// Miscellaneous Symbols (U+2600 to U+26FF)
	if r >= 0x2600 && r <= 0x26FF {
		return true
	}
	// Dingbats (U+2700 to U+27BF)
	if r >= 0x2700 && r <= 0x27BF {
		return true
	}
	// Supplemental Symbols and Pictographs (U+1F900 to U+1F9FF)
	if r >= 0x1F900 && r <= 0x1F9FF {
		return true
	}
	return false
}

// isGSMAllowed checks if a rune is allowed by our GSM whitelist.
// Here we allow letters, digits, whitespace and a set of standard punctuation.
func isGSMAllowed(r rune) bool {
	// Allow letters, digits, and whitespace.
	if unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsSpace(r) {
		return true
	}
	// Allowed punctuation. Adjust this list as needed.
	allowedPunct := ".,!?;:'\"-()[]{}"
	if strings.ContainsRune(allowedPunct, r) {
		return true
	}
	return false
}

// cleanSMSMessage processes the input message by:
//  1. Removing unwanted control or invalid characters.
//  2. Replacing non-standard characters with allowed equivalents when possible.
//  3. Allowing genuine characters and emojis.
func cleanSMSMessage(input string) string {
	var output strings.Builder

	for _, r := range input {
		// Remove known unwanted characters.
		if r == '\x00' || r == '\x1B' || (r < 32 && r != '\n' && r != '\r' && r != '\t') || r == 127 || (r >= 0xD800 && r <= 0xDFFF) {
			continue
		}

		// Always allow emojis.
		if isEmoji(r) {
			output.WriteRune(r)
			continue
		}

		// If the character is allowed by GSM, keep it.
		if isGSMAllowed(r) {
			output.WriteRune(r)
			continue
		}

		// Otherwise, if a similar allowed replacement exists, use it.
		if replacement, ok := replacementMap[r]; ok {
			output.WriteString(replacement)
			continue
		}

		// If no allowed replacement exists, the character is skipped.
	}

	return output.String()
}

// sendSMPP attempts to send an SMPPMessage via the SMPP server.
// On failure, it notifies via sendFailureChannel and enqueues the message.
// sendSMPP attempts to send an SMPPMessage via the SMPP server.
// On failure, it notifies via sendFailureChannel and enqueues the message.
func (s *SMPPServer) sendSMPP(msg MsgQueueItem, session *smpp.Session) error {
	// Find the SMPP session associated with the destination number
	session, err := s.findSmppSession(msg.To)
	if err != nil {
		return fmt.Errorf("error finding SMPP session: %v", err)
	}

	// Generate the next sequence number for the PDU
	nextSeq := session.NextSequence

	// cleanedContent := ValidateAndCleanSMS(msg.message)

	smsMessage := cleanSMSMessage(msg.message)

	encoding := coding.ASCIICoding

	limit, _ := strconv.Atoi(os.Getenv("SMS_CHAR_LIMIT"))
	bestCoding := coding.BestSafeCoding(smsMessage)
	if bestCoding == coding.UCS2Coding {
		encoding = coding.UCS2Coding
		limit, _ = strconv.Atoi(os.Getenv("SMS_CHAR_LIMIT_UTF16"))
	}

	segments := make([]string, 0)
	splitter := encoding.Splitter()

	if splitter != nil {
		segments = splitter.Split(smsMessage, limit)
	} else {
		segments = []string{smsMessage}
	}

	encoder := encoding.Encoding().NewEncoder()

	for _, segment := range segments {
		encoded, _ := encoder.Bytes([]byte(segment))
		seq := nextSeq() // generate new sequence number

		// Create the PDU, setting its sequence
		submitSM := &pdu.DeliverSM{
			SourceAddr: pdu.Address{TON: 0x01, NPI: 0x01, No: msg.From},
			DestAddr:   pdu.Address{TON: 0x01, NPI: 0x01, No: msg.To},
			Message:    pdu.ShortMessage{Message: encoded, DataCoding: encoding},
			RegisteredDelivery: pdu.RegisteredDelivery{
				MCDeliveryReceipt: 1,
			},
			Header: pdu.Header{
				Sequence: seq,
			},
		}

		// Register a pending ack channel
		ackCh := s.addPendingAck(seq)

		// Send the PDU
		err = session.Send(submitSM)
		if err != nil {
			s.removePendingAck(seq)
			return fmt.Errorf("error sending SubmitSM: %v", err)
		}

		// Wait for the ack with a timeout
		select {
		case respPDU := <-ackCh:
			// Optionally, verify the response status here
			if status := respPDU.Header.CommandStatus; status != 0 {
				return fmt.Errorf("non-OK response for sequence %d: %d", seq, status)
			}
			s.gateway.LogManager.SendLog(s.gateway.LogManager.BuildLog(
				"Server.SMPP.HandleSubmitSM",
				"found matching delivery ack resp",
				logrus.WarnLevel,
				map[string]interface{}{
					"ip":       session.Parent.RemoteAddr().String(),
					"sequence": seq,
				},
			))
		case <-time.After(5 * time.Second): // adjust timeout as needed
			s.removePendingAck(seq)
			return fmt.Errorf("timeout waiting for ack for sequence %d", seq)
		}
		// Acknowledgment received; continue to next segment.
	}
	return nil
}

func (srv *SMPPServer) findSmppSession(destination string) (*smpp.Session, error) {
	srv.mu.RLock()
	defer srv.mu.RUnlock()

	for _, client := range srv.gateway.Clients {
		for _, num := range client.Numbers {
			if strings.Contains(destination, num.Number) {
				if session, ok := srv.conns[client.Username]; ok {
					return session, nil
				} else {
					return nil, fmt.Errorf("client found but not connected: %s", client.Username)
				}
			}
		}
	}

	return nil, fmt.Errorf("no session found for destination: %s", destination)
}

func (srv *SMPPServer) GetClientIP(session *smpp.Session) (string, error) {
	if session == nil {
		return "", fmt.Errorf("session is nil")
	}

	// todo add proxy support?

	addr := session.Parent.RemoteAddr()
	if addr == nil {
		return "", fmt.Errorf("could not retrieve remote address from session")
	}

	// addr.String() returns "IP:Port"
	host, _, err := net.SplitHostPort(addr.String())
	if err != nil {
		return "", fmt.Errorf("error splitting host and port: %w", err)
	}

	return host, nil
}
