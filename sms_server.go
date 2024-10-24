package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"github.com/sirupsen/logrus"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"net"
	"os"
	"strings"
	"sync"
	"time"
	"zultys-smpp-mm4/smpp"
	"zultys-smpp-mm4/smpp/pdu"
)

type SMSMessage struct {
	Source      string
	Destination string
	Content     string
	Client      *Client
	Route       *Route
}

// SMPPMessage represents an SMS message for SMPP.
type SMPPMessage struct {
	From        string
	To          string
	Content     string
	CarrierData map[string]string
	logID       string
}

type SMPPServer struct {
	TLS                *tls.Config
	GatewayClients     map[string]*Client // Map of Username to Client
	conns              map[string]*smpp.Session
	mu                 sync.RWMutex
	l                  net.Listener
	smsOutboundChannel chan SMSMessage
	smsInboundChannel  chan SMPPMessage
	smsQueueCollection *mongo.Collection // MongoDB collection for SMS queue
	smsQueue           chan SMPPMessage
	reconnectChannel   chan string
	routing            *Routing
}

func (srv *SMPPServer) Start(gateway *Gateway) {
	logf := LoggingFormat{Type: LogType.SMPP + "_" + LogType.Startup}

	handler := NewSimpleHandler(gateway.SMPPServer)
	smppListen := os.Getenv("SMPP_LISTEN")
	if smppListen == "" {
		smppListen = "0.0.0.0:2775"
	}

	srv.smsQueueCollection = gateway.MongoClient.Database(SMSQueueDBName).Collection(SMSQueueCollectionName)

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

	go srv.handleInboundSMS()

	go srv.handleOutboundSMS()
	// Start processing the SMS queue
	go srv.processReconnectNotifications()

	select {}
}

func initSmppServer() (*SMPPServer, error) {
	logf := LoggingFormat{Type: LogType.SMPP}

	clients, err := loadClients()
	if err != nil {
		logf.Level = logrus.ErrorLevel
		logf.Message = fmt.Sprintf("failed to load clients")
		logf.Error = err
		return nil, logf.ToError()
	}

	clientMap := make(map[string]*Client)
	for i := range clients {
		clientMap[clients[i].Username] = &clients[i]
	}

	return &SMPPServer{
		GatewayClients:     clientMap,
		conns:              make(map[string]*smpp.Session),
		smsOutboundChannel: make(chan SMSMessage),
		smsInboundChannel:  make(chan SMPPMessage),
		smsQueue:           make(chan SMPPMessage),
		reconnectChannel:   make(chan string),
	}, nil
}

type SimpleHandler struct {
	server *SMPPServer
}

func NewSimpleHandler(server *SMPPServer) *SimpleHandler {
	return &SimpleHandler{server: server}
}

func (h *SimpleHandler) Serve(session *smpp.Session) {
	defer session.Close(context.Background())

	//log.Printf("New connection from %s", session.RemoteAddr())

	// Start EnquireLink process
	go h.enquireLink(session)

	for {
		select {
		/*case <-session.Context().Done():
		//log.Printf("Connection closed from %s", session.RemoteAddr())
		return*/
		case packet := <-session.PDU():
			h.handlePDU(session, packet)
		}
	}
}

func (h *SimpleHandler) enquireLink(session *smpp.Session) {
	logf := LoggingFormat{Type: LogType.SMPP}

	ctx := context.Background()
	tick := 30 * time.Second
	timeout := 5 * time.Second

	err := session.EnquireLink(ctx, tick, timeout)
	if err != nil {
		logf.Error = err
		logf.Level = logrus.ErrorLevel
		logf.Message = "EnquireLink process ended"
		logf.Print()
	}
}

