package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"github.com/M2MGateway/go-smpp"
	"github.com/M2MGateway/go-smpp/pdu"
	"github.com/sirupsen/logrus"
	"net"
	"os"
	"strings"
	"sync"
	"time"
)

type SMSMessage struct {
	Source      string
	Destination string
	Content     string
	Client      *Client
	Route       *Route
}

type SMPPServer struct {
	TLS                *tls.Config
	GatewayClients     map[string]*Client // Map of Username to Client
	conns              map[string]*smpp.Session
	mu                 sync.RWMutex
	l                  net.Listener
	smsOutboundChannel chan SMSMessage
	smsInboundChannel  chan CarrierMessage
	routing            *Routing
}

func (srv *SMPPServer) Start(gateway *Gateway) {
	logf := LoggingFormat{Path: "sms_smpp", Function: "Start"}

	handler := NewSimpleHandler(gateway.SMPPServer)
	smppListen := os.Getenv("SMPP_LISTEN")
	if smppListen == "" {
		smppListen = "0.0.0.0:2775"
	}

	go func() {
		logf.Level = logrus.InfoLevel
		logf.Message = fmt.Sprintf("Starting SMPP server on %s", smppListen)
		logf.Print()

		err := smpp.ServeTCP(smppListen, handler, nil)
		if err != nil {
			logf.Level = logrus.ErrorLevel
			logf.Message = fmt.Sprintf("Failed to start SMPP server on %s", smppListen)
			logf.Error = err
			logf.Print()
			os.Exit(1)
		}
	}()

	go func() {
		srv.handleInboundSMS()
	}()

	go func() {
		srv.handleOutboundSMS()
	}()
	select {}
}

func initSmppServer() (*SMPPServer, error) {
	logf := LoggingFormat{Path: "sms_smpp", Function: "initSmppServer"}

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
		smsInboundChannel:  make(chan CarrierMessage),
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
	logf := LoggingFormat{Path: "sms_smpp", Function: "enquireLink"}

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
	logf := LoggingFormat{Path: "sms_smpp", Function: "handlePDU"}

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
			logf.Message = "Error sending response to PDU"
			logf.Print()
		}
	default:
		logf.Level = logrus.WarnLevel
		logf.Message = fmt.Sprintf("Received unhandled PDU: %T", p)
		logf.Print()
	}
}

func (h *SimpleHandler) handleBind(session *smpp.Session, bindReq *pdu.BindTransceiver) {
	logf := LoggingFormat{Path: "sms_smpp", Function: "handleBind"}

	logf.Level = logrus.WarnLevel
	logf.Message = fmt.Sprintf("Received bind request: %T", bindReq)
	logf.Print()

	username := bindReq.SystemID
	password := bindReq.Password

	if username == "" || password == "" {
		logf.Level = logrus.WarnLevel
		logf.Message = fmt.Sprintf("Empty system ID or password: %s", username)
		logf.Print()
		return
	}

	authed, err := authClient(username, password, h.server.GatewayClients)
	if err != nil {
		logf.Level = logrus.WarnLevel
		logf.Message = fmt.Sprintf("Authentication error: %v", err)
		logf.Print()
		return
	}

	if authed {
		resp := bindReq.Resp()
		err = session.Send(resp)
		if err != nil {
			logf.Level = logrus.ErrorLevel
			logf.Error = err
			logf.Message = fmt.Sprintf("Error sending bind response: %v", err)
			logf.Print()
			logf.Error = nil
		}

		h.server.mu.Lock()
		h.server.conns[username] = session
		h.server.mu.Unlock()

		logf.Level = logrus.InfoLevel
		logf.Message = fmt.Sprintf("Client %s authenticated successfully", username)
		logf.Print()
	} else {
		logf.Level = logrus.ErrorLevel
		logf.Message = fmt.Sprintf("Authentication failed for client: %s", username)
		logf.Print()
	}
}

func (h *SimpleHandler) handleSubmitSM(session *smpp.Session, submitSM *pdu.SubmitSM) {
	logf := LoggingFormat{Path: "sms_smpp", Function: "handleSubmitSM"}
	logf.Level = logrus.InfoLevel
	logf.Message = fmt.Sprintf("Received SubmitSM: From=%s, To=%s", submitSM.SourceAddr, submitSM.DestAddr)
	logf.Print()

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
		logf.Message = fmt.Sprintf("Error: Unable to identify client for connection")
		logf.Print()
		return
	}

	route := h.server.findRoute(submitSM.SourceAddr.String(), submitSM.DestAddr.String())
	if route == nil {
		logf.Level = logrus.WarnLevel
		logf.Message = fmt.Sprintf("No route found for source %s and destination %s", submitSM.SourceAddr, submitSM.DestAddr)
		logf.Print()
		return
	}

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
		logf.Message = fmt.Sprintf("Error sending SubmitSM response")
		logf.Error = err
		logf.Print()
	}
}

