package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"time"
	"unicode"
	"zultys-smpp-mm4/smpp"
	"zultys-smpp-mm4/smpp/coding"
	"zultys-smpp-mm4/smpp/pdu"

	"github.com/sirupsen/logrus"
	"go.mongodb.org/mongo-driver/bson/primitive"
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
	lm := srv.gateway.LogManager

	srv.pendingAcks = make(map[int32]chan *pdu.DeliverSMResp)

	lm.SendLog(lm.BuildLog(
		"Server.SMPP.Start",
		"StartingSMPPServer",
		logrus.InfoLevel,
		map[string]interface{}{
			"listen_addr": smppListen,
		},
	))

	go func() {
		err := smpp.ServeTCP(smppListen, handler, nil)
		if err != nil {
			lm.SendLog(lm.BuildLog(
				"Server.SMPP.Start",
				"SMPPServeTCPError",
				logrus.FatalLevel,
				map[string]interface{}{
					"listen_addr": smppListen,
				}, err,
			))
			panic(err)
		}
	}()

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

// getSessionClientInfo returns (username, *Client) for a given session, if known.
func (srv *SMPPServer) getSessionClientInfo(session *smpp.Session) (string, *Client) {
	if srv == nil || session == nil || srv.gateway == nil {
		return "", nil
	}

	srv.mu.RLock()
	defer srv.mu.RUnlock()

	for username, sess := range srv.conns {
		if sess == session {
			if srv.gateway.Clients != nil {
				if c, ok := srv.gateway.Clients[username]; ok {
					return username, c
				}
			}
			return username, nil
		}
	}
	return "", nil
}

func (srv *SMPPServer) removeSession(session *smpp.Session) {
	srv.mu.Lock()
	defer srv.mu.Unlock()

	var lm *LogManager
	if srv.gateway != nil {
		lm = srv.gateway.LogManager
	}

	for username, sess := range srv.conns {
		if sess == session {
			delete(srv.conns, username)

			if lm != nil {
				ip := ""
				if session != nil && session.Parent != nil && session.Parent.RemoteAddr() != nil {
					ip = session.Parent.RemoteAddr().String()
				}

				clientName := ""
				if srv.gateway != nil && srv.gateway.Clients != nil {
					if c, ok := srv.gateway.Clients[username]; ok && c != nil {
						clientName = c.Username
					}
				}

				lm.SendLog(lm.BuildLog(
					"Server.SMPP.removeSession",
					"SessionRemoved",
					logrus.InfoLevel,
					map[string]interface{}{
						"username": username,
						"client":   clientName,
						"ip":       ip,
					},
				))
			}
			break
		}
	}
}

