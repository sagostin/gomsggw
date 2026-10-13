package main

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseEmailAddresses(t *testing.T) {
	assert.Nil(t, ParseEmailAddresses(""))
	assert.Nil(t, ParseEmailAddresses("   "))
	assert.Equal(t, []string{"a@b.com"}, ParseEmailAddresses("a@b.com"))
	assert.Equal(t, []string{"a@b.com", "c@d.org"}, ParseEmailAddresses("a@b.com, c@d.org"))
	// Invalid entries are dropped
	assert.Equal(t, []string{"a@b.com"}, ParseEmailAddresses("a@b.com, not-an-email, ,c@"))
	assert.Nil(t, ParseEmailAddresses("not-an-email"))
}

func TestResolveEmailDelivery_MasterOff(t *testing.T) {
	g := &Gateway{EmailDeliveryEnabled: false, SMTPHost: "smtp.example.com", SMTPFrom: "gw@example.com"}
	ns := &NumberSettings{EmailDeliveryEnabled: true, EmailDeliveryAddresses: "a@b.com"}
	enabled, addrs, _ := g.ResolveEmailDelivery(ns)
	assert.False(t, enabled, "master off must disable email delivery entirely")
	assert.Nil(t, addrs)
}

func TestResolveEmailDelivery_SMTPNotConfigured(t *testing.T) {
	g := &Gateway{EmailDeliveryEnabled: true} // no SMTP host/from
	ns := &NumberSettings{EmailDeliveryEnabled: true, EmailDeliveryAddresses: "a@b.com"}
	enabled, _, _ := g.ResolveEmailDelivery(ns)
	assert.False(t, enabled, "missing SMTP config must disable email delivery")

	g = &Gateway{EmailDeliveryEnabled: true, SMTPHost: "smtp.example.com"} // no from
	enabled, _, _ = g.ResolveEmailDelivery(ns)
	assert.False(t, enabled)
}

func TestResolveEmailDelivery_NumberOff(t *testing.T) {
	g := &Gateway{EmailDeliveryEnabled: true, SMTPHost: "smtp.example.com", SMTPFrom: "gw@example.com"}
	ns := &NumberSettings{EmailDeliveryEnabled: false, EmailDeliveryAddresses: "a@b.com"}
	enabled, _, _ := g.ResolveEmailDelivery(ns)
	assert.False(t, enabled)
}

func TestResolveEmailDelivery_NilSettings(t *testing.T) {
	g := &Gateway{EmailDeliveryEnabled: true, SMTPHost: "smtp.example.com", SMTPFrom: "gw@example.com"}
	enabled, _, _ := g.ResolveEmailDelivery(nil)
	assert.False(t, enabled)
}

func TestResolveEmailDelivery_EmptyAddresses(t *testing.T) {
	g := &Gateway{EmailDeliveryEnabled: true, SMTPHost: "smtp.example.com", SMTPFrom: "gw@example.com"}
	ns := &NumberSettings{EmailDeliveryEnabled: true, EmailDeliveryAddresses: "not-an-email"}
	enabled, addrs, _ := g.ResolveEmailDelivery(ns)
	assert.False(t, enabled, "no valid addresses should resolve to disabled")
	assert.Nil(t, addrs)
}

func TestResolveEmailDelivery_EnabledDefaultsToBoth(t *testing.T) {
	g := &Gateway{EmailDeliveryEnabled: true, SMTPHost: "smtp.example.com", SMTPFrom: "gw@example.com"}
	ns := &NumberSettings{EmailDeliveryEnabled: true, EmailDeliveryAddresses: "a@b.com, c@d.org"}
	enabled, addrs, mode := g.ResolveEmailDelivery(ns)
	assert.True(t, enabled)
	assert.Equal(t, []string{"a@b.com", "c@d.org"}, addrs)
	assert.Equal(t, EmailDeliveryModeBoth, mode, "unset mode must default to both")
}

