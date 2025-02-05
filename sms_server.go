package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"github.com/sirupsen/logrus"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"net"
	"os"
	"strings"
	"sync"
	"time"
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
}

func (srv *SMPPServer) Start(gateway *Gateway) {
	handler := NewSimpleHandler(gateway.SMPPServer)
	smppListen := os.Getenv("SMPP_LISTEN")
	if smppListen == "" {
		smppListen = "0.0.0.0:2775"
	}

	srv.gateway = gateway

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

	/*route := h.server.findRoute(submitSM.SourceAddr.String(), submitSM.DestAddr.String())
	if route == nil {
		logf.Level = logrus.WarnLevel
		logf.Message = fmt.Sprintf("no route found for source %s and destination %s", submitSM.SourceAddr, submitSM.DestAddr)
		logf.Print()
		return
	}*/

	encoding := coding.GSM7BitCoding

	// todo fix this make better??
	/*if bestCoding == coding.GSM7BitCoding {
		bestCoding = coding.ASCIICoding
	}*/

	if submitSM.Message.DataCoding == 8 { // UTF-16
		encoding = coding.UCS2Coding
	} else if submitSM.Message.DataCoding == 1 { // UTF-16
		encoding = coding.ASCIICoding
	} else if submitSM.Message.DataCoding != 0 {
		encoding = coding.NoCoding
	}

	decodedMsg, err := encoding.Encoding().NewDecoder().String(string(submitSM.Message.Message))

	if err != nil {
		lm.SendLog(lm.BuildLog(
			"Server.SMPP.HandleSubmitSM",
			"Message contains no information",
			logrus.ErrorLevel,
			map[string]interface{}{
				"client":     client.Username,
				"logID":      transId,
				"decodedMsg": decodedMsg,
				"submitsm":   submitSM,
				"encoding":   encoding,
				"error":      err,
			},
		))

	}

	//todo test if this is better? we may just need to parse the messages?

	if decodedMsg == "" {
		lm.SendLog(lm.BuildLog(
			"Server.SMPP.HandleSubmitSM",
			"Message contains no information",
			logrus.WarnLevel,
			map[string]interface{}{
				"client":     client.Username,
				"logID":      transId,
				"decodedMsg": decodedMsg,
				"submitsm":   submitSM,
				"encoding":   encoding,
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

	msgQueueItem := MsgQueueItem{
		To:                submitSM.DestAddr.String(),
		From:              submitSM.SourceAddr.String(),
		ReceivedTimestamp: time.Now(),
		Type:              MsgQueueItemType.SMS,
		Message:           decodedMsg,
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
			"encoded_msg": decodedMsg,
			"submitsm":    submitSM,
			"encoding":    encoding,
		},
	))

	/*logf.AddField("to", msgQueueItem.To)
	logf.AddField("from", msgQueueItem.From)
	logf.AddField("systemID", client.Username)*/

	// send to message queue?
	h.server.gateway.Router.ClientMsgChan <- msgQueueItem
	/*	logf.Level = logrus.InfoLevel
		logf.Message = fmt.Sprintf("sending to message queue")
		logf.Print()*/

	resp := submitSM.Resp()
	err = session.Send(resp)
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

	// cleanedContent := ValidateAndCleanSMS(msg.Message)

	// todo split messages here?

	limit := 160
	bestCoding := coding.BestSafeCoding(msg.Message)

	segments := make([]string, 0)

	if bestCoding == coding.GSM7BitCoding {
		bestCoding = coding.ASCIICoding
	}
	splitter := bestCoding.Splitter()

	if splitter != nil {
		segments = splitter.Split(msg.Message, limit)
	} else {
		segments = []string{msg.Message}
	}

	encoder := bestCoding.Encoding().NewEncoder()

	for _, segment := range segments {
		encoded, _ := encoder.Bytes([]byte(segment))

		// Create the DeliverSM PDU with your specified values
		submitSM := &pdu.DeliverSM{
			SourceAddr: pdu.Address{TON: 0x01, NPI: 0x01, No: msg.From},
			DestAddr:   pdu.Address{TON: 0x01, NPI: 0x01, No: msg.To},
			Message:    pdu.ShortMessage{Message: encoded, DataCoding: bestCoding}, // todo fix encoding
			RegisteredDelivery: pdu.RegisteredDelivery{
				MCDeliveryReceipt: 1,
			},
			Header: pdu.Header{
				Sequence: nextSeq(),
			},
		}

		// Attempt to send the PDU
		err = session.Send(submitSM)
		if err != nil {
			return fmt.Errorf("error sending SubmitSM: %v", err)
		}
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
