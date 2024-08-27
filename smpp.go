package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"github.com/M2MGateway/go-smpp"
	"github.com/M2MGateway/go-smpp/pdu"
	"log"
	"net"
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

type Route struct {
	Prefix   string
	Type     string // "carrier" or "smpp"
	Endpoint string
	Handler  CarrierHandler
}

type SmppServer struct {
	TLS                *tls.Config
	Clients            map[string]*Client // Map of Username to Client
	conns              map[string]*smpp.Session
	mu                 sync.RWMutex
	l                  net.Listener
	smsInboundChannel  chan SMSMessage
	smsOutboundChannel chan SMS
	routes             []Route
}

func initSmppServer() (*SmppServer, error) {
	clients, err := loadClients()
	if err != nil {
		return nil, fmt.Errorf("failed to load clients: %v", err)
	}

	clientMap := make(map[string]*Client)
	for i := range clients {
		clientMap[clients[i].Username] = &clients[i]
	}

	return &SmppServer{
		Clients:            clientMap,
		conns:              make(map[string]*smpp.Session),
		smsInboundChannel:  make(chan SMSMessage),
		smsOutboundChannel: make(chan SMS),
		routes:             make([]Route, 0),
	}, nil
}

type SimpleHandler struct {
	server *SmppServer
}

func NewSimpleHandler(server *SmppServer) *SimpleHandler {
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
	ctx := context.Background()
	tick := 30 * time.Second
	timeout := 5 * time.Second

	err := session.EnquireLink(ctx, tick, timeout)
	if err != nil {
		log.Printf("EnquireLink process ended: %v", err)
	}
}

func (h *SimpleHandler) handlePDU(session *smpp.Session, packet any) {
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
		log.Printf("Received PDU: %T", p)
		err := session.Send(p.Resp())
		if err != nil {
			log.Printf("Error sending response: %v", err)
		}
	default:
		log.Printf("Received unhandled PDU: %T", p)
	}
}

func (h *SimpleHandler) handleBind(session *smpp.Session, bindReq *pdu.BindTransceiver) {
	log.Printf("Received bind request: %T", bindReq)

	username := bindReq.SystemID
	password := bindReq.Password

	if username == "" || password == "" {
		log.Printf("Empty system id or password: %s", username)
		return
	}

	authed, err := authClient(username, password, h.server.Clients)
	if err != nil {
		log.Printf("Authentication error: %v", err)
		return
	}

	if authed {
		resp := bindReq.Resp()
		err = session.Send(resp)
		if err != nil {
			log.Printf("Error sending bind response: %v", err)
		}

		h.server.mu.Lock()
		h.server.conns[username] = session
		h.server.mu.Unlock()

		log.Printf("Client %s authenticated successfully", username)
	} else {
		log.Printf("Authentication failed for client: %s", username)
	}
}

func (h *SimpleHandler) handleSubmitSM(session *smpp.Session, submitSM *pdu.SubmitSM) {
	log.Printf("Received SubmitSM: From=%s, To=%s", submitSM.SourceAddr, submitSM.DestAddr)

	// Find the client associated with this session
	var client *Client
	h.server.mu.RLock()
	for username, conn := range h.server.conns {
		if conn == session {
			client = h.server.Clients[username]
			break
		}
	}
	h.server.mu.RUnlock()

	if client == nil {
		log.Printf("Error: Unable to identify client for connection")
		return
	}

	route := h.server.findRoute(submitSM.SourceAddr.String(), submitSM.DestAddr.String())
	if route == nil {
		log.Printf("No route found for source %s and destination %s", submitSM.SourceAddr, submitSM.DestAddr)
		return
	}

	h.server.smsInboundChannel <- SMSMessage{
		Source:      submitSM.SourceAddr.String(),
		Destination: submitSM.DestAddr.String(),
		Content:     string(submitSM.Message.Message),
		Client:      client,
		Route:       route,
	}

	resp := submitSM.Resp()
	err := session.Send(resp)
	if err != nil {
		log.Printf("Error sending SubmitSM response: %v", err)
	}
}