func TestResolveEmailDelivery_EmailOnlyMode(t *testing.T) {
	g := &Gateway{EmailDeliveryEnabled: true, SMTPHost: "smtp.example.com", SMTPFrom: "gw@example.com"}
	ns := &NumberSettings{
		EmailDeliveryEnabled:   true,
		EmailDeliveryAddresses: "a@b.com",
		EmailDeliveryMode:      EmailDeliveryModeEmailOnly,
	}
	enabled, _, mode := g.ResolveEmailDelivery(ns)
	assert.True(t, enabled)
	assert.Equal(t, EmailDeliveryModeEmailOnly, mode)
}

func TestResolveEmailDelivery_InvalidModeFallsBackToBoth(t *testing.T) {
	g := &Gateway{EmailDeliveryEnabled: true, SMTPHost: "smtp.example.com", SMTPFrom: "gw@example.com"}
	ns := &NumberSettings{EmailDeliveryEnabled: true, EmailDeliveryAddresses: "a@b.com", EmailDeliveryMode: "bogus"}
	_, _, mode := g.ResolveEmailDelivery(ns)
	assert.Equal(t, EmailDeliveryModeBoth, mode)
}

func TestEffectiveEmailMode(t *testing.T) {
	assert.Equal(t, EmailDeliveryModeBoth, effectiveEmailMode(nil))
	assert.Equal(t, EmailDeliveryModeBoth, effectiveEmailMode(&NumberSettings{}))
	assert.Equal(t, EmailDeliveryModeEmailOnly, effectiveEmailMode(&NumberSettings{EmailDeliveryMode: EmailDeliveryModeEmailOnly}))
}

func TestSMTPConfigured(t *testing.T) {
	assert.False(t, (&Gateway{}).SMTPConfigured())
	assert.False(t, (&Gateway{SMTPHost: "h"}).SMTPConfigured())
	assert.False(t, (&Gateway{SMTPFrom: "f@x.com"}).SMTPConfigured())
	assert.True(t, (&Gateway{SMTPHost: "h", SMTPFrom: "f@x.com"}).SMTPConfigured())
}

func TestBuildEmailMessage_TextOnly(t *testing.T) {
	g := &Gateway{SMTPFrom: "gw@example.com"}
	m := &MsgQueueItem{
		From:              "+15555550100",
		To:                "+15555550200",
		Type:              MsgQueueItemType.SMS,
		message:           "hello world",
		ReceivedTimestamp: time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC),
		LogID:             "abc123",
	}
	raw, err := buildEmailMessage(g, m, []string{"ops@example.com"})
	require.NoError(t, err)
	msg := string(raw)
	assert.Contains(t, msg, "From: gw@example.com")
	assert.Contains(t, msg, "To: ops@example.com")
	assert.Contains(t, msg, "Subject: SMS from +15555550100 to +15555550200")
	assert.Contains(t, msg, "Content-Type: text/plain")
	assert.Contains(t, msg, "hello world")
	assert.NotContains(t, msg, "multipart/mixed", "no attachments means no multipart envelope")
}

func TestBuildEmailMessage_WithAttachment(t *testing.T) {
	g := &Gateway{SMTPFrom: "gw@example.com"}
	m := &MsgQueueItem{
		From:    "+15555550100",
		To:      "+15555550200",
		Type:    MsgQueueItemType.MMS,
		message: "see attached",
		LogID:   "def456",
		files: []MsgFile{
			{Filename: "photo.jpg", ContentType: "image/jpeg", Content: []byte("fake-jpeg-bytes")},
			{Filename: "doc.txt", ContentType: "text/plain", Base64Data: base64.StdEncoding.EncodeToString([]byte("doc body"))},
			{Filename: "remote.mp4", ContentType: "video/mp4", MediaURL: "https://cdn.example.com/remote.mp4"},
		},
	}
	raw, err := buildEmailMessage(g, m, []string{"a@b.com", "c@d.org"})
	require.NoError(t, err)
	msg := string(raw)
	assert.Contains(t, msg, "multipart/mixed")
	assert.Contains(t, msg, `filename="photo.jpg"`)
	assert.Contains(t, msg, base64.StdEncoding.EncodeToString([]byte("fake-jpeg-bytes")))
	assert.Contains(t, msg, `filename="doc.txt"`)
	assert.Contains(t, msg, base64.StdEncoding.EncodeToString([]byte("doc body")))
	// URL-only media is linked in the body, not attached
	assert.Contains(t, msg, "https://cdn.example.com/remote.mp4")
	assert.NotContains(t, msg, `filename="remote.mp4"`)
	assert.Contains(t, msg, "To: a@b.com, c@d.org")
}

