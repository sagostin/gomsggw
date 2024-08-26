package main

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"sync"
	"time"

	"github.com/fiorix/go-smpp/smpp"
	"github.com/fiorix/go-smpp/smpp/pdu"
	"github.com/fiorix/go-smpp/smpp/pdu/pdufield"
)

// Default settings.
var (
	DefaultUser     = "client"
	DefaultPasswd   = "secret"
	DefaultSystemID = "smpptest"
)

type SMSMessage struct {
	Source      string
	Destination string
	Content     string
}

type Route struct {
	Prefix   string
	Type     string // "carrier" or "smpp"
	Endpoint string
}

// Server is an SMPP server for testing purposes.
type Server struct {
	User    string
	Passwd  string
	TLS     *tls.Config
	Handler HandlerFunc

	conns      map[smpp.Conn]struct{}
	mu         sync.Mutex
	l          net.Listener
	smsChannel chan SMSMessage
	routes     []Route
}

// HandlerFunc is the signature of a function that handles PDUs.
type HandlerFunc func(s *Server, c smpp.Conn, m pdu.Body)

// NewServer creates and initializes a new Server.
func NewServer() *Server {
	s := NewUnstartedServer()
	s.Start()
	return s
}

// NewUnstartedServer creates a new Server with default settings.
func NewUnstartedServer() *Server {
	return &Server{
		User:       DefaultUser,
		Passwd:     DefaultPasswd,
		Handler:    CustomHandler,
		l:          newLocalListener(),
		conns:      make(map[smpp.Conn]struct{}),
		smsChannel: make(chan SMSMessage, 1000),
		routes:     make([]Route, 0),
	}
}

func newLocalListener() net.Listener {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err == nil {
		return l
	}
	if l, err = net.Listen("tcp6", "[::1]:0"); err != nil {
		panic(fmt.Sprintf("smpptest: failed to listen on a port: %v", err))
	}
	return l
}

// Start starts the server.
func (srv *Server) Start() {
	go srv.Serve()
	go srv.ProcessSMS()
}

// Addr returns the local address of the server.
func (srv *Server) Addr() string {
	if srv.l == nil {
		return ""
	}
	return srv.l.Addr().String()
}

// Close stops the server.
func (srv *Server) Close() {
	if srv.l == nil {
		panic("smpptest: server is not started")
	}
	srv.l.Close()
}

// Serve accepts new clients and handles them.
func (srv *Server) Serve() {
	for {
		cli, err := srv.l.Accept()
		if err != nil {
			break // on srv.l.Close
		}
		c := newConn(cli)
		srv.mu.Lock()
		srv.conns[c] = struct{}{}
		srv.mu.Unlock()
		go srv.handle(c)
	}
}

// BroadcastMessage broadcasts a PDU to all bound clients.
func (srv *Server) BroadcastMessage(p pdu.Body) {
	srv.mu.Lock()
	defer srv.mu.Unlock()
	for c := range srv.conns {
		c.Write(p)
	}
}

// Conn implements a server side connection.
type conn struct {
	rwc net.Conn
	r   *bufio.Reader
	w   *bufio.Writer
}

func newConn(c net.Conn) *conn {
	return &conn{
		rwc: c,
		r:   bufio.NewReader(c),
		w:   bufio.NewWriter(c),
	}
}

func (c *conn) RemoteAddr() net.Addr {
	return c.rwc.RemoteAddr()
}

func (c *conn) Read() (pdu.Body, error) {
	return pdu.Decode(c.r)
}

func (c *conn) Write(p pdu.Body) error {
	var b bytes.Buffer
	err := p.SerializeTo(&b)
	if err != nil {
		return err
	}
	_, err = io.Copy(c.w, &b)
	if err != nil {
		return err
	}
	return c.w.Flush()
}

func (c *conn) Close() error {
	return c.rwc.Close()
}

func (srv *Server) handle(c *conn) {
	defer func() {
		c.Close()
		srv.mu.Lock()
		delete(srv.conns, c)
		srv.mu.Unlock()
	}()
	if err := srv.auth(c); err != nil {
		if err != io.EOF {
			log.Println("smpptest: server auth failed:", err)
		}
		return
	}
	for {
		p, err := c.Read()
		if err != nil {
			if err != io.EOF {
				log.Println("smpptest: read failed:", err)
			}
			break
		}
		srv.Handler(srv, c, p)
	}
}

func (srv *Server) auth(c *conn) error {
	p, err := c.Read()
	if err != nil {
		return err
	}
	var resp pdu.Body
	switch p.Header().ID {
	case pdu.BindTransmitterID:
		resp = pdu.NewBindTransmitterResp()
	case pdu.BindReceiverID:
		resp = pdu.NewBindReceiverResp()
	case pdu.BindTransceiverID:
		resp = pdu.NewBindTransceiverResp()
	default:
		return errors.New("unexpected pdu, want bind")
	}
	f := p.Fields()
	user := f[pdufield.SystemID]
	passwd := f[pdufield.Password]
	if user == nil || passwd == nil {
		return errors.New("malformed pdu, missing system_id/password")
	}
	if user.String() != srv.User {
		return errors.New("invalid user")
	}
	if passwd.String() != srv.Passwd {
		return errors.New("invalid passwd")
	}
	resp.Fields().Set(pdufield.SystemID, DefaultSystemID)
	return c.Write(resp)
}

func CustomHandler(s *Server, c smpp.Conn, m pdu.Body) {
	switch m.Header().ID {
	case pdu.SubmitSMID:
		handleSubmitSM(s, c, m)
	default:
		log.Printf("Received PDU: %s", m.Header().ID)
		c.Write(m)
	}
}

func handleSubmitSM(s *Server, c smpp.Conn, m pdu.Body) {
	f := m.Fields()
	sourceAddr := f[pdufield.SourceAddr].String()
	destAddr := f[pdufield.DestinationAddr].String()
	shortMessage := f[pdufield.ShortMessage].String()

	log.Printf("Received SubmitSM: From=%s, To=%s, Message=%s", sourceAddr, destAddr, shortMessage)

	// Send to channel for async processing
	s.smsChannel <- SMSMessage{
		Source:      sourceAddr,
		Destination: destAddr,
		Content:     shortMessage,
	}

	resp := pdu.NewSubmitSMResp()
	resp.Header().Seq = m.Header().Seq
	resp.Fields().Set(pdufield.MessageID, fmt.Sprintf("%d", time.Now().UnixNano()))
	err := c.Write(resp)
	if err != nil {
		log.Printf("Error sending SubmitSMResp: %v", err)
	}
}

func (srv *Server) AddRoute(prefix, routeType, endpoint string) {
	srv.routes = append(srv.routes, Route{Prefix: prefix, Type: routeType, Endpoint: endpoint})
}

func (srv *Server) findRoute(destination string) *Route {
	for _, route := range srv.routes {
		if len(destination) >= len(route.Prefix) && destination[:len(route.Prefix)] == route.Prefix {
			return &route
		}
	}
	return nil
}

func (srv *Server) ProcessSMS() {
	for msg := range srv.smsChannel {
		go func(m SMSMessage) {
			route := srv.findRoute(m.Destination)
			if route == nil {
				log.Printf("No route found for destination: %s", m.Destination)
				return
			}

			switch route.Type {
			case "carrier":
				log.Printf("Sending SMS via carrier: %s", route.Endpoint)
				// Implement carrier-specific logic here
			case "smpp":
				log.Printf("Sending SMS via SMPP: %s", route.Endpoint)
				// Implement SMPP client logic here
			default:
				log.Printf("Unknown route type: %s", route.Type)
			}
		}(msg)
	}
}