func (srv *SmppServer) findRoute(source, destination string) *Route {
	carrier, err := srv.clientOutboundCarrier(source)
	if err != nil {
		log.Printf("Error finding carrier: %v", err)
		return nil
	}

	if carrier != "" {
		for _, route := range srv.routes {
			if route.Type == "carrier" && route.Endpoint == carrier {
				return &route
			}
		}
	}

	// Fallback to prefix-based routing if no carrier route found
	for _, route := range srv.routes {
		if strings.HasPrefix(destination, route.Prefix) {
			return &route
		}
	}

	return nil
}

func (srv *SmppServer) clientOutboundCarrier(source string) (string, error) {
	for _, client := range srv.Clients {
		for _, num := range client.Numbers {
			if strings.Contains(source, num.Number) {
				return num.Carrier, nil
			}
		}
	}

	return "", nil
}

func (srv *SmppServer) clientInboundConn(destination string) (*smpp.Session, error) {

	for _, client := range srv.Clients {
		for _, num := range client.Numbers {
			log.Printf("%s", num)
			if strings.Contains(destination, num.Number) {
				return srv.conns[client.Username], nil
			}
		}
	}

	return nil, nil
}

func (h *SimpleHandler) handleDeliverSM(session *smpp.Session, deliverSM *pdu.DeliverSM) {
	log.Printf("Received DeliverSM: From=%s, To=%s", deliverSM.SourceAddr, deliverSM.DestAddr)
	resp := deliverSM.Resp()
	err := session.Send(resp)
	if err != nil {
		log.Printf("Error sending DeliverSM response: %v", err)
	}
}

func (h *SimpleHandler) handleUnbind(session *smpp.Session, unbind *pdu.Unbind) {
	log.Printf("Received Unbind request")
	resp := unbind.Resp()
	err := session.Send(resp)
	if err != nil {
		log.Printf("Error sending Unbind response: %v", err)
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

func (srv *SmppServer) AddRoute(prefix, routeType, endpoint string, handler CarrierHandler) {
	srv.routes = append(srv.routes, Route{Prefix: prefix, Type: routeType, Endpoint: endpoint, Handler: handler})
}

func (srv *SmppServer) handleInboundSMS() {
	for msg := range srv.smsInboundChannel {
		go func(m SMSMessage) {
			if m.Route == nil {
				log.Printf("No route found for message: From=%s, To=%s", m.Source, m.Destination)
				return
			}

			switch m.Route.Type {
			case "carrier":
				log.Printf("Sending SMS via carrier: %s", m.Route.Endpoint)
				// Implement carrier-specific logic here

				switch m.Route.Endpoint {
				case "twilio":
					sms := SMS{
						From:        m.Source,
						To:          m.Destination,
						Content:     m.Content,
						CarrierData: nil,
					}

					err := m.Route.Handler.SendSMS(&sms)
					if err != nil {
						log.Printf(err.Error())
						return
					}
				default:
					log.Printf("error sending to carrier")
				}
			case "smpp":
				log.Printf("Sending SMS via SMPP: %s", m.Route.Endpoint)
				// Implement SMPP client logic here
			default:
				log.Printf("Unknown route type: %s", m.Route.Type)
			}
		}(msg)
	}
}

func (srv *SmppServer) handleOutboundSMS() {
	for m := range srv.smsOutboundChannel {
		go func(msg *SMS) {
			session, err := srv.findSmppSession(msg.To)
			if err != nil {
				log.Printf("Error finding SMPP session: %v", err)
			}

			nextSeq := session.NextSequence

			submitSM := &pdu.SubmitSM{
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
				log.Printf("SMS send failed with status: %d", err)
			} else {
				log.Printf("SMS sent successfully via SMPP: From %s To %s", msg.From, msg.To)
				/*log.Printf("%s", resp)*/
			}
		}(&m)
	}
}

func (srv *SmppServer) findSmppSession(destination string) (*smpp.Session, error) {
	srv.mu.RLock()
	defer srv.mu.RUnlock()

	for _, client := range srv.Clients {
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
