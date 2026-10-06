// Package smtptest provides an in-process SMTP server for tests. It records
// every accepted message with its envelope, supports STARTTLS, implicit TLS
// and AUTH PLAIN/LOGIN, and can inject failure replies or stall at any stage
// of a session to exercise client error handling.
//
//	srv, err := smtptest.NewServer(smtptest.Options{})
//	if err != nil { t.Fatal(err) }
//	defer srv.Close()
//	// point an SMTP client at srv.Host(), srv.Port()
//	msgs := srv.Messages()
package smtptest

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/textproto"
	"strconv"
	"strings"
	"sync"
)

// Stage identifies the point in an SMTP session where a Failure applies.
type Stage string

// Session stages.
const (
	StageConnect Stage = "CONNECT" // greeting
	StageHelo    Stage = "EHLO"    // EHLO/HELO reply
	StageAuth    Stage = "AUTH"    // AUTH result
	StageMail    Stage = "MAIL"    // MAIL FROM reply
	StageRcpt    Stage = "RCPT"    // first RCPT TO reply
	StageData    Stage = "DATA"    // DATA command reply (before the message)
	StageMessage Stage = "MESSAGE" // reply after the message is transferred
)

// Failure is an injected reply. Code and Message form the SMTP reply
// ("450", "4.7.0 rate limited"). With Stall set the server stops responding
// at the stage instead, until the client disconnects or the server closes.
type Failure struct {
	Stage   Stage
	Code    int
	Message string
	Stall   bool
}

// Options configures a Server.
type Options struct {
	// TLSConfig enables STARTTLS, or TLS on connect when ImplicitTLS is set.
	TLSConfig *tls.Config
	// ImplicitTLS serves TLS from the first byte (SMTPS, port 465 style).
	ImplicitTLS bool
	// Username and Password, when set, advertise AUTH PLAIN LOGIN and
	// require authentication before MAIL.
	Username string
	Password string
	// SMTPUTF8 advertises the SMTPUTF8 extension.
	SMTPUTF8 bool
	// Hostname is used in the greeting (default "smtptest.local").
	Hostname string
}

// Envelope is an accepted message.
type Envelope struct {
	// From is the MAIL FROM address.
	From string
	// To holds the RCPT TO addresses.
	To []string
	// Data is the message as transferred, with CRLF line endings and dot
	// stuffing removed.
	Data []byte
	// TLS reports whether the session was encrypted.
	TLS bool
	// AuthUser is the authenticated username, if any.
	AuthUser string
	// SMTPUTF8 reports whether MAIL FROM carried the SMTPUTF8 parameter.
	SMTPUTF8 bool
}

// Server is an in-process SMTP server listening on 127.0.0.1.
type Server struct {
	opts Options
	ln   net.Listener

	mu       sync.Mutex
	messages []Envelope
	failures []Failure
	conns    map[net.Conn]struct{}
	closed   bool
	wg       sync.WaitGroup
}

// NewServer starts a server on a random local port.
func NewServer(opts Options) (*Server, error) {
	if opts.ImplicitTLS && opts.TLSConfig == nil {
		return nil, errors.New("smtptest: ImplicitTLS requires TLSConfig")
	}
	if opts.Hostname == "" {
		opts.Hostname = "smtptest.local"
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("smtptest: listen: %w", err)
	}
	if opts.ImplicitTLS {
		ln = tls.NewListener(ln, opts.TLSConfig)
	}
	s := &Server{opts: opts, ln: ln, conns: map[net.Conn]struct{}{}}
	s.wg.Add(1)
	go s.serve()
	return s, nil
}

// Addr returns the listen address ("127.0.0.1:port").
func (s *Server) Addr() string { return s.ln.Addr().String() }

// Host returns the listen host.
func (s *Server) Host() string { return "127.0.0.1" }

// Port returns the listen port.
func (s *Server) Port() int { return s.ln.Addr().(*net.TCPAddr).Port }

