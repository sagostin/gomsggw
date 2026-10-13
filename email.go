package main

import (
	"bytes"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"net"
	"net/mail"
	"net/smtp"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
)

// ParseEmailAddresses parses a comma-separated recipient list, trimming
// whitespace and dropping empty/invalid entries.
func ParseEmailAddresses(raw string) []string {
	var out []string
	for _, part := range strings.Split(raw, ",") {
		addr := strings.TrimSpace(part)
		if addr == "" {
			continue
		}
		if _, err := mail.ParseAddress(addr); err != nil {
			continue
		}
		out = append(out, addr)
	}
	return out
}

// attachment represents a single email attachment decoded from a MsgFile.
type emailAttachment struct {
	Filename    string
	ContentType string
	Content     []byte
}

// collectAttachments decodes the message's media files into attachments.
// Files with only a MediaURL (not yet fetched) are skipped — their URL is
// included in the body instead.
func collectAttachments(m *MsgQueueItem) (attachments []emailAttachment, mediaLinks []string) {
	for i, f := range m.files {
		filename := f.Filename
		if filename == "" {
			filename = fmt.Sprintf("attachment-%d", i+1)
		}
		contentType := f.ContentType
		if contentType == "" {
			contentType = "application/octet-stream"
		}

		switch {
		case len(f.Content) > 0:
			attachments = append(attachments, emailAttachment{filename, contentType, f.Content})
		case len(f.Base64Data) > 0:
			if decoded, err := base64.StdEncoding.DecodeString(f.Base64Data); err == nil {
				attachments = append(attachments, emailAttachment{filename, contentType, decoded})
			} else if f.MediaURL != "" {
				mediaLinks = append(mediaLinks, f.MediaURL)
			}
		case f.MediaURL != "":
			mediaLinks = append(mediaLinks, f.MediaURL)
		}
	}
	return attachments, mediaLinks
}

// buildEmailMessage builds a MIME message (plain text + optional attachments)
// for an inbound SMS/MMS.
func buildEmailMessage(gateway *Gateway, m *MsgQueueItem, to []string) ([]byte, error) {
	attachments, mediaLinks := collectAttachments(m)

	var body strings.Builder
	fmt.Fprintf(&body, "From: %s\r\n", m.From)
	fmt.Fprintf(&body, "To: %s\r\n", m.To)
	fmt.Fprintf(&body, "Type: %s\r\n", strings.ToUpper(string(m.Type)))
	fmt.Fprintf(&body, "Received: %s\r\n", m.ReceivedTimestamp.Format(time.RFC1123Z))
	if m.SourceCarrier != "" {
		fmt.Fprintf(&body, "Carrier: %s\r\n", m.SourceCarrier)
	}
	body.WriteString("\r\n")
	body.WriteString(m.message)
	if len(mediaLinks) > 0 {
		body.WriteString("\r\n\r\nMedia:\r\n")
		for _, link := range mediaLinks {
			fmt.Fprintf(&body, "- %s\r\n", link)
		}
	}

	subject := fmt.Sprintf("%s from %s to %s", strings.ToUpper(string(m.Type)), m.From, m.To)

	var buf bytes.Buffer
	fmt.Fprintf(&buf, "From: %s\r\n", gateway.SMTPFrom)
	fmt.Fprintf(&buf, "To: %s\r\n", strings.Join(to, ", "))
	fmt.Fprintf(&buf, "Subject: %s\r\n", subject)
	fmt.Fprintf(&buf, "Date: %s\r\n", time.Now().Format(time.RFC1123Z))
	fmt.Fprintf(&buf, "Message-ID: <%s@gomsggw>\r\n", m.LogID)
	buf.WriteString("MIME-Version: 1.0\r\n")

	if len(attachments) == 0 {
		buf.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
		buf.WriteString("\r\n")
		buf.WriteString(body.String())
		return buf.Bytes(), nil
	}

	boundary := generateBoundary()
	fmt.Fprintf(&buf, "Content-Type: multipart/mixed; boundary=\"%s\"\r\n", boundary)
	buf.WriteString("\r\nThis is a multi-part message in MIME format.\r\n")

	// Text part
	fmt.Fprintf(&buf, "--%s\r\n", boundary)
	buf.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	buf.WriteString("Content-Transfer-Encoding: 8bit\r\n")
	buf.WriteString("\r\n")
	buf.WriteString(body.String())
	buf.WriteString("\r\n")

	// Attachment parts
	for _, att := range attachments {
		fmt.Fprintf(&buf, "--%s\r\n", boundary)
		fmt.Fprintf(&buf, "Content-Type: %s; name=\"%s\"\r\n", att.ContentType, att.Filename)
		buf.WriteString("Content-Transfer-Encoding: base64\r\n")
		fmt.Fprintf(&buf, "Content-Disposition: attachment; filename=\"%s\"\r\n", att.Filename)
		buf.WriteString("\r\n")
		encoded := base64.StdEncoding.EncodeToString(att.Content)
		// RFC 2045: base64 lines wrapped at 76 chars
		for i := 0; i < len(encoded); i += 76 {
			end := i + 76
			if end > len(encoded) {
				end = len(encoded)
			}
			buf.WriteString(encoded[i:end])
			buf.WriteString("\r\n")
		}
	}

	fmt.Fprintf(&buf, "--%s--\r\n", boundary)
	return buf.Bytes(), nil
}