func (h *SimpleHandler) Serve(session *smpp.Session) {
	lm := h.server.gateway.LogManager

	ip := ""
	if session != nil && session.Parent != nil && session.Parent.RemoteAddr() != nil {
		ip = session.Parent.RemoteAddr().String()
	}

	username, client := h.server.getSessionClientInfo(session)
	clientName := ""
	if client != nil {
		clientName = client.Username
	}

	lm.SendLog(lm.BuildLog(
		"Server.SMPP.Serve",
		"SessionStarted",
		logrus.InfoLevel,
		map[string]interface{}{
			"ip":       ip,
			"username": username,
			"client":   clientName,
		},
	))

	ctx, cancel := context.WithCancel(context.Background())

	// Track who caused the session to end.
	var closedByServer bool
	var closedByClient bool

	defer func() {
		// Stop background goroutines.
		cancel()

		// Only force-close the SMPP session when *we* initiated the shutdown.
		if closedByServer {
			if err := session.Close(context.Background()); err != nil {
				lm.SendLog(lm.BuildLog(
					"Server.SMPP.Serve",
					"SessionCloseError",
					logrus.WarnLevel,
					map[string]interface{}{
						"ip":       ip,
						"username": username,
						"client":   clientName,
					},
					err,
				))
			}
		}

		// Remove from our tracking map.
		h.server.removeSession(session)

		lm.SendLog(lm.BuildLog(
			"Server.SMPP.Serve",
			"SessionClosed",
			logrus.InfoLevel,
			map[string]interface{}{
				"ip":               ip,
				"username":         username,
				"client":           clientName,
				"closed_by_server": closedByServer,
				"closed_by_client": closedByClient,
			},
		))
	}()

	// Periodic enquire_link
	go h.enquireLink(session, ctx)

	for {
		select {
		case <-ctx.Done():
			// Some higher-level logic canceled this context – treat as server-driven.
			// closedByServer = true
			return

		case packet, ok := <-session.PDU():
			if !ok {
				// PDU channel closed – usually remote close/unbind or TCP EOF.
				username, client := h.server.getSessionClientInfo(session)
				clientName := ""
				if client != nil {
					clientName = client.Username
				}

				closedByClient = true

				lm.SendLog(lm.BuildLog(
					"Server.SMPP.Serve",
					"PDUChannelClosed",
					logrus.DebugLevel,
					map[string]interface{}{
						"ip":       ip,
						"username": username,
						"client":   clientName,
					},
				))

				// Do NOT call session.Close() here; remote/library already handled it.
				return
			}

			// If handlePDU detects a fatal condition, it can optionally cancel ctx
			// (which will set closedByServer on the next select iteration),
			// or you can change handlePDU to return an error and set closedByServer here.
			h.handlePDU(session, packet)
		}
	}
}
func (h *SimpleHandler) enquireLink(session *smpp.Session, ctx context.Context) {
	lm := h.server.gateway.LogManager
	tick := time.NewTicker(15 * time.Second) // keep your 15s if that matches clients
	defer tick.Stop()

	for {
		select {
		case <-ctx.Done():
			return

		case <-tick.C:
			// Use ctx directly like the old version to keep behavior identical.
			err := session.EnquireLink(ctx, 15*time.Second, 5*time.Second)
			if err != nil {
				username, client := h.server.getSessionClientInfo(session)
				clientName := ""
				if client != nil {
					clientName = client.Username
				}

				lm.SendLog(lm.BuildLog(
					"Server.SMPP.EnquireLink",
					"SMPPEnquireLinkError",
					logrus.WarnLevel,
					map[string]interface{}{
						"ip":       session.Parent.RemoteAddr().String(),
						"username": username,
						"client":   clientName,
					},
					err,
				))

				// Let the main loop / library handle closing; we just stop pinging.
				return
			}

			// Optional; keep at Debug so it doesn't spam.
			username, client := h.server.getSessionClientInfo(session)
			clientName := ""
			if client != nil {
				clientName = client.Username
			}

			lm.SendLog(lm.BuildLog(
				"Server.SMPP.EnquireLink",
				"EnquireLinkOK",
				logrus.DebugLevel,
				map[string]interface{}{
					"ip":       session.Parent.RemoteAddr().String(),
					"username": username,
					"client":   clientName,
				},
			))
		}
	}
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
	lm := h.server.gateway.LogManager

	username, client := h.server.getSessionClientInfo(session)
	clientName := ""
	if client != nil {
		clientName = client.Username
	}

	switch p := packet.(type) {
	case *pdu.BindTransceiver:
		h.handleBind(session, p)

	case *pdu.SubmitSM:
		// Auth/session check is done inside handleSubmitSM using conns map.
		h.handleSubmitSM(session, p)

	case *pdu.DeliverSMResp:
		seq := p.Header.Sequence
		h.server.pendingAcksMu.Lock()
		ackCh, exists := h.server.pendingAcks[seq]
		h.server.pendingAcksMu.Unlock()
		if exists {
			select {
			case ackCh <- p:
			default:
			}
			h.server.removePendingAck(seq)
		}

	case *pdu.DeliverSM:
		h.handleDeliverSM(session, p)

	case *pdu.Unbind:
		h.handleUnbind(session, p)

	case *pdu.UnbindResp:
		lm.SendLog(lm.BuildLog(
			"Server.SMPP.HandlePDU",
			"SMPPUnbindRespReceived",
			logrus.DebugLevel,
			map[string]interface{}{
				"ip":             session.Parent.RemoteAddr().String(),
				"username":       username,
				"client":         clientName,
				"go_type":        "*pdu.UnbindResp",
				"command_status": p.Header.CommandStatus,
				"sequence":       p.Header.Sequence,
			},
		))

	case *pdu.EnquireLinkResp:
		lm.SendLog(lm.BuildLog(
			"Server.SMPP.HandlePDU",
			"SMPPEnquireLinkRespReceived",
			logrus.DebugLevel,
			map[string]interface{}{
				"ip":             session.Parent.RemoteAddr().String(),
				"username":       username,
				"client":         clientName,
				"go_type":        "*pdu.EnquireLinkResp",
				"command_status": p.Header.CommandStatus,
				"sequence":       p.Header.Sequence,
			},
		))

	case pdu.Responsable:
		if err := session.Send(p.Resp()); err != nil {
			lm.SendLog(lm.BuildLog(
				"Server.SMPP.HandlePDU",
				"SMPPResponsableError",
				logrus.ErrorLevel,
				map[string]interface{}{
					"ip":       session.Parent.RemoteAddr().String(),
					"username": username,
					"client":   clientName,
					"go_type":  fmt.Sprintf("%T", packet),
				}, err,
			))
		}

	default:
		var cmdID uint32
		var seq uint32

		if hPDU, ok := packet.(interface{ GetHeader() pdu.Header }); ok {
			header := hPDU.GetHeader()
			cmdID = uint32(header.CommandID)
			seq = uint32(header.Sequence)
		} else if hWithHeader, ok := packet.(interface{ Header() pdu.Header }); ok {
			header := hWithHeader.Header()
			cmdID = uint32(header.CommandID)
			seq = uint32(header.Sequence)
		}

		lm.SendLog(lm.BuildLog(
			"Server.SMPP.HandlePDU",
			"SMPPUnhandledPDU",
			logrus.WarnLevel,
			map[string]interface{}{
				"ip":         session.Parent.RemoteAddr().String(),
				"username":   username,
				"client":     clientName,
				"go_type":    fmt.Sprintf("%T", packet),
				"command_id": cmdID,
				"sequence":   seq,
			},
		))
	}
}