// Close stops the server and closes open sessions.
func (s *Server) Close() error {
	s.mu.Lock()
	s.closed = true
	for c := range s.conns {
		if err := c.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			s.mu.Unlock()
			return fmt.Errorf("smtptest: close connection: %w", err)
		}
	}
	s.mu.Unlock()
	err := s.ln.Close()
	s.wg.Wait()
	if err != nil && !errors.Is(err, net.ErrClosed) {
		return fmt.Errorf("smtptest: close listener: %w", err)
	}
	return nil
}

// Messages returns copies of the accepted messages in order.
func (s *Server) Messages() []Envelope {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Envelope, len(s.messages))
	for i, m := range s.messages {
		m.To = append([]string(nil), m.To...)
		m.Data = bytes.Clone(m.Data)
		out[i] = m
	}
	return out
}

// Reset discards accepted messages and pending failures.
func (s *Server) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.messages = nil
	s.failures = nil
}

// FailNext queues a failure. It fires the next time any session reaches its
// stage and is then discarded.
func (s *Server) FailNext(f Failure) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failures = append(s.failures, f)
}

func (s *Server) takeFailure(stage Stage) (Failure, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, f := range s.failures {
		if f.Stage == stage {
			s.failures = append(s.failures[:i], s.failures[i+1:]...)
			return f, true
		}
	}
	return Failure{}, false
}

func (s *Server) serve() {
	defer s.wg.Done()
	for {
		c, err := s.ln.Accept()
		if err != nil {
			return
		}
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			closeQuietly(c)
			return
		}
		s.conns[c] = struct{}{}
		s.mu.Unlock()
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			sess := &session{srv: s, conn: c, tls: s.opts.ImplicitTLS}
			sess.run()
			s.mu.Lock()
			delete(s.conns, sess.conn)
			delete(s.conns, c)
			s.mu.Unlock()
			closeQuietly(sess.conn)
		}()
	}
}

// closeQuietly closes a connection the server is abandoning. The error is
// irrelevant: the peer may already have closed it and no data is pending.
func closeQuietly(c io.Closer) {
	if err := c.Close(); err != nil {
		return
	}
}

type session struct {
	srv  *Server
	conn net.Conn
	tp   *textproto.Conn
	tls  bool

	helo     bool
	authUser string
	from     string
	utf8     bool
	rcpts    []string
}

var errQuit = errors.New("quit")

func (ss *session) reply(code int, msg string) error {
	return ss.tp.PrintfLine("%d %s", code, msg)
}

// fail applies a queued failure for stage. It reports whether a failure was
// applied; a stalled session never returns until the connection closes.
func (ss *session) fail(stage Stage) (bool, error) {
	f, ok := ss.srv.takeFailure(stage)
	if !ok {
		return false, nil
	}
	if f.Stall {
		if _, err := io.Copy(io.Discard, ss.conn); err != nil {
			return true, err
		}
		return true, errQuit
	}
	return true, ss.reply(f.Code, f.Message)
}

func (ss *session) run() {
	ss.tp = textproto.NewConn(ss.conn)
	if applied, err := ss.fail(StageConnect); applied || err != nil {
		return
	}
	if err := ss.reply(220, ss.srv.opts.Hostname+" ESMTP smtptest"); err != nil {
		return
	}
	for {
		line, err := ss.tp.ReadLine()
		if err != nil {
			return
		}
		verb, arg, _ := strings.Cut(line, " ")
		if err := ss.handle(strings.ToUpper(verb), arg); err != nil {
			return
		}
	}
}

func (ss *session) handle(verb, arg string) error {
	switch verb {
	case "EHLO", "HELO":
		return ss.ehlo(verb)
	case "STARTTLS":
		return ss.startTLS()
	case "AUTH":
		return ss.auth(arg)
	case "MAIL":
		return ss.mail(arg)
	case "RCPT":
		return ss.rcpt(arg)
	case "DATA":
		return ss.data()
	case "RSET":
		ss.from, ss.rcpts, ss.utf8 = "", nil, false
		return ss.reply(250, "2.0.0 OK")
	case "NOOP":
		return ss.reply(250, "2.0.0 OK")
	case "QUIT":
		if err := ss.reply(221, "2.0.0 Bye"); err != nil {
			return err
		}
		return errQuit
	default:
		return ss.reply(502, "5.5.2 Command not recognized")
	}
}

