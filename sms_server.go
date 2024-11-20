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
	logf := LoggingFormat{Type: LogType.SMPP + "_" + LogType.Startup}

	handler := NewSimpleHandler(gateway.SMPPServer)
	smppListen := os.Getenv("SMPP_LISTEN")
	if smppListen == "" {
		smppListen = "0.0.0.0:2775"
	}

	srv.gateway = gateway

	/*srv.smsQueueCollection = gateway.MongoClient.Database(SMSQueueDBName).Collection(SMSQueueCollectionName)
	 */
	go func() {
		logf.Level = logrus.InfoLevel
		logf.Message = fmt.Sprintf("starting SMPP server on %s", smppListen)
		logf.Print()

		err := smpp.ServeTCP(smppListen, handler, nil)
		if err != nil {
			logf.Level = logrus.ErrorLevel
			logf.Message = fmt.Sprintf("failed to start SMPP server on %s", smppListen)
			logf.Error = err
			logf.Print()
			os.Exit(1)
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

func (h *SimpleHandler) Serve(session *smpp.Session) {
	ctx, cancel := context.WithCancel(context.Background())
	defer func() {
		cancel()
		_ = session.Close(ctx)
	}()

	go h.enquireLink(session, ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case packet := <-session.PDU():
			h.handlePDU(session, packet)
		}
	}
}

func (h *SimpleHandler) enquireLink(session *smpp.Session, ctx context.Context) {
	logf := LoggingFormat{Type: LogType.SMPP}

	tick := time.NewTicker(15 * time.Second)
	defer tick.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			err := session.EnquireLink(ctx, 15*time.Second, 5*time.Second)
			if err != nil {
				logf.Error = err
				logf.Level = logrus.ErrorLevel
				logf.Message = "EnquireLink process ended"
				logf.Print()
				return
			}
		}
	}
}

func (srv *SMPPServer) findAuthdSession(session *smpp.Session) error {
	for _, sess := range srv.conns {
		if session.Parent.RemoteAddr() == sess.Parent.RemoteAddr() {
			return nil
		}
	}
	return fmt.Errorf("unable to find matching session")
}

func (h *SimpleHandler) handlePDU(session *smpp.Session, packet any) {
	logf := LoggingFormat{Type: LogType.SMPP}

	switch p := packet.(type) {
	case *pdu.BindTransceiver:
		h.handleBind(session, p)
	case *pdu.SubmitSM:
		err := h.server.findAuthdSession(session)
		if err != nil {
			logf.Error = err
			logf.Level = logrus.ErrorLevel
			logf.Message = "received submitSM from invalid unauthenticated user"
			logf.AddField("ip", session.Parent.RemoteAddr())
			logf.Print()
		}
		h.handleSubmitSM(session, p)
	case *pdu.DeliverSM:
		h.handleDeliverSM(session, p)
	case *pdu.Unbind:
		h.handleUnbind(session, p)
	case pdu.Responsable:
		err := session.Send(p.Resp())
		if err != nil {
			logf.Error = err
			logf.Level = logrus.ErrorLevel
			logf.Message = "error sending response to PDU"
			logf.Print()
		}
	default:
		logf.Level = logrus.WarnLevel
		logf.Message = fmt.Sprintf("received unhandled PDU: %T", p)
		logf.Print()
	}
}

func (h *SimpleHandler) handleBind(session *smpp.Session, bindReq *pdu.BindTransceiver) {
	logf := LoggingFormat{Type: LogType.SMPP + "_" + LogType.Authentication}
	logf.AddField("type", LogType.SMPP)

	// server (mm4/smpp), success/failed (err), userid, ip
	// Authentication

	username := bindReq.SystemID
	password := bindReq.Password

	ip, err := h.server.GetClientIP(session)

	logf.AddField("systemID", username)
	logf.AddField("ip", ip)

	if username == "" || password == "" {
		logf.Level = logrus.WarnLevel
		logf.Message = fmt.Sprintf(LogMessages.Authentication, logf.AdditionalData["type"], "invalid username or password", username, ip)
		logf.Print()
		return
	}

	authed, err := h.server.gateway.authClient(username, password)
	if err != nil {
		logf.Level = logrus.WarnLevel
		logf.Message = fmt.Sprintf(LogMessages.Authentication, logf.AdditionalData["type"], "unable to authenticate", username, ip)
		logf.Print()
		return
	}

	if authed {
		resp := bindReq.Resp()
		err = session.Send(resp)
		if err != nil {
			logf.Level = logrus.ErrorLevel
			logf.Error = err
			logf.Message = fmt.Sprintf(LogMessages.Authentication, logf.AdditionalData["type"], "error sending bind req", username, ip)
			logf.Print()
			logf.Error = nil
		}

		logf.Level = logrus.InfoLevel
		logf.Message = fmt.Sprintf(LogMessages.Authentication, logf.AdditionalData["type"], "success", username, ip)
		logf.Print()

		h.server.mu.Lock()
		h.server.conns[username] = session
		h.server.mu.Unlock()

		//h.server.reconnectChannel <- username
	} else {
		logf.Level = logrus.ErrorLevel
		logf.Message = fmt.Sprintf(LogMessages.Authentication, logf.AdditionalData["type"], "failed to authenticate", username, ip)
		logf.Print()
	}
}

