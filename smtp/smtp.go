// Package smtp provides an omnimail.Sender that delivers through an SMTP
// server (a relay such as Postfix, a provider's SMTP endpoint, or a local
// development server such as Mailpit).
//
// It supports STARTTLS (default, required) and implicit TLS (SMTPS), AUTH
// PLAIN and LOGIN, dial and session timeouts, and context cancellation. Each
// Send uses its own connection, so a Sender is safe for concurrent use. Only
// the standard library is used.
//
//	s, err := smtp.New(smtp.Config{
//	    Host:     "email-smtp.us-east-1.amazonaws.com",
//	    Username: user,
//	    Password: pass,
//	})
package smtp

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	netsmtp "net/smtp"
	"strconv"
	"strings"
	"time"

	"github.com/plexusone/omnimail"
)

// ProviderName is reported in SendResult.Provider and Error.Provider.
const ProviderName = "smtp"

// Security selects how the connection is encrypted.
type Security int

const (
	// StartTLS connects in plaintext and upgrades with STARTTLS. The upgrade
	// is required: if the server does not offer it, Send fails. Default port
	// 587.
	StartTLS Security = iota
	// ImplicitTLS uses TLS from the first byte (SMTPS). Default port 465.
	ImplicitTLS
	// Plaintext never encrypts. Use only for local relays and development
	// servers. Default port 25.
	Plaintext
)

// String returns the name of the security mode.
func (s Security) String() string {
	switch s {
	case StartTLS:
		return "starttls"
	case ImplicitTLS:
		return "tls"
	case Plaintext:
		return "plaintext"
	default:
		return "invalid"
	}
}

// AuthMechanism selects the SMTP AUTH mechanism.
type AuthMechanism string

const (
	// AuthAuto picks PLAIN when the server offers it, otherwise LOGIN.
	AuthAuto AuthMechanism = ""
	// AuthPlain uses AUTH PLAIN (RFC 4616).
	AuthPlain AuthMechanism = "PLAIN"
	// AuthLogin uses AUTH LOGIN.
	AuthLogin AuthMechanism = "LOGIN"
)

// Dialer opens network connections. *net.Dialer implements it.
type Dialer interface {
	DialContext(ctx context.Context, network, address string) (net.Conn, error)
}

// Default timeouts.
const (
	DefaultDialTimeout = 10 * time.Second
	DefaultTimeout     = 60 * time.Second
)

// Config configures a Sender.
type Config struct {
	// Host is the server host name (required).
	Host string
	// Port defaults to 587 (StartTLS), 465 (ImplicitTLS) or 25 (Plaintext).
	Port int
	// Security defaults to StartTLS.
	Security Security
	// Username and Password enable SMTP AUTH when Username is set.
	Username string
	Password string
	// Auth selects the mechanism (default AuthAuto).
	Auth AuthMechanism
	// AllowInsecureAuth permits sending credentials over an unencrypted
	// connection. Off by default; enable only for local test servers.
	AllowInsecureAuth bool
	// TLSConfig customizes TLS. ServerName defaults to Host and MinVersion
	// to TLS 1.2.
	TLSConfig *tls.Config
	// LocalName is the EHLO name (default "localhost").
	LocalName string
	// DialTimeout bounds connecting and the TLS handshake (default 10s).
	DialTimeout time.Duration
	// Timeout bounds a whole send session (default 60s). A shorter context
	// deadline takes precedence.
	Timeout time.Duration
	// MessageIDDomain is the right-hand side of generated Message-IDs
	// (default: the From domain).
	MessageIDDomain string
	// Dialer overrides the network dialer (default *net.Dialer).
	Dialer Dialer
}

// Sender delivers messages over SMTP. It is safe for concurrent use.
type Sender struct {
	cfg  Config
	addr string
}

var _ omnimail.Sender = (*Sender)(nil)