func (ss *session) ehlo(verb string) error {
	if applied, err := ss.fail(StageHelo); applied || err != nil {
		return err
	}
	ss.helo = true
	if verb == "HELO" {
		return ss.reply(250, ss.srv.opts.Hostname)
	}
	lines := []string{ss.srv.opts.Hostname, "8BITMIME", "PIPELINING"}
	if ss.srv.opts.SMTPUTF8 {
		lines = append(lines, "SMTPUTF8")
	}
	if ss.srv.opts.TLSConfig != nil && !ss.tls {
		lines = append(lines, "STARTTLS")
	}
	if ss.srv.opts.Username != "" {
		lines = append(lines, "AUTH PLAIN LOGIN")
	}
	for i, l := range lines {
		sep := "-"
		if i == len(lines)-1 {
			sep = " "
		}
		if err := ss.tp.PrintfLine("250%s%s", sep, l); err != nil {
			return err
		}
	}
	return nil
}

func (ss *session) startTLS() error {
	if ss.srv.opts.TLSConfig == nil || ss.tls {
		return ss.reply(454, "4.7.0 TLS not available")
	}
	if err := ss.reply(220, "2.0.0 Ready to start TLS"); err != nil {
		return err
	}
	tc := tls.Server(ss.conn, ss.srv.opts.TLSConfig)
	if err := tc.Handshake(); err != nil {
		return err
	}
	ss.srv.mu.Lock()
	ss.srv.conns[tc] = struct{}{}
	ss.srv.mu.Unlock()
	ss.conn = tc
	ss.tp = textproto.NewConn(tc)
	ss.tls = true
	ss.helo, ss.authUser, ss.from, ss.rcpts = false, "", "", nil
	return nil
}

func (ss *session) readAuthLine(prompt string) (string, error) {
	if err := ss.reply(334, prompt); err != nil {
		return "", err
	}
	return ss.tp.ReadLine()
}

func (ss *session) auth(arg string) error {
	if ss.srv.opts.Username == "" || !ss.helo {
		return ss.reply(503, "5.5.1 AUTH not available")
	}
	mech, initial, _ := strings.Cut(arg, " ")
	var user, pass string
	switch strings.ToUpper(mech) {
	case "PLAIN":
		resp := initial
		if resp == "" {
			var err error
			if resp, err = ss.readAuthLine(""); err != nil {
				return err
			}
		}
		b, err := base64.StdEncoding.DecodeString(resp)
		if err != nil {
			return ss.reply(501, "5.5.2 Invalid base64")
		}
		parts := strings.Split(string(b), "\x00")
		if len(parts) != 3 {
			return ss.reply(501, "5.5.2 Invalid PLAIN response")
		}
		user, pass = parts[1], parts[2]
	case "LOGIN":
		u, err := ss.readAuthLine(base64.StdEncoding.EncodeToString([]byte("Username:")))
		if err != nil {
			return err
		}
		p, err := ss.readAuthLine(base64.StdEncoding.EncodeToString([]byte("Password:")))
		if err != nil {
			return err
		}
		ub, err1 := base64.StdEncoding.DecodeString(u)
		pb, err2 := base64.StdEncoding.DecodeString(p)
		if err1 != nil || err2 != nil {
			return ss.reply(501, "5.5.2 Invalid base64")
		}
		user, pass = string(ub), string(pb)
	default:
		return ss.reply(504, "5.5.4 Unrecognized authentication type")
	}
	if applied, err := ss.fail(StageAuth); applied || err != nil {
		return err
	}
	if user != ss.srv.opts.Username || pass != ss.srv.opts.Password {
		return ss.reply(535, "5.7.8 Authentication credentials invalid")
	}
	ss.authUser = user
	return ss.reply(235, "2.7.0 Authentication successful")
}

