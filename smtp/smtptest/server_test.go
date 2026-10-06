package smtptest

import (
	"crypto/tls"
	"encoding/base64"
	"net"
	"net/textproto"
	"strings"
	"testing"
)

func start(t *testing.T, opts Options) *Server {
	t.Helper()
	s, err := NewServer(opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return s
}

type client struct {
	t  *testing.T
	tp *textproto.Conn
	c  net.Conn
}

func dial(t *testing.T, s *Server) *client {
	t.Helper()
	c, err := net.Dial("tcp", s.Addr())
	if err != nil {
		t.Fatal(err)
	}
	cl := &client{t: t, tp: textproto.NewConn(c), c: c}
	t.Cleanup(func() {
		if err := c.Close(); err != nil && !strings.Contains(err.Error(), "closed") {
			t.Errorf("close client: %v", err)
		}
	})
	cl.expect(220)
	return cl
}

// cmd sends a line and checks the reply code.
func (c *client) cmd(code int, line string) string {
	c.t.Helper()
	if err := c.tp.PrintfLine("%s", line); err != nil {
		c.t.Fatal(err)
	}
	return c.expect(code)
}

func (c *client) expect(code int) string {
	c.t.Helper()
	got, msg, err := c.tp.ReadResponse(code)
	if err != nil {
		c.t.Fatalf("expected %d, got %d %q: %v", code, got, msg, err)
	}
	return msg
}

func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

func TestSessionCommands(t *testing.T) {
	s := start(t, Options{SMTPUTF8: true})
	c := dial(t, s)
	c.cmd(503, "MAIL FROM:<a@example.com>")
	c.cmd(250, "HELO client")
	c.cmd(503, "RCPT TO:<b@example.com>")
	c.cmd(501, "MAIL FROM:a@example.com")
	c.cmd(501, "MAIL FROM:<a@example.com")
	c.cmd(503, "AUTH PLAIN")
	c.cmd(454, "STARTTLS")
	ehlo := c.cmd(250, "EHLO client")
	if !strings.Contains(ehlo, "SMTPUTF8") || strings.Contains(ehlo, "STARTTLS") || strings.Contains(ehlo, "AUTH") {
		t.Fatalf("EHLO = %q", ehlo)
	}
	c.cmd(250, "NOOP")
	c.cmd(502, "VRFY a")
	c.cmd(553, "MAIL FROM:<josé@example.com>")
	c.cmd(250, "MAIL FROM:<a@example.com>")
	c.cmd(503, "DATA")
	c.cmd(501, "RCPT TO:b@example.com")
	c.cmd(553, "RCPT TO:<用户@例子.广告>")
	c.cmd(250, "RSET")
	c.cmd(250, "MAIL FROM:<josé@example.com> SMTPUTF8")
	c.cmd(250, "RCPT TO:<用户@例子.广告>")
	c.cmd(250, "RCPT TO:<c@example.com>")
	c.cmd(354, "DATA")
	c.cmd(250, "Subject: hi\r\n\r\n..dotted\r\nbody\r\n.")
	c.cmd(221, "QUIT")

	msgs := s.Messages()
	if len(msgs) != 1 {
		t.Fatalf("messages = %d", len(msgs))
	}
	m := msgs[0]
	if m.From != "josé@example.com" || len(m.To) != 2 || !m.SMTPUTF8 || m.TLS {
		t.Fatalf("envelope = %+v", m)
	}
	if string(m.Data) != "Subject: hi\r\n\r\n.dotted\r\nbody\r\n" {
		t.Fatalf("data = %q", m.Data)
	}
	s.Reset()
	if len(s.Messages()) != 0 {
		t.Fatal("Reset")
	}
}

func TestAuth(t *testing.T) {
	s := start(t, Options{Username: "u", Password: "p"})
	c := dial(t, s)
	if !strings.Contains(c.cmd(250, "EHLO x"), "AUTH PLAIN LOGIN") {
		t.Fatal("AUTH not advertised")
	}
	c.cmd(530, "MAIL FROM:<a@example.com>")
	c.cmd(504, "AUTH CRAM-MD5")
	c.cmd(501, "AUTH PLAIN !!!")
	c.cmd(501, "AUTH PLAIN "+b64("nonulls"))
	c.cmd(535, "AUTH PLAIN "+b64("\x00u\x00wrong"))
	c.cmd(334, "AUTH PLAIN")
	c.cmd(235, b64("\x00u\x00p"))

	c2 := dial(t, s)
	c2.cmd(250, "EHLO x")
	c2.cmd(334, "AUTH LOGIN")
	c2.cmd(334, b64("u"))
	c2.cmd(501, "!!!")
	c2.cmd(334, "AUTH LOGIN")
	c2.cmd(334, b64("u"))
	c2.cmd(235, b64("p"))
	c2.cmd(250, "MAIL FROM:<a@example.com>")
}

func TestFailuresAndStall(t *testing.T) {
	s := start(t, Options{})
	s.FailNext(Failure{Stage: StageRcpt, Code: 550, Message: "5.1.1 nope"})
	s.FailNext(Failure{Stage: StageData, Code: 451, Message: "4.3.0 later"})
	s.FailNext(Failure{Stage: StageMessage, Code: 554, Message: "5.7.1 spam"})
	s.FailNext(Failure{Stage: StageMail, Code: 450, Message: "4.7.0 rate"})
	c := dial(t, s)
	c.cmd(250, "EHLO x")
	c.cmd(450, "MAIL FROM:<a@example.com>")
	c.cmd(250, "MAIL FROM:<a@example.com>")
	c.cmd(550, "RCPT TO:<b@example.com>")
	c.cmd(250, "RCPT TO:<b@example.com>")
	c.cmd(451, "DATA")
	c.cmd(354, "DATA")
	c.cmd(554, "x\r\n.")
	if len(s.Messages()) != 0 {
		t.Fatal("rejected message stored")
	}

	s.FailNext(Failure{Stage: StageConnect, Code: 421, Message: "busy"})
	conn, err := net.Dial("tcp", s.Addr())
	if err != nil {
		t.Fatal(err)
	}
	tp := textproto.NewConn(conn)
	if code, _, err := tp.ReadResponse(220); code != 421 || err == nil {
		t.Fatalf("greeting = %d %v", code, err)
	}
	if err := tp.Close(); err != nil {
		t.Fatal(err)
	}

	s.FailNext(Failure{Stage: StageHelo, Stall: true})
	stalled := dial(t, s)
	if err := stalled.tp.PrintfLine("EHLO x"); err != nil {
		t.Fatal(err)
	}
	// Closing the client ends the stalled session.
	if err := stalled.c.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestStartTLSAndImplicitTLS(t *testing.T) {
	serverTLS, clientTLS, err := NewTLSConfig()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewServer(Options{ImplicitTLS: true}); err == nil {
		t.Fatal("ImplicitTLS without TLSConfig should fail")
	}
	s := start(t, Options{TLSConfig: serverTLS})
	c := dial(t, s)
	if !strings.Contains(c.cmd(250, "EHLO x"), "STARTTLS") {
		t.Fatal("STARTTLS not advertised")
	}
	c.cmd(220, "STARTTLS")
	tc := tls.Client(c.c, clientTLS)
	if err := tc.Handshake(); err != nil {
		t.Fatal(err)
	}
	c.tp = textproto.NewConn(tc)
	c.cmd(503, "MAIL FROM:<a@example.com>")
	if strings.Contains(c.cmd(250, "EHLO x"), "STARTTLS") {
		t.Fatal("STARTTLS advertised twice")
	}
	c.cmd(454, "STARTTLS")
	c.cmd(250, "MAIL FROM:<a@example.com>")
	c.cmd(250, "RCPT TO:<b@example.com>")
	c.cmd(354, "DATA")
	c.cmd(250, "x\r\n.")
	if m := s.Messages(); len(m) != 1 || !m[0].TLS {
		t.Fatalf("messages = %+v", m)
	}

	si := start(t, Options{TLSConfig: serverTLS, ImplicitTLS: true, Hostname: "mx.test"})
	conn, err := tls.Dial("tcp", si.Addr(), clientTLS)
	if err != nil {
		t.Fatal(err)
	}
	tp := textproto.NewConn(conn)
	if _, msg, err := tp.ReadResponse(220); err != nil || !strings.HasPrefix(msg, "mx.test") {
		t.Fatalf("greeting %q %v", msg, err)
	}
	if err := tp.Close(); err != nil {
		t.Fatal(err)
	}
	if si.Host() != "127.0.0.1" || si.Port() == 0 {
		t.Fatal("Host/Port")
	}
}