func (h *SimpleHandler) handleBind(session *smpp.Session, bindReq *pdu.BindTransceiver) {
	lm := h.server.gateway.LogManager

	username := bindReq.SystemID
	password := bindReq.Password

	ip, err := h.server.GetClientIP(session)
	if err != nil {
		ip = session.Parent.RemoteAddr().String()
	}

	// Helper to send a bind_resp with a given status and then close the session.
	sendBindError := func(status pdu.CommandStatus, reason string, logErr error) {
		resp := bindReq.Resp(status)
		_ = session.Send(resp)
		_ = session.Close(context.Background())

		lm.SendLog(lm.BuildLog(
			"Server.SMPP.HandleBind",
			reason,
			logrus.ErrorLevel,
			map[string]interface{}{
				"ip":       ip,
				"username": username,
				"status":   status,
			}, logErr,
		))
	}

	if username == "" || password == "" {
		sendBindError(pdu.ErrInvalidSystemID, "AuthFailedMissingCredentials", nil)
		return
	}

	authed, err := h.server.gateway.authClient(username, password)
	if err != nil {
		sendBindError(pdu.ErrInvalidPasswd, "AuthFailedInternal", err)
		return
	}

	if !authed {
		sendBindError(pdu.ErrInvalidPasswd, "AuthFailedInvalidCredentials", nil)
		return
	}

	resp := bindReq.Resp(pdu.ESME_ROK)
	if err = session.Send(resp); err != nil {
		lm.SendLog(lm.BuildLog(
			"Server.SMPP.HandleBind",
			"SMPPPDUError",
			logrus.ErrorLevel,
			map[string]interface{}{
				"ip":       ip,
				"username": username,
			}, err,
		))
		_ = session.Close(context.Background())
		return
	}

	clientName := ""
	if h.server.gateway != nil && h.server.gateway.Clients != nil {
		if c, ok := h.server.gateway.Clients[username]; ok && c != nil {
			clientName = c.Username
		}
	}

	lm.SendLog(lm.BuildLog(
		"Server.SMPP.HandleBind",
		"AuthSuccess",
		logrus.InfoLevel,
		map[string]interface{}{
			"ip":       ip,
			"username": username,
			"client":   clientName,
		},
	))

	h.server.mu.Lock()
	// Close old session if exists
	if oldSession, exists := h.server.conns[username]; exists && oldSession != session {
		lm.SendLog(lm.BuildLog(
			"Server.SMPP.HandleBind",
			"ClosingExistingSessionForUser",
			logrus.InfoLevel,
			map[string]interface{}{
				"ip":       ip,
				"username": username,
				"client":   clientName,
			},
		))
		_ = oldSession.Close(context.Background())
	}
	h.server.conns[username] = session
	h.server.mu.Unlock()
}