// --- Fake SMTP server for sendEmail tests ---

// selfSignedCert generates an in-memory self-signed cert valid for 127.0.0.1.
func pemEncode(blockType string, der []byte) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: blockType, Bytes: der})
}

func mustMarshalECKey(t *testing.T, key *ecdsa.PrivateKey) []byte {
	t.Helper()
	der, err := x509.MarshalECPrivateKey(key)
	require.NoError(t, err)
	return der
}

func selfSignedCert(t *testing.T) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	tmpl := x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "localhost"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	require.NoError(t, err)
	cert, err := tls.X509KeyPair(
		pemEncode("CERTIFICATE", der),
		pemEncode("EC PRIVATE KEY", mustMarshalECKey(t, key)),
	)
	require.NoError(t, err)
	return cert
}

// fakeSMTPSession runs one SMTP session (EHLO → [STARTTLS → EHLO] → MAIL →
// RCPT → DATA → QUIT) on the accepted connection and returns the DATA payload.
// When startTLS is false the session stays plaintext.
func fakeSMTPSession(ln net.Listener, cert tls.Certificate, startTLS bool) ([]byte, error) {
	conn, err := ln.Accept()
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	reader := bufio.NewReader(conn)
	writer := bufio.NewWriter(conn)
	write := func(s string) error {
		if _, err := writer.WriteString(s + "\r\n"); err != nil {
			return err
		}
		return writer.Flush()
	}
	readCmd := func() (string, error) {
		line, err := reader.ReadString('\n')
		return strings.TrimRight(line, "\r\n"), err
	}

	if err := write("220 fake-smtp ready"); err != nil {
		return nil, err
	}
	ehlo, err := readCmd()
	if err != nil || !strings.HasPrefix(strings.ToUpper(ehlo), "EHLO") {
		return nil, fmt.Errorf("expected EHLO, got %q (%v)", ehlo, err)
	}
	if startTLS {
		if err := write("250-localhost\r\n250-STARTTLS\r\n250 AUTH PLAIN"); err != nil {
			return nil, err
		}
	} else {
		if err := write("250 localhost"); err != nil {
			return nil, err
		}
	}

	if startTLS {
		st, err := readCmd()
		if err != nil || strings.ToUpper(st) != "STARTTLS" {
			return nil, fmt.Errorf("expected STARTTLS, got %q (%v)", st, err)
		}
		if err := write("220 Go ahead"); err != nil {
			return nil, err
		}

		tlsConn := tls.Server(conn, &tls.Config{Certificates: []tls.Certificate{cert}})
		if err := tlsConn.Handshake(); err != nil {
			return nil, err
		}

		reader = bufio.NewReader(tlsConn)
		writer = bufio.NewWriter(tlsConn)

		ehlo, err = readCmd()
		if err != nil || !strings.HasPrefix(strings.ToUpper(ehlo), "EHLO") {
			return nil, fmt.Errorf("expected EHLO after TLS, got %q (%v)", ehlo, err)
		}
		if err := write("250 localhost"); err != nil {
			return nil, err
		}
	}

	mail, err := readCmd()
	if err != nil || !strings.HasPrefix(strings.ToUpper(mail), "MAIL FROM") {
		return nil, fmt.Errorf("expected MAIL FROM, got %q (%v)", mail, err)
	}
	if err := write("250 OK"); err != nil {
		return nil, err
	}
	rcpt, err := readCmd()
	if err != nil || !strings.HasPrefix(strings.ToUpper(rcpt), "RCPT TO") {
		return nil, fmt.Errorf("expected RCPT TO, got %q (%v)", rcpt, err)
	}
	if err := write("250 OK"); err != nil {
		return nil, err
	}
	dataCmd, err := readCmd()
	if err != nil || strings.ToUpper(dataCmd) != "DATA" {
		return nil, fmt.Errorf("expected DATA, got %q (%v)", dataCmd, err)
	}
	if err := write("354 End data with <CR><LF>.<CR><LF>"); err != nil {
		return nil, err
	}

	var data strings.Builder
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return nil, err
		}
		if line == ".\r\n" {
			break
		}
		data.WriteString(line)
	}
	if err := write("250 Queued"); err != nil {
		return nil, err
	}
	if _, err := readCmd(); err != nil { // QUIT
		return nil, err
	}
	if err := write("221 Bye"); err != nil {
		return nil, err
	}
	return []byte(data.String()), nil
}