func parsePath(arg, prefix string) (addr string, params []string, ok bool) {
	if len(arg) < len(prefix) || !strings.EqualFold(arg[:len(prefix)], prefix) {
		return "", nil, false
	}
	rest := strings.TrimSpace(arg[len(prefix):])
	if !strings.HasPrefix(rest, "<") {
		return "", nil, false
	}
	end := strings.IndexByte(rest, '>')
	if end < 0 {
		return "", nil, false
	}
	return rest[1:end], strings.Fields(rest[end+1:]), true
}

func (ss *session) mail(arg string) error {
	if !ss.helo {
		return ss.reply(503, "5.5.1 Send EHLO first")
	}
	if ss.srv.opts.Username != "" && ss.authUser == "" {
		return ss.reply(530, "5.7.0 Authentication required")
	}
	addr, params, ok := parsePath(arg, "FROM:")
	if !ok {
		return ss.reply(501, "5.5.4 Syntax: MAIL FROM:<address>")
	}
	if applied, err := ss.fail(StageMail); applied || err != nil {
		return err
	}
	ss.utf8 = false
	for _, p := range params {
		if strings.EqualFold(p, "SMTPUTF8") {
			ss.utf8 = true
		}
	}
	if !isASCII(addr) && !ss.utf8 {
		return ss.reply(553, "5.6.7 SMTPUTF8 required for non-ASCII address")
	}
	ss.from, ss.rcpts = addr, nil
	return ss.reply(250, "2.1.0 OK")
}

func (ss *session) rcpt(arg string) error {
	if ss.from == "" {
		return ss.reply(503, "5.5.1 Need MAIL first")
	}
	addr, _, ok := parsePath(arg, "TO:")
	if !ok {
		return ss.reply(501, "5.5.4 Syntax: RCPT TO:<address>")
	}
	if len(ss.rcpts) == 0 {
		if applied, err := ss.fail(StageRcpt); applied || err != nil {
			return err
		}
	}
	if !isASCII(addr) && !ss.utf8 {
		return ss.reply(553, "5.6.7 SMTPUTF8 required for non-ASCII address")
	}
	ss.rcpts = append(ss.rcpts, addr)
	return ss.reply(250, "2.1.5 OK")
}

func (ss *session) data() error {
	if len(ss.rcpts) == 0 {
		return ss.reply(503, "5.5.1 Need RCPT first")
	}
	if applied, err := ss.fail(StageData); applied || err != nil {
		return err
	}
	if err := ss.reply(354, "End data with <CR><LF>.<CR><LF>"); err != nil {
		return err
	}
	raw, err := ss.tp.ReadDotBytes()
	if err != nil {
		return err
	}
	if applied, err := ss.fail(StageMessage); applied || err != nil {
		return err
	}
	env := Envelope{
		From:     ss.from,
		To:       ss.rcpts,
		Data:     toCRLF(raw),
		TLS:      ss.tls,
		AuthUser: ss.authUser,
		SMTPUTF8: ss.utf8,
	}
	ss.srv.mu.Lock()
	ss.srv.messages = append(ss.srv.messages, env)
	n := len(ss.srv.messages)
	ss.srv.mu.Unlock()
	ss.from, ss.rcpts, ss.utf8 = "", nil, false
	return ss.reply(250, "2.0.0 OK queued as "+strconv.Itoa(n))
}

// toCRLF restores CRLF line endings, which textproto's dot reader converts
// to LF.
func toCRLF(b []byte) []byte {
	var out bytes.Buffer
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 0, 64*1024), len(b)+1)
	for sc.Scan() {
		out.Write(sc.Bytes())
		out.WriteString("\r\n")
	}
	return out.Bytes()
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}