func (h *SimpleHandler) handleSubmitSM(session *smpp.Session, submitSM *pdu.SubmitSM) {
	transId := primitive.NewObjectID().Hex()
	lm := h.server.gateway.LogManager

	// Find the client associated with this session via the conns map
	var (
		client   *Client
		username string
	)
	h.server.mu.RLock()
	for u, conn := range h.server.conns {
		if conn == session {
			username = u
			client = h.server.gateway.Clients[u]
			break
		}
	}
	h.server.mu.RUnlock()

	if client == nil {
		lm.SendLog(lm.BuildLog(
			"Server.SMPP.HandleSubmitSM",
			"SMPPUnknownOrUnauthedSession",
			logrus.ErrorLevel,
			map[string]interface{}{
				"ip": session.Parent.RemoteAddr().String(),
			},
		))
		resp := submitSM.Resp()
		_ = session.Send(resp)
		return
	}

	var decodedMsg string
	encoding := coding.GSM7BitCoding

	if submitSM.Message.DataCoding != 0 {
		if submitSM.Message.DataCoding == 8 { // UTF-16
			encoding = coding.UCS2Coding
		} else if submitSM.Message.DataCoding == 1 { // ASCII
			encoding = coding.ASCIICoding
		}
		decodedMsg, _ = encoding.Encoding().NewDecoder().String(string(submitSM.Message.Message))
	} else {
		decodedMsg, _ = decodeUnpackedGSM7(submitSM.Message.Message)
	}

	if decodedMsg == "" {
		lm.SendLog(lm.BuildLog(
			"Server.SMPP.HandleSubmitSM",
			"EmptyDecodedMessage",
			logrus.DebugLevel,
			map[string]interface{}{
				"client":   client.Username,
				"logID":    transId,
				"username": username,
				"ip":       session.Parent.RemoteAddr().String(),
				"coding":   submitSM.Message.DataCoding,
			},
		))

		resp := submitSM.Resp()
		if err := session.Send(resp); err != nil {
			lm.SendLog(lm.BuildLog(
				"Server.SMPP.HandleSubmitSM",
				"SMPPPDUError",
				logrus.ErrorLevel,
				map[string]interface{}{
					"ip":       session.Parent.RemoteAddr().String(),
					"client":   client.Username,
					"username": username,
				}, err,
			))
		}

		return
	}

	numData := h.server.gateway.getNumber(submitSM.SourceAddr.String())
	if numData.IgnoreStopCmdSending && decodedMsg == "Reply STOP to end messages." {
		lm.SendLog(lm.BuildLog(
			"Server.SMPP.HandleSubmitSM",
			"DroppingSTOPMessagePerClientRule",
			logrus.DebugLevel,
			map[string]interface{}{
				"ip":       session.Parent.RemoteAddr().String(),
				"client":   client.Username,
				"from":     numData.Number,
				"username": username,
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
		"InboundSubmitSM",
		logrus.InfoLevel,
		map[string]interface{}{
			"client":     client.Username,
			"username":   username,
			"logID":      transId,
			"from":       msgQueueItem.From,
			"to":         msgQueueItem.To,
			"decodedMsg": decodedMsg,
		},
	))

	// Compute conversation hash.
	convoID := computeCorrelationKey(msgQueueItem.From, msgQueueItem.To)
	// Add the message to the conversation manager.
	h.server.gateway.ConvoManager.AddMessage(convoID, msgQueueItem, h.server.gateway.Router)

	resp := submitSM.Resp()
	if err := session.Send(resp); err != nil {
		lm.SendLog(lm.BuildLog(
			"Server.SMPP.HandleSubmitSM",
			"SMPPPDUError",
			logrus.ErrorLevel,
			map[string]interface{}{
				"ip":       session.Parent.RemoteAddr().String(),
				"client":   client.Username,
				"username": username,
			}, err,
		))
	}
}

func (h *SimpleHandler) handleDeliverSM(session *smpp.Session, deliverSM *pdu.DeliverSM) {
	lm := h.server.gateway.LogManager

	username, client := h.server.getSessionClientInfo(session)
	clientName := ""
	if client != nil {
		clientName = client.Username
	}

	resp := deliverSM.Resp()
	err := session.Send(resp)
	if err != nil {
		lm.SendLog(lm.BuildLog(
			"Server.SMPP.HandleDeliverSM",
			"DeliverSMRespSendError",
			logrus.ErrorLevel,
			map[string]interface{}{
				"ip":       session.Parent.RemoteAddr().String(),
				"username": username,
				"client":   clientName,
			}, err,
		))
	} else {
		lm.SendLog(lm.BuildLog(
			"Server.SMPP.HandleDeliverSM",
			"DeliverSMRespSent",
			logrus.DebugLevel,
			map[string]interface{}{
				"ip":       session.Parent.RemoteAddr().String(),
				"username": username,
				"client":   clientName,
			},
		))
	}
}

func (h *SimpleHandler) handleUnbind(session *smpp.Session, unbind *pdu.Unbind) {
	lm := h.server.gateway.LogManager

	username, client := h.server.getSessionClientInfo(session)
	clientName := ""
	if client != nil {
		clientName = client.Username
	}

	lm.SendLog(lm.BuildLog(
		"Server.SMPP.HandleUnbind",
		"ReceivedUnbind",
		logrus.InfoLevel,
		map[string]interface{}{
			"ip":       session.Parent.RemoteAddr().String(),
			"username": username,
			"client":   clientName,
		},
	))

	resp := unbind.Resp()
	if err := session.Send(resp); err != nil {
		lm.SendLog(lm.BuildLog(
			"Server.SMPP.HandleUnbind",
			"SMPPPDUError",
			logrus.ErrorLevel,
			map[string]interface{}{
				"ip":       session.Parent.RemoteAddr().String(),
				"username": username,
				"client":   clientName,
			}, err,
		))
	}

	_ = session.Close(context.Background())
	h.server.removeSession(session)
}

// replacementMap defines replacements for non-standard characters.
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
	if r >= 0x1F600 && r <= 0x1F64F {
		return true
	}
	if r >= 0x1F300 && r <= 0x1F5FF {
		return true
	}
	if r >= 0x1F680 && r <= 0x1F6FF {
		return true
	}
	if r >= 0x2600 && r <= 0x26FF {
		return true
	}
	if r >= 0x2700 && r <= 0x27BF {
		return true
	}
	if r >= 0x1F900 && r <= 0x1F9FF {
		return true
	}
	return false
}

// isGSMAllowed checks if a rune is allowed by our GSM whitelist.
func isGSMAllowed(r rune) bool {
	if unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsSpace(r) {
		return true
	}
	allowedPunct := ".,!?;:'\"-()[]{}"
	if strings.ContainsRune(allowedPunct, r) {
		return true
	}
	return false
}

// cleanSMSMessage processes the input message.
func cleanSMSMessage(input string) string {
	var output strings.Builder

	for _, r := range input {
		if r == '\x00' || r == '\x1B' || (r < 32 && r != '\n' && r != '\r' && r != '\t') || r == 127 || (r >= 0xD800 && r <= 0xDFFF) {
			continue
		}

		if isEmoji(r) {
			output.WriteRune(r)
			continue
		}

		if isGSMAllowed(r) {
			output.WriteRune(r)
			continue
		}

		if replacement, ok := replacementMap[r]; ok {
			output.WriteString(replacement)
			continue
		}
	}

	return output.String()
}

// sendSMPP attempts to send an SMPPMessage via the SMPP server.
func (s *SMPPServer) sendSMPP(msg MsgQueueItem, session *smpp.Session) error {
	lm := s.gateway.LogManager

	lm.SendLog(lm.BuildLog(
		"Server.SMPP.sendSMPP",
		"AttemptSend",
		logrus.DebugLevel,
		map[string]interface{}{
			"to":   msg.To,
			"from": msg.From,
		},
	))

	// Find the SMPP session associated with the destination number
	var err error
	session, err = s.findSmppSession(msg.To)
	if err != nil {
		lm.SendLog(lm.BuildLog(
			"Server.SMPP.sendSMPP",
			"FindSessionError",
			logrus.ErrorLevel,
			map[string]interface{}{
				"to": msg.To,
			}, err,
		))
		return fmt.Errorf("error finding SMPP session: %v", err)
	}

	username, client := s.getSessionClientInfo(session)
	clientName := ""
	if client != nil {
		clientName = client.Username
	}

	nextSeq := session.NextSequence

	// Determine best encoding + segmenting
	bestCoding := coding.BestSafeCoding(msg.message)
	segments := coding.SplitSMS(msg.message, byte(bestCoding))
	encoder := bestCoding.Encoding().NewEncoder()

	for i, segment := range segments {
		var encoded []byte
		if bestCoding == coding.GSM7BitCoding {
			encoded, err = encodeUnpackedGSM7(segment)
			if err != nil {
				lm.SendLog(lm.BuildLog(
					"Server.SMPP.sendSMPP",
					"GSM7EncodeError",
					logrus.ErrorLevel,
					map[string]interface{}{
						"to":       msg.To,
						"from":     msg.From,
						"segment":  i,
						"username": username,
						"client":   clientName,
					}, err,
				))
				return fmt.Errorf("GSM7 encode error: %w", err)
			}
		} else {
			encoded, err = encoder.Bytes([]byte(segment))
			if err != nil {
				lm.SendLog(lm.BuildLog(
					"Server.SMPP.sendSMPP",
					"EncodingError",
					logrus.ErrorLevel,
					map[string]interface{}{
						"to":       msg.To,
						"from":     msg.From,
						"segment":  i,
						"username": username,
						"client":   clientName,
					}, err,
				))
				return fmt.Errorf("encoding error: %w", err)
			}
		}

		seq := nextSeq()
		deliverSM := &pdu.DeliverSM{
			SourceAddr: pdu.Address{TON: 0x01, NPI: 0x01, No: msg.From},
			DestAddr:   pdu.Address{TON: 0x01, NPI: 0x01, No: msg.To},
			Message:    pdu.ShortMessage{Message: encoded, DataCoding: bestCoding},
			RegisteredDelivery: pdu.RegisteredDelivery{
				MCDeliveryReceipt: 1,
			},
			Header: pdu.Header{
				Sequence: seq,
			},
		}

		lm.SendLog(lm.BuildLog(
			"Server.SMPP.sendSMPP",
			"SendingSegment",
			logrus.DebugLevel,
			map[string]interface{}{
				"to":        msg.To,
				"from":      msg.From,
				"segment":   i,
				"sequence":  seq,
				"num_parts": len(segments),
				"username":  username,
				"client":    clientName,
			},
		))

		ackCh := s.addPendingAck(seq)
		if err := session.Send(deliverSM); err != nil {
			s.removePendingAck(seq)
			lm.SendLog(lm.BuildLog(
				"Server.SMPP.sendSMPP",
				"SendError",
				logrus.ErrorLevel,
				map[string]interface{}{
					"to":       msg.To,
					"from":     msg.From,
					"sequence": seq,
					"username": username,
					"client":   clientName,
				}, err,
			))
			return fmt.Errorf("error sending SubmitSM: %v", err)
		}

		select {
		case respPDU := <-ackCh:
			if respPDU.Header.CommandStatus != 0 {
				lm.SendLog(lm.BuildLog(
					"Server.SMPP.sendSMPP",
					"AckNonOK",
					logrus.ErrorLevel,
					map[string]interface{}{
						"to":            msg.To,
						"from":          msg.From,
						"sequence":      seq,
						"commandStatus": respPDU.Header.CommandStatus,
						"username":      username,
						"client":        clientName,
					},
				))
				return fmt.Errorf("non-OK response for sequence %d: %d", seq, respPDU.Header.CommandStatus)
			}
			lm.SendLog(lm.BuildLog(
				"Server.SMPP.sendSMPP",
				"AckOK",
				logrus.DebugLevel,
				map[string]interface{}{
					"ip":       session.Parent.RemoteAddr().String(),
					"sequence": seq,
					"to":       msg.To,
					"from":     msg.From,
					"username": username,
					"client":   clientName,
				},
			))
		case <-time.After(5 * time.Second):
			s.removePendingAck(seq)
			lm.SendLog(lm.BuildLog(
				"Server.SMPP.sendSMPP",
				"AckTimeout",
				logrus.WarnLevel,
				map[string]interface{}{
					"to":       msg.To,
					"from":     msg.From,
					"sequence": seq,
					"username": username,
					"client":   clientName,
				},
			))
			return fmt.Errorf("timeout waiting for ack for sequence %d", seq)
		}
	}

	return nil
}

func (srv *SMPPServer) findSmppSession(destination string) (*smpp.Session, error) {
	lm := srv.gateway.LogManager
	srv.mu.RLock()
	defer srv.mu.RUnlock()

	// Debug: Log the search
	clientCount := len(srv.gateway.Clients)
	connCount := len(srv.conns)
	lm.SendLog(lm.BuildLog(
		"SMPPServer.DEBUG",
		"FindSmppSessionStart",
		logrus.DebugLevel,
		map[string]interface{}{
			"destination": destination,
			"clientCount": clientCount,
			"connCount":   connCount,
			"connUsernames": func() []string {
				names := make([]string, 0, len(srv.conns))
				for username := range srv.conns {
					names = append(names, username)
				}
				return names
			}(),
		},
	))

	for _, client := range srv.gateway.Clients {
		for _, num := range client.Numbers {
			// Debug: Log each number check
			isMatch := strings.Contains(destination, num.Number)
			lm.SendLog(lm.BuildLog(
				"SMPPServer.DEBUG",
				"FindSmppSessionCheck",
				logrus.DebugLevel,
				map[string]interface{}{
					"destination":    destination,
					"clientUsername": client.Username,
					"numberChecked":  num.Number,
					"isMatch":        isMatch,
				},
			))

			if isMatch {
				if session, ok := srv.conns[client.Username]; ok {
					lm.SendLog(lm.BuildLog(
						"SMPPServer.DEBUG",
						"FindSmppSessionFound",
						logrus.DebugLevel,
						map[string]interface{}{
							"destination":    destination,
							"clientUsername": client.Username,
							"numberMatched":  num.Number,
							"sessionFound":   true,
						},
					))
					return session, nil
				}
				lm.SendLog(lm.BuildLog(
					"SMPPServer.DEBUG",
					"FindSmppSessionClientNotConnected",
					logrus.WarnLevel,
					map[string]interface{}{
						"destination":    destination,
						"clientUsername": client.Username,
						"numberMatched":  num.Number,
					},
				))
				return nil, fmt.Errorf("client found but not connected: %s", client.Username)
			}
		}
	}

	lm.SendLog(lm.BuildLog(
		"SMPPServer.DEBUG",
		"FindSmppSessionNotFound",
		logrus.WarnLevel,
		map[string]interface{}{
			"destination": destination,
			"clientCount": clientCount,
			"connCount":   connCount,
		},
	))

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

	host, _, err := net.SplitHostPort(addr.String())
	if err != nil {
		return "", fmt.Errorf("error splitting host and port: %w", err)
	}

	return host, nil
}