// plainAuthPermissive implements SMTP PLAIN auth without net/smtp's
// TLS-or-localhost requirement — needed when SMTPEncryption is "none" on a
// trusted internal relay that still requires credentials.
type plainAuthPermissive struct {
	username string
	password string
}

func (a plainAuthPermissive) Start(_ *smtp.ServerInfo) (string, []byte, error) {
	return "PLAIN", []byte("\x00" + a.username + "\x00" + a.password), nil
}

func (a plainAuthPermissive) Next(_ []byte, more bool) ([]byte, error) {
	if more {
		return nil, fmt.Errorf("unexpected server challenge")
	}
	return nil, nil
}

// sendEmail delivers a pre-built RFC 5322 message to the given recipients via
// the gateway's configured SMTP server. Transport encryption is governed by
// gateway.SMTPEncryption: "starttls" (default), "tls" (implicit TLS), or
// "none" (plaintext — only for trusted internal relays).
func sendEmail(gateway *Gateway, to []string, message []byte) error {
	addr := net.JoinHostPort(gateway.SMTPHost, fmt.Sprintf("%d", gateway.SMTPPort))
	tlsConfig := &tls.Config{ServerName: gateway.SMTPHost, InsecureSkipVerify: gateway.SMTPInsecureSkipTLS}

	var client *smtp.Client
	var err error
	switch gateway.SMTPEncryption {
	case "none":
		client, err = smtp.Dial(addr)
		if err != nil {
			return fmt.Errorf("smtp dial: %w", err)
		}
	case "tls":
		conn, dialErr := tls.Dial("tcp", addr, tlsConfig)
		if dialErr != nil {
			return fmt.Errorf("smtp tls dial: %w", dialErr)
		}
		client, err = smtp.NewClient(conn, gateway.SMTPHost)
		if err != nil {
			conn.Close()
			return fmt.Errorf("smtp client: %w", err)
		}
	default: // "starttls"
		client, err = smtp.Dial(addr)
		if err != nil {
			return fmt.Errorf("smtp dial: %w", err)
		}
		if err := client.StartTLS(tlsConfig); err != nil {
			client.Close()
			return fmt.Errorf("smtp starttls: %w", err)
		}
	}
	defer client.Close()

	if gateway.SMTPUsername != "" {
		var auth smtp.Auth
		if gateway.SMTPEncryption == "none" {
			auth = plainAuthPermissive{gateway.SMTPUsername, gateway.SMTPPassword}
		} else {
			auth = smtp.PlainAuth("", gateway.SMTPUsername, gateway.SMTPPassword, gateway.SMTPHost)
		}
		if err := client.Auth(auth); err != nil {
			return fmt.Errorf("smtp auth: %w", err)
		}
	}

	if err := client.Mail(gateway.SMTPFrom); err != nil {
		return fmt.Errorf("smtp mail from: %w", err)
	}
	for _, rcpt := range to {
		if err := client.Rcpt(rcpt); err != nil {
			return fmt.Errorf("smtp rcpt to %s: %w", rcpt, err)
		}
	}
	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("smtp data: %w", err)
	}
	if _, err := w.Write(message); err != nil {
		return fmt.Errorf("smtp write: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("smtp data close: %w", err)
	}
	return client.Quit()
}

// DispatchEmail sends an inbound message to the configured email recipients.
// Fire-and-forget: intended to run in its own goroutine; all outcomes are
// logged via the LogManager and never propagate to message routing.
func (router *Router) DispatchEmail(m *MsgQueueItem, to []string) {
	lm := router.gateway.LogManager

	message, err := buildEmailMessage(router.gateway, m, to)
	if err != nil {
		lm.SendLog(lm.BuildLog(
			"Router.EmailDelivery",
			"BuildFailed",
			logrus.ErrorLevel,
			map[string]interface{}{
				"logID":      m.LogID,
				"from":       m.From,
				"to":         m.To,
				"recipients": strings.Join(to, ","),
				"msgType":    string(m.Type),
				"mediaCount": len(m.files),
				"error":      err.Error(),
			},
		))
		return
	}

	if err := sendEmail(router.gateway, to, message); err != nil {
		lm.SendLog(lm.BuildLog(
			"Router.EmailDelivery",
			"SendFailed",
			logrus.ErrorLevel,
			map[string]interface{}{
				"logID":      m.LogID,
				"from":       m.From,
				"to":         m.To,
				"recipients": strings.Join(to, ","),
				"smtpHost":   router.gateway.SMTPHost,
				"msgType":    string(m.Type),
				"error":      err.Error(),
			},
		))
		return
	}

	lm.SendLog(lm.BuildLog(
		"Router.EmailDelivery",
		"Sent",
		logrus.InfoLevel,
		map[string]interface{}{
			"logID":      m.LogID,
			"from":       m.From,
			"to":         m.To,
			"recipients": strings.Join(to, ","),
			"msgType":    string(m.Type),
			"mediaCount": len(m.files),
		},
	))
}