func (srv *SMPPServer) findRoute(source, destination string) *Route {
	logf := LoggingFormat{Path: "sms_smpp", Function: "findRoute"}
	carrier, err := srv.clientOutboundCarrier(source)
	if err != nil {
		logf.Level = logrus.ErrorLevel
		logf.Message = fmt.Sprintf("Error finding carrier")
		logf.Error = err
		logf.Print()
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
	logf := LoggingFormat{Path: "sms_smpp", Function: "handleDeliverSM"}

	logf.Level = logrus.InfoLevel
	logf.Message = fmt.Sprintf("Received DeliverSM: From=%s, To=%s", deliverSM.SourceAddr, deliverSM.DestAddr)
	logf.Print()

	resp := deliverSM.Resp()
	err := session.Send(resp)
	if err != nil {
		logf.Level = logrus.ErrorLevel
		logf.Message = fmt.Sprintf("Error sending DeliverSM response")
		logf.Error = err
		logf.Print()
	}
}

func (h *SimpleHandler) handleUnbind(session *smpp.Session, unbind *pdu.Unbind) {
	logf := LoggingFormat{Path: "sms_smpp", Function: "handleUnbind"}
	logf.Level = logrus.InfoLevel
	logf.Message = fmt.Sprintf("Received unbind request")
	logf.Print()

	resp := unbind.Resp()
	err := session.Send(resp)
	if err != nil {
		logf.Level = logrus.ErrorLevel
		logf.Message = fmt.Sprintf("Error sending Unbind response: %v", err)
		logf.Error = err
		logf.Print()
	}

	// Remove the session from the server's connections
	h.server.mu.Lock()
	for username, conn := range h.server.conns {
		if conn == session {
			delete(h.server.conns, username)
			break
		}
	}
	h.server.mu.Unlock()
}

func (srv *SMPPServer) handleInboundSMS() {
	for msg := range srv.smsOutboundChannel {
		go func(m SMSMessage) {
			logf := LoggingFormat{Path: "sms_smpp", Function: "handleInboundSMS"}
			if m.Route == nil {
				logf.Level = logrus.WarnLevel
				logf.Message = fmt.Sprintf("No route found for message: From=%s, To=%s", m.Source, m.Destination)
				logf.Print()
				return
			}

			switch m.Route.Type {
			case "carrier":
				logf.Level = logrus.InfoLevel
				logf.Message = fmt.Sprintf("Sending SMS via carrier: %s", m.Route.Endpoint)
				logf.Print()
				// Implement carrier-specific logic here

				switch m.Route.Endpoint {
				case "twilio":
					sms := CarrierMessage{
						From:        m.Source,
						To:          m.Destination,
						Content:     m.Content,
						CarrierData: nil,
					}

					err := m.Route.Handler.SendSMS(&sms)
					if err != nil {
						logf.Level = logrus.ErrorLevel
						logf.Message = fmt.Sprintf("Failed to send SMS")
						logf.Error = err
						logf.Print()
						return
					}
				default:
					logf.Level = logrus.WarnLevel
					logf.Message = fmt.Sprintf("Error sending to carrier")
					logf.Print()
				}
			case "smpp":
				logf.Level = logrus.WarnLevel
				logf.Message = fmt.Sprintf("NOT IMPLEMENTED - Sending SMS via SMPP: %s", m.Route.Endpoint)
				logf.Print()
				// Implement SMPP client logic here
			default:
				logf.Level = logrus.WarnLevel
				logf.Message = fmt.Sprintf("Unknown route type: %s", m.Route.Type)
				logf.Print()
			}
		}(msg)
	}
}

func (srv *SMPPServer) handleOutboundSMS() {
	for m := range srv.smsInboundChannel {
		go func(msg *CarrierMessage) {
			logf := LoggingFormat{Path: "sms_smpp", Function: "handleOutboundSMS"}

			session, err := srv.findSmppSession(msg.To)
			if err != nil {
				logf.Level = logrus.ErrorLevel
				logf.Message = fmt.Sprintf("Error finding SMPP session: %v", err)
				logf.Print()
				return
			}

			nextSeq := session.NextSequence

			submitSM := &pdu.DeliverSM{
				SourceAddr:         pdu.Address{TON: 0x01, NPI: 0x01, No: msg.From},
				DestAddr:           pdu.Address{TON: 0x01, NPI: 0x01, No: msg.To},
				Message:            pdu.ShortMessage{Message: []byte(msg.Content)},
				RegisteredDelivery: pdu.RegisteredDelivery{MCDeliveryReceipt: 1},
				Header:             pdu.Header{Sequence: nextSeq()},
			}

			/*cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()*/

			err = session.Send(submitSM)
			if err != nil {
				logf.Level = logrus.ErrorLevel
				logf.Message = fmt.Sprintf("SMS send failed with status: %d", err)
				logf.Print()
			} else {
				logf.Level = logrus.InfoLevel
				logf.Message = fmt.Sprintf("SMS sent successfully via SMPP - From: %s To: %s", msg.From, msg.To)
				logf.Print()
				/*log.Printf("%s", resp)*/
			}
		}(&m)
	}
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
