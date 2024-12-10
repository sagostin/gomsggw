package smpp

import (
	"context"
	"crypto/tls"
	"github.com/pires/go-proxyproto"
	log "github.com/sirupsen/logrus"
	"net"
	"os"
)

type Handler interface{ Serve(*Session) }

type HandlerFunc func(*Session)

/*func (h HandlerFunc) Serve(session *Session) { h.Serve(session) }

func ServeTCP(address string, handler Handler, config *tls.Config) (err error) {
	var listener net.Listener
	if config == nil {
		listener, err = net.Listen("tcp", address)
	} else {
		listener, err = tls.Listen("tcp", address, config)
	}
	if err != nil {
		return
	}
	var parent net.Conn
	for {
		if parent, err = listener.Accept(); err != nil {
			return
		}
		go handler.Serve(NewSession(context.Background(), parent))
	}
}*/

func ServeTCP(address string, handler Handler, config *tls.Config) (err error) {
	var list net.Listener
	if config == nil {
		list, err = net.Listen("tcp", address)
	} else {
		list, err = tls.Listen("tcp", address, config)
	}

	var proxyListener net.Listener

	if os.Getenv("HAPROXY_PROXY_PROTOCOL") == "true" {
		proxyListener = &proxyproto.Listener{Listener: list}
		/*defer proxyListener.Close()*/

		// Wait for a connection and accept it
		conn, err := proxyListener.Accept()
		if err != nil {
			return err
		}

		/*defer func(conn net.Conn) {
			err := conn.Close()
			if err != nil {
				log.Fatal("couldn't close proxy connection")
			}
		}(conn)*/

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
		proxyListener = list
	}
	var parent net.Conn
	for {
		if parent, err = proxyListener.Accept(); err != nil {
			return
		}
		go handler.Serve(NewSession(context.Background(), parent))
	}
}