// New validates cfg, applies defaults and returns a Sender. It does not
// connect.
func New(cfg Config) (*Sender, error) {
	if cfg.Host == "" {
		return nil, errors.New("smtp: Host is required")
	}
	if cfg.Security < StartTLS || cfg.Security > Plaintext {
		return nil, fmt.Errorf("smtp: invalid Security %d", int(cfg.Security))
	}
	switch cfg.Auth {
	case AuthAuto, AuthPlain, AuthLogin:
	default:
		return nil, fmt.Errorf("smtp: unsupported auth mechanism %q", cfg.Auth)
	}
	if cfg.Port == 0 {
		cfg.Port = map[Security]int{StartTLS: 587, ImplicitTLS: 465, Plaintext: 25}[cfg.Security]
	}
	if cfg.Port < 1 || cfg.Port > 65535 {
		return nil, fmt.Errorf("smtp: invalid Port %d", cfg.Port)
	}
	if cfg.LocalName == "" {
		cfg.LocalName = "localhost"
	}
	if cfg.DialTimeout <= 0 {
		cfg.DialTimeout = DefaultDialTimeout
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = DefaultTimeout
	}
	if cfg.TLSConfig == nil {
		cfg.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	} else {
		cfg.TLSConfig = cfg.TLSConfig.Clone()
	}
	if cfg.TLSConfig.ServerName == "" {
		cfg.TLSConfig.ServerName = cfg.Host
	}
	if cfg.Dialer == nil {
		cfg.Dialer = &net.Dialer{}
	}
	return &Sender{cfg: cfg, addr: net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))}, nil
}

// Send validates msg, assembles it with omnimail.BuildMIME and delivers it in
// one SMTP session. Failures are returned as *omnimail.Error classified from
// the SMTP reply code; cancellation of ctx returns an error matching
// ctx.Err().
func (s *Sender) Send(ctx context.Context, msg *omnimail.Message) (*omnimail.SendResult, error) {
	if err := omnimail.ValidateMessage(msg); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, ctxError(err)
	}
	built, err := omnimail.BuildMIME(msg, omnimail.BuildOptions{MessageIDDomain: s.cfg.MessageIDDomain})
	if err != nil {
		return nil, err
	}
	if err := s.deliver(ctx, msg, built.Data); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxError(ctxErr)
		}
		return nil, err
	}
	return &omnimail.SendResult{MessageID: built.MessageID, Provider: ProviderName}, nil
}

func (s *Sender) deliver(ctx context.Context, msg *omnimail.Message, data []byte) error {
	conn, err := s.dial(ctx)
	if err != nil {
		return err
	}
	if err := conn.SetDeadline(time.Now().Add(s.cfg.Timeout)); err != nil {
		closeConn(conn)
		return classify("connect", err)
	}
	// Unblock any in-flight read or write as soon as ctx is done (canceled
	// or past its deadline). By then ctx.Err() is set, so Send reports the
	// context error rather than an I/O timeout.
	stop := context.AfterFunc(ctx, func() {
		if err := conn.SetDeadline(time.Unix(1, 0)); err != nil {
			closeConn(conn)
		}
	})
	defer stop()

	c, err := netsmtp.NewClient(conn, s.cfg.Host)
	if err != nil {
		closeConn(conn)
		return classify("connect", err)
	}
	defer closeConn(c)

	if err := c.Hello(s.cfg.LocalName); err != nil {
		return classify("ehlo", err)
	}
	if s.cfg.Security == StartTLS {
		if ok, _ := c.Extension("STARTTLS"); !ok {
			return &omnimail.Error{Kind: omnimail.KindUnknown, Provider: ProviderName,
				Message: "server does not offer STARTTLS; use ImplicitTLS or Plaintext explicitly"}
		}
		if err := c.StartTLS(s.cfg.TLSConfig); err != nil {
			return classify("starttls", err)
		}
	}
	if s.cfg.Username != "" {
		if err := s.auth(c); err != nil {
			return err
		}
	}
	if needsUTF8(msg) {
		if ok, _ := c.Extension("SMTPUTF8"); !ok {
			return &omnimail.Error{Kind: omnimail.KindInvalidAddress, Provider: ProviderName,
				Message: "message has internationalized addresses but the server does not support SMTPUTF8"}
		}
	}
	if err := c.Mail(msg.From.Email); err != nil {
		return classify("mail", err)
	}
	for _, r := range msg.Recipients() {
		if err := c.Rcpt(r.Email); err != nil {
			return classify("rcpt", err)
		}
	}
	w, err := c.Data()
	if err != nil {
		return classify("data", err)
	}
	if _, err := w.Write(data); err != nil {
		return classify("message", err)
	}
	if err := w.Close(); err != nil {
		return classify("message", err)
	}
	// The message is accepted once DATA completes; a failed QUIT does not
	// change that, so its error is deliberately not reported.
	if err := c.Quit(); err != nil {
		return nil
	}
	return nil
}