func (h *SimpleHandler) handleSubmitSM(session *smpp.Session, submitSM *pdu.SubmitSM) {
	transId := primitive.NewObjectID().Hex()
	logf := LoggingFormat{Type: LogType.SMPP + "_" + LogType.Inbound + "_" + LogType.Endpoint}
	logf.AddField("logID", transId)

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

	if client == nil {
		logf.Level = logrus.ErrorLevel
		logf.Message = fmt.Sprintf("unable to identify client for connection")
		logf.Print()
		return
	}

	/*route := h.server.findRoute(submitSM.SourceAddr.String(), submitSM.DestAddr.String())
	if route == nil {
		logf.Level = logrus.WarnLevel
		logf.Message = fmt.Sprintf("no route found for source %s and destination %s", submitSM.SourceAddr, submitSM.DestAddr)
		logf.Print()
		return
	}*/

	bestCoding := coding.BestSafeCoding(string(submitSM.Message.Message))

	// todo fix this make better??
	if bestCoding == coding.GSM7BitCoding {
		bestCoding = coding.ASCIICoding
	}

	encodedMsg, _ := bestCoding.Encoding().NewDecoder().String(string(submitSM.Message.Message))

	msgQueueItem := MsgQueueItem{
		To:                submitSM.DestAddr.String(),
		From:              submitSM.SourceAddr.String(),
		ReceivedTimestamp: time.Now(),
		Type:              MsgQueueItemType.SMS,
		Message:           encodedMsg,
		SkipNumberCheck:   false,
		LogID:             transId,
	}

	logf.AddField("to", msgQueueItem.To)
	logf.AddField("from", msgQueueItem.From)
	logf.AddField("systemID", client.Username)

	// send to message queue?
	h.server.gateway.Router.ClientMsgChan <- msgQueueItem
	logf.Level = logrus.InfoLevel
	logf.Message = fmt.Sprintf("sending to message queue")
	logf.Print()

	resp := submitSM.Resp()
	err := session.Send(resp)
	if err != nil {
		logf.Level = logrus.ErrorLevel
		logf.Message = fmt.Sprintf("error sending SubmitSM response")
		logf.Error = err
		logf.Print()
	}
}

func (srv *SMPPServer) clientInboundConn(destination string) (*smpp.Session, error) {

	for _, client := range srv.gateway.Clients {
		for _, num := range client.Numbers {
			//log.Printf("%s", num)
			if strings.Contains(destination, num.Number) {
				return srv.conns[client.Username], nil
			}
		}
	}

	return nil, nil
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
	logf := LoggingFormat{Type: "handleUnbind"}
	logf.Level = logrus.InfoLevel
	logf.Message = fmt.Sprintf("received unbind request")
	logf.Print()

	resp := unbind.Resp()
	err := session.Send(resp)
	if err != nil {
		logf.Level = logrus.ErrorLevel
		logf.Message = fmt.Sprintf("error sending Unbind response: %v", err)
		logf.Error = err
		logf.Print()
	}

	// Remove the session from the server's connections
	h.server.mu.Lock()
	for username, conn := range h.server.conns {
		ip, err := h.server.GetClientIP(conn)
		if err != nil {
			println(err)
			return
		}
		clientIP, err := h.server.GetClientIP(session)
		if err != nil {
			println(err)
			return
		}

		if clientIP == ip {
			delete(h.server.conns, username)
			break
		}
	}
	h.server.mu.Unlock()
}

// sendSMPP attempts to send an SMPPMessage via the SMPP server.
// On failure, it notifies via sendFailureChannel and enqueues the message.
func (srv *SMPPServer) sendSMPP(msg MsgQueueItem) error {
	logf := LoggingFormat{Type: LogType.SMPP + "_" + LogType.Outbound + "_" + LogType.Endpoint}
	logf.AddField("logID", msg.LogID)

	// Find the SMPP session associated with the destination number
	session, err := srv.findSmppSession(msg.To)
	if err != nil {
		logf.Level = logrus.ErrorLevel
		logf.Error = fmt.Errorf("error finding SMPP session: %v", err)
		logf.Print()

		// todo postgresql queue system?

		return logf.Error
	}

	// Generate the next sequence number for the PDU
	nextSeq := session.NextSequence

	// cleanedContent := ValidateAndCleanSMS(msg.Message)

	// todo split messages here?

	limit := 134
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
			logf.Level = logrus.ErrorLevel
			logf.Error = fmt.Errorf("error sending SubmitSM: %v", err)
			logf.Print()

			return logf.Error
		} else {
			logf.Level = logrus.InfoLevel
			logf.Message = fmt.Sprintf(LogMessages.Transaction, "outbound", "", msg.From, msg.To)
			logf.Print()
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
				}
				return nil, fmt.Errorf("client found but not connected: %s", client.Username)
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