func (h *SimpleHandler) handlePDU(session *smpp.Session, packet any) {
	logf := LoggingFormat{Type: LogType.SMPP}

	switch p := packet.(type) {
	case *pdu.BindTransceiver:
		h.handleBind(session, p)
	case *pdu.SubmitSM:
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

	/*logf.Level = logrus.WarnLevel
	logf.Message = fmt.Sprintf("received bind request: %T", bindReq)
	logf.Print()*/

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

	authed, err := authClient(username, password, h.server.GatewayClients)
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
		h.server.reconnectChannel <- username

		logf.Level = logrus.InfoLevel
		logf.Message = fmt.Sprintf(LogMessages.Authentication, logf.AdditionalData["type"], "success", username, ip)
		logf.Print()

		h.server.mu.Lock()
		h.server.conns[username] = session
		h.server.mu.Unlock()
	} else {
		logf.Level = logrus.ErrorLevel
		logf.Message = fmt.Sprintf(LogMessages.Authentication, logf.AdditionalData["type"], "failed to authenticate", username, ip)
		logf.Print()
	}
}

func (h *SimpleHandler) handleSubmitSM(session *smpp.Session, submitSM *pdu.SubmitSM) {
	logf := LoggingFormat{Type: LogType.SMPP + "_" + LogType.Routing}
	/*	logf.Level = logrus.InfoLevel
		logf.Message = fmt.Sprintf("Received SubmitSM: From=%s, To=%s", submitSM.SourceAddr, submitSM.DestAddr)
		logf.Print()*/

	// Find the client associated with this session
	var client *Client
	h.server.mu.RLock()
	for username, conn := range h.server.conns {
		if conn == session {
			client = h.server.GatewayClients[username]
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

	logf.AddField("from", submitSM.SourceAddr.String())
	logf.AddField("to", submitSM.DestAddr.String())
	logf.AddField("systemID", client.Username)

	route := h.server.findRoute(submitSM.SourceAddr.String(), submitSM.DestAddr.String())
	if route == nil {
		logf.Level = logrus.WarnLevel
		logf.Message = fmt.Sprintf("no route found for source %s and destination %s", submitSM.SourceAddr, submitSM.DestAddr)
		logf.Print()
		return
	}

	// todo route to other smpp endpoints if available
	logf.Level = logrus.InfoLevel
	logf.Message = fmt.Sprintf("routing message via carrier: %s", route.Endpoint)
	logf.Print()

	h.server.smsOutboundChannel <- SMSMessage{
		Source:      submitSM.SourceAddr.String(),
		Destination: submitSM.DestAddr.String(),
		Content:     string(submitSM.Message.Message),
		Client:      client,
		Route:       route,
	}

	resp := submitSM.Resp()
	err := session.Send(resp)
	if err != nil {
		logf.Level = logrus.ErrorLevel
		logf.Message = fmt.Sprintf("error sending SubmitSM response")
		logf.Error = err
		logf.Print()
	}
}

func (srv *SMPPServer) findRoute(source, destination string) *Route {
	carrier, err := srv.clientOutboundCarrier(source)
	if err != nil {
		return nil
	}

	if carrier != "" {
		for _, route := range srv.routing.Routes {
			if route.Type == "carrier" && route.Endpoint == carrier {
				return route
			}
		}
	}

	// Fallback to prefix-based routing if no carrier route found
	/*for _, route := range srv.routing.Routes {
		if strings.HasPrefix(destination, route.Prefix) {
			return route
		}
	}*/

	return nil
}

func (srv *SMPPServer) clientOutboundCarrier(source string) (string, error) {
	for _, client := range srv.GatewayClients {
		for _, num := range client.Numbers {
			if strings.Contains(source, num.Number) {
				return num.Carrier, nil
			}
		}
	}

	return "", nil
}

func (srv *SMPPServer) clientInboundConn(destination string) (*smpp.Session, error) {

	for _, client := range srv.GatewayClients {
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
	/*
		logf.Level = logrus.InfoLevel
		logf.Message = fmt.Sprintf("Received DeliverSM: From=%s, To=%s", deliverSM.SourceAddr, deliverSM.DestAddr)
		logf.Print()*/

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

// handleInboundSMS handle inbound sms from an SMPP client
func (srv *SMPPServer) handleInboundSMS() {
	for msg := range srv.smsOutboundChannel {
		go func(m SMSMessage) {
			transId := primitive.NewObjectID().Hex()
			logf := LoggingFormat{Type: LogType.SMPP + "_" + LogType.Inbound + "_" + LogType.Endpoint}
			logf.AddField("logID", transId)
			logf.AddField("to", m.Destination)
			logf.AddField("from", m.Source)
			logf.AddField("systemID", m.Client.Username)

			if m.Route == nil {
				logf.Level = logrus.WarnLevel
				logf.Message = fmt.Sprintf("no route found for message: from=%s, to=%s", m.Source, m.Destination)
				logf.Print()
				return
			}

			switch m.Route.Type {
			case "carrier":
				logf.Level = logrus.InfoLevel
				logf.Message = fmt.Sprintf("sending SMS via carrier: %s", m.Route.Endpoint)
				logf.Print()
				// Implement carrier-specific logic here

				switch m.Route.Endpoint {
				case "twilio":
					sms := SMPPMessage{
						From:        m.Source,
						To:          m.Destination,
						Content:     m.Content,
						CarrierData: nil,
						logID:       transId,
					}

					err := m.Route.Handler.SendSMS(&sms)
					if err != nil {
						logf.Level = logrus.ErrorLevel
						logf.Message = fmt.Sprintf("failed to send SMS")
						logf.Error = err
						logf.Print()
						return
					}
				default:
					logf.Level = logrus.WarnLevel
					logf.Message = fmt.Sprintf("error sending to carrier")
					logf.Print()
				}
			case "sms_session":
				logf.Level = logrus.WarnLevel
				logf.Message = fmt.Sprintf("NOT IMPLEMENTED - Sending SMS via SMPP: %s", m.Route.Endpoint)
				logf.Print()
				// Implement SMPP client logic here
			default:
				logf.Level = logrus.WarnLevel
				logf.Message = fmt.Sprintf("unknown route type: %s", m.Route.Type)
				logf.Print()
			}
		}(msg)
	}
}

// sendSMPP attempts to send an SMPPMessage via the SMPP server.
// On failure, it notifies via sendFailureChannel and enqueues the message.
func (srv *SMPPServer) sendSMPP(msg SMPPMessage) {
	logf := LoggingFormat{Type: LogType.SMPP + "_" + LogType.Outbound + "_" + LogType.Endpoint}
	logf.AddField("logID", msg.logID)

	// Find the SMPP session associated with the destination number
	session, err := srv.findSmppSession(msg.To)
	if err != nil {
		logf.Level = logrus.ErrorLevel
		logf.Message = fmt.Sprintf("Error finding SMPP session: %v", err)
		logf.Print()

		// Enqueue the message for later delivery
		enqueueErr := EnqueueSMS(context.Background(), srv.smsQueueCollection, msg)
		if enqueueErr != nil {
			logf.Level = logrus.ErrorLevel
			logf.Message = fmt.Sprintf("Failed to enqueue SMS (logID: %s): %v", msg.logID, enqueueErr)
			logf.Print()
		}

		return
	}

	// Find the client (source systemID) from the destination number
	source, err := srv.findClientFromSource(msg.To)
	if err != nil {
		logf.Level = logrus.ErrorLevel
		logf.Message = "Error finding source systemID"
		logf.Error = err
		logf.Print()

		// Notify the send failure channel with the client's username
		// srv.reconnectChannel <- "unknown_client"

		// Enqueue the message for later delivery
		enqueueErr := EnqueueSMS(context.Background(), srv.smsQueueCollection, msg)
		if enqueueErr != nil {
			logf.Level = logrus.ErrorLevel
			logf.Message = fmt.Sprintf("Failed to enqueue SMS (logID: %s): %v", msg.logID, enqueueErr)
			logf.Print()
		}

		return
	}

	logf.AddField("systemID", source.Username)

	// Generate the next sequence number for the PDU
	nextSeq := session.NextSequence

	cleanedContent := ValidateAndCleanSMS(msg.Content)

	// Create the DeliverSM PDU with your specified values
	submitSM := &pdu.DeliverSM{
		SourceAddr: pdu.Address{TON: 0x01, NPI: 0x01, No: msg.From},
		DestAddr:   pdu.Address{TON: 0x01, NPI: 0x01, No: msg.To},
		Message:    pdu.ShortMessage{Message: []byte(cleanedContent)},
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
		logf.Message = fmt.Sprintf("Error sending SubmitSM: %v", err)
		logf.Print()

		// Notify the send failure channel with the client's username
		// srv.reconnectChannel <- source.Username

		// Enqueue the message for later delivery
		enqueueErr := EnqueueSMS(context.Background(), srv.smsQueueCollection, msg)
		if enqueueErr != nil {
			logf.Level = logrus.ErrorLevel
			logf.Message = fmt.Sprintf("Failed to enqueue SMS (logID: %s): %v", msg.logID, enqueueErr)
			logf.Print()
		}

	} else {
		logf.Level = logrus.InfoLevel
		logf.Message = fmt.Sprintf(LogMessages.Transaction, "outbound", source.Username, msg.From, msg.To)
		logf.Print()
	}
}

// handleOutboundSMS to SMPP client (inbound from carrier)
func (srv *SMPPServer) handleOutboundSMS() {
	for msg := range srv.smsInboundChannel {
		go srv.sendSMPP(msg)
	}
	/*	select {
		case msg := <-srv.smsInboundChannel:

		}*/
	/*for m := range srv.smsInboundChannel {
		go func(msg *SMPPMessage) {
			logf := LoggingFormat{Type: LogType.SMPP + "_" + LogType.Outbound + "_" + LogType.Endpoint}
			logf.AddField("logID", msg.logID)

			session, err := srv.findSmppSession(msg.To)
			if err != nil {
				logf.Level = logrus.ErrorLevel
				logf.Message = fmt.Sprintf("error finding SMPP session: %v", err)
				logf.Print()
				return
			}

			source, err := srv.findClientFromSource(msg.To)
			if err != nil {
				logf.Level = logrus.ErrorLevel
				logf.Message = fmt.Sprintf("error finding source systemID")
				logf.Error = err
			}

			logf.AddField("systemID", source.Username)

			nextSeq := session.NextSequence

			submitSM := &pdu.DeliverSM{
				SourceAddr:         pdu.Address{TON: 0x01, NPI: 0x01, No: msg.From},
				DestAddr:           pdu.Address{TON: 0x01, NPI: 0x01, No: msg.To},
				Message:            pdu.ShortMessage{Message: []byte(msg.Content)},
				RegisteredDelivery: pdu.RegisteredDelivery{MCDeliveryReceipt: 1},
				Header:             pdu.Header{Sequence: nextSeq()},
			}

			//cancel := context.WithTimeout(context.Background(), 5*time.Second)
			//defer cancel()

			err = session.Send(submitSM)
			if err != nil {
				logf.Level = logrus.ErrorLevel
				logf.Message = fmt.Sprintf(LogMessages.Transaction, "outbound", source.Username, msg.From, msg.To)
				logf.Print()
			} else {
				logf.Level = logrus.InfoLevel
				logf.Message = fmt.Sprintf(LogMessages.Transaction, "outbound", source.Username, msg.From, msg.To)
				// logf.Message = fmt.Sprintf("SMS sent successfully via SMPP - From: %s To: %s", msg.From, msg.To)
				logf.Print()
				//log.Printf("%s", resp)
			}
		}(&m)
	}*/
}

func (srv *SMPPServer) findClientFromSource(source string) (*Client, error) {
	srv.mu.RLock()
	defer srv.mu.RUnlock()

	for _, client := range srv.GatewayClients {
		for _, num := range client.Numbers {
			if strings.Contains(source, num.Number) {
				return client, nil
			}
		}
	}

	return nil, fmt.Errorf("no session found for destination: %s", source)
}

func (srv *SMPPServer) findSmppSession(destination string) (*smpp.Session, error) {
	srv.mu.RLock()
	defer srv.mu.RUnlock()

	for _, client := range srv.GatewayClients {
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