func (s *Sender) dial(ctx context.Context) (net.Conn, error) {
	dctx, cancel := context.WithTimeout(ctx, s.cfg.DialTimeout)
	defer cancel()
	conn, err := s.cfg.Dialer.DialContext(dctx, "tcp", s.addr)
	if err != nil {
		return nil, classify("connect", err)
	}
	if s.cfg.Security != ImplicitTLS {
		return conn, nil
	}
	tc := tls.Client(conn, s.cfg.TLSConfig)
	if err := tc.HandshakeContext(dctx); err != nil {
		closeConn(conn)
		return nil, classify("tls", err)
	}
	return tc, nil
}

func (s *Sender) auth(c *netsmtp.Client) error {
	ok, mechs := c.Extension("AUTH")
	if !ok {
		return &omnimail.Error{Kind: omnimail.KindAuth, Provider: ProviderName,
			Message: "credentials configured but the server does not offer AUTH"}
	}
	mech := s.cfg.Auth
	if mech == AuthAuto {
		offered := strings.Fields(strings.ToUpper(mechs))
		switch {
		case contains(offered, string(AuthPlain)):
			mech = AuthPlain
		case contains(offered, string(AuthLogin)):
			mech = AuthLogin
		default:
			return &omnimail.Error{Kind: omnimail.KindAuth, Provider: ProviderName,
				Message: "server offers no supported AUTH mechanism (PLAIN, LOGIN): " + mechs}
		}
	}
	var a netsmtp.Auth
	if mech == AuthPlain {
		a = &plainAuth{user: s.cfg.Username, pass: s.cfg.Password, insecure: s.cfg.AllowInsecureAuth}
	} else {
		a = &loginAuth{user: s.cfg.Username, pass: s.cfg.Password, insecure: s.cfg.AllowInsecureAuth}
	}
	if err := c.Auth(a); err != nil {
		var ie *insecureError
		if errors.As(err, &ie) {
			return &omnimail.Error{Kind: omnimail.KindAuth, Provider: ProviderName, Message: ie.Error()}
		}
		return classify("auth", err)
	}
	return nil
}

func needsUTF8(msg *omnimail.Message) bool {
	if !msg.From.IsASCII() {
		return true
	}
	for _, r := range msg.Recipients() {
		if !r.IsASCII() {
			return true
		}
	}
	for _, r := range msg.ReplyTo {
		if !r.IsASCII() {
			return true
		}
	}
	return false
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

type closer interface{ Close() error }

// closeConn closes a connection being abandoned after an error or after
// QUIT. Its error carries no information the caller can act on: the session
// outcome has already been determined.
func closeConn(c closer) {
	if err := c.Close(); err != nil {
		return
	}
}

func ctxError(err error) error {
	return &omnimail.Error{Kind: omnimail.KindTransient, Provider: ProviderName, Message: "send canceled", Err: err}
}