// runFakeSMTP stands up a one-shot fake SMTP server on 127.0.0.1 and returns
// the gateway pointed at it plus a channel that receives the session result.
func runFakeSMTP(t *testing.T, startTLS bool) (*Gateway, <-chan struct {
	data []byte
	err  error
}) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { ln.Close() })

	host, portStr, err := net.SplitHostPort(ln.Addr().String())
	require.NoError(t, err)
	port, err := strconv.Atoi(portStr)
	require.NoError(t, err)

	done := make(chan struct {
		data []byte
		err  error
	}, 1)
	go func() {
		data, err := fakeSMTPSession(ln, selfSignedCert(t), startTLS)
		done <- struct {
			data []byte
			err  error
		}{data, err}
	}()

	g := &Gateway{
		SMTPHost:            host,
		SMTPPort:            port,
		SMTPFrom:            "gw@example.com",
		SMTPInsecureSkipTLS: true, // self-signed test cert
	}
	if startTLS {
		g.SMTPEncryption = "starttls"
	} else {
		g.SMTPEncryption = "none"
	}
	return g, done
}

func TestSendEmail_StartTLS(t *testing.T) {
	g, done := runFakeSMTP(t, true)

	err := sendEmail(g, []string{"ops@example.com"}, []byte("Subject: test\r\n\r\nbody"))
	require.NoError(t, err)

	select {
	case res := <-done:
		require.NoError(t, res.err)
		assert.Contains(t, string(res.data), "Subject: test")
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for fake SMTP session")
	}
}

func TestSendEmail_Plaintext(t *testing.T) {
	g, done := runFakeSMTP(t, false)

	err := sendEmail(g, []string{"ops@example.com"}, []byte("Subject: plain\r\n\r\nbody"))
	require.NoError(t, err, "encryption=none must deliver without STARTTLS")

	select {
	case res := <-done:
		require.NoError(t, res.err)
		assert.Contains(t, string(res.data), "Subject: plain")
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for fake SMTP session")
	}
}

func TestSendEmail_ConnectionRefused(t *testing.T) {
	g := &Gateway{SMTPHost: "127.0.0.1", SMTPPort: 1, SMTPFrom: "gw@example.com", SMTPEncryption: "starttls"}
	err := sendEmail(g, []string{"ops@example.com"}, []byte("x"))
	assert.Error(t, err, "delivery failure must surface an error (logged by caller)")
}

func TestParseSMTPEncryption(t *testing.T) {
	t.Run("default is starttls", func(t *testing.T) {
		assert.Equal(t, "starttls", parseSMTPEncryption())
	})
	t.Run("tls mode explicit", func(t *testing.T) {
		t.Setenv("SMTP_TLS_MODE", "none")
		assert.Equal(t, "none", parseSMTPEncryption())
		t.Setenv("SMTP_TLS_MODE", "TLS")
		assert.Equal(t, "tls", parseSMTPEncryption())
	})
	t.Run("tls mode wins over legacy flag", func(t *testing.T) {
		t.Setenv("SMTP_TLS_MODE", "none")
		t.Setenv("SMTP_STARTTLS", "true")
		assert.Equal(t, "none", parseSMTPEncryption())
	})
	t.Run("legacy false maps to tls", func(t *testing.T) {
		t.Setenv("SMTP_STARTTLS", "false")
		assert.Equal(t, "tls", parseSMTPEncryption())
	})
	t.Run("legacy true maps to starttls", func(t *testing.T) {
		t.Setenv("SMTP_STARTTLS", "true")
		assert.Equal(t, "starttls", parseSMTPEncryption())
	})
}
