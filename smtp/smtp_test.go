package smtp_test

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/plexusone/omnimail"
	"github.com/plexusone/omnimail/providertest"
	"github.com/plexusone/omnimail/smtp"
	"github.com/plexusone/omnimail/smtp/smtptest"
)

const (
	testUser = "user"
	testPass = "s3cret"
)

func newServer(t *testing.T, opts smtptest.Options) *smtptest.Server {
	t.Helper()
	srv, err := smtptest.NewServer(opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := srv.Close(); err != nil {
			t.Errorf("close server: %v", err)
		}
	})
	return srv
}

func tlsConfigs(t *testing.T) (server, client *tls.Config) {
	t.Helper()
	server, client, err := smtptest.NewTLSConfig()
	if err != nil {
		t.Fatal(err)
	}
	return server, client
}

func newSender(t *testing.T, cfg smtp.Config) *smtp.Sender {
	t.Helper()
	s, err := smtp.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func testMessage() *omnimail.Message {
	return &omnimail.Message{
		From:    omnimail.Address{Name: "App", Email: "no-reply@example.com"},
		To:      []omnimail.Address{{Email: "to@example.org"}},
		Bcc:     []omnimail.Address{{Email: "bcc@example.org"}},
		Subject: "Verify your email",
		Text:    "Open https://example.com/verify\n.\n..leading dots survive\n",
	}
}

// harness adapts the in-process SMTP server to providertest.
type harness struct {
	srv    *smtptest.Server
	sender *smtp.Sender
}

func (h *harness) Sender() omnimail.Sender { return h.sender }

func (h *harness) Captured(t testing.TB) []*omnimail.Message {
	t.Helper()
	var out []*omnimail.Message
	for _, env := range h.srv.Messages() {
		m, err := providertest.DecodeMIME(env.Data, env.To)
		if err != nil {
			t.Fatalf("decode captured message: %v", err)
		}
		out = append(out, m)
	}
	return out
}

func (h *harness) Reset() { h.srv.Reset() }

func (h *harness) InjectFailure(kind omnimail.Kind) bool {
	f, ok := map[omnimail.Kind]smtptest.Failure{
		omnimail.KindInvalidAddress: {Stage: smtptest.StageRcpt, Code: 550, Message: "5.1.1 User unknown"},
		omnimail.KindRejected:       {Stage: smtptest.StageMessage, Code: 554, Message: "5.7.1 Message rejected"},
		omnimail.KindThrottled:      {Stage: smtptest.StageMail, Code: 450, Message: "4.7.0 Rate limit exceeded, try again later"},
		omnimail.KindTransient:      {Stage: smtptest.StageData, Code: 451, Message: "4.3.0 Local error in processing"},
		omnimail.KindAuth:           {Stage: smtptest.StageAuth, Code: 535, Message: "5.7.8 Authentication credentials invalid"},
	}[kind]
	if ok {
		h.srv.FailNext(f)
	}
	return ok
}

func TestConformanceStartTLS(t *testing.T) {
	serverTLS, clientTLS := tlsConfigs(t)
	srv := newServer(t, smtptest.Options{TLSConfig: serverTLS, Username: testUser, Password: testPass, SMTPUTF8: true})
	sender := newSender(t, smtp.Config{
		Host: srv.Host(), Port: srv.Port(), Username: testUser, Password: testPass, TLSConfig: clientTLS,
	})
	providertest.RunAll(t, providertest.Config{
		Harness:          &harness{srv: srv, sender: sender},
		Provider:         smtp.ProviderName,
		SupportsSMTPUTF8: true,
	})
}

func TestConformanceImplicitTLS(t *testing.T) {
	serverTLS, clientTLS := tlsConfigs(t)
	srv := newServer(t, smtptest.Options{TLSConfig: serverTLS, ImplicitTLS: true, Username: testUser, Password: testPass})
	sender := newSender(t, smtp.Config{
		Host: srv.Host(), Port: srv.Port(), Security: smtp.ImplicitTLS,
		Username: testUser, Password: testPass, Auth: smtp.AuthLogin, TLSConfig: clientTLS,
	})
	providertest.RunAll(t, providertest.Config{Harness: &harness{srv: srv, sender: sender}, Provider: smtp.ProviderName})
}

func TestSendDeliversEnvelope(t *testing.T) {
	serverTLS, clientTLS := tlsConfigs(t)
	srv := newServer(t, smtptest.Options{TLSConfig: serverTLS, Username: testUser, Password: testPass})
	s := newSender(t, smtp.Config{Host: srv.Host(), Port: srv.Port(), Username: testUser, Password: testPass,
		Auth: smtp.AuthPlain, TLSConfig: clientTLS, MessageIDDomain: "mail.example.com"})
	msg := testMessage()
	res, err := s.Send(context.Background(), msg)
	if err != nil {
		t.Fatal(err)
	}
	if res.Provider != "smtp" || !strings.HasSuffix(res.MessageID, "@mail.example.com") {
		t.Fatalf("result = %+v", res)
	}
	msgs := srv.Messages()
	if len(msgs) != 1 {
		t.Fatalf("got %d messages", len(msgs))
	}
	env := msgs[0]
	if !env.TLS || env.AuthUser != testUser || env.From != "no-reply@example.com" {
		t.Fatalf("envelope = %+v", env)
	}
	if strings.Join(env.To, ",") != "to@example.org,bcc@example.org" {
		t.Fatalf("rcpts = %v", env.To)
	}
	if !strings.Contains(string(env.Data), "Message-ID: <"+res.MessageID+">") {
		t.Fatal("Message-ID header does not match result")
	}
	got, err := providertest.DecodeMIME(env.Data, env.To)
	if err != nil {
		t.Fatal(err)
	}
	if providertest.NormalizeBody(got.Text) != providertest.NormalizeBody(msg.Text) {
		t.Fatalf("dot-stuffed body = %q", got.Text)
	}
}

func TestPlaintext(t *testing.T) {
	srv := newServer(t, smtptest.Options{})
	s := newSender(t, smtp.Config{Host: srv.Host(), Port: srv.Port(), Security: smtp.Plaintext})
	if _, err := s.Send(context.Background(), testMessage()); err != nil {
		t.Fatal(err)
	}
	if env := srv.Messages()[0]; env.TLS || env.AuthUser != "" {
		t.Fatalf("envelope = %+v", env)
	}
}

func TestInsecureAuth(t *testing.T) {
	srv := newServer(t, smtptest.Options{Username: testUser, Password: testPass})
	for _, mech := range []smtp.AuthMechanism{smtp.AuthAuto, smtp.AuthPlain, smtp.AuthLogin} {
		s := newSender(t, smtp.Config{Host: srv.Host(), Port: srv.Port(), Security: smtp.Plaintext,
			Username: testUser, Password: testPass, Auth: mech})
		_, err := s.Send(context.Background(), testMessage())
		if omnimail.KindOf(err) != omnimail.KindAuth || !strings.Contains(err.Error(), "unencrypted") {
			t.Fatalf("%q: err = %v, want auth error refusing plaintext credentials", mech, err)
		}
		s = newSender(t, smtp.Config{Host: srv.Host(), Port: srv.Port(), Security: smtp.Plaintext,
			Username: testUser, Password: testPass, Auth: mech, AllowInsecureAuth: true})
		if _, err := s.Send(context.Background(), testMessage()); err != nil {
			t.Fatalf("%q with AllowInsecureAuth: %v", mech, err)
		}
	}
	if len(srv.Messages()) != 3 {
		t.Fatalf("got %d messages", len(srv.Messages()))
	}
}

func TestWrongPassword(t *testing.T) {
	serverTLS, clientTLS := tlsConfigs(t)
	srv := newServer(t, smtptest.Options{TLSConfig: serverTLS, Username: testUser, Password: testPass})
	for _, mech := range []smtp.AuthMechanism{smtp.AuthPlain, smtp.AuthLogin} {
		s := newSender(t, smtp.Config{Host: srv.Host(), Port: srv.Port(), Username: testUser, Password: "wrong",
			Auth: mech, TLSConfig: clientTLS})
		_, err := s.Send(context.Background(), testMessage())
		if !errors.Is(err, omnimail.ErrAuth) || omnimail.IsRetryable(err) {
			t.Fatalf("%s: err = %v", mech, err)
		}
	}
}

func TestStartTLSRequired(t *testing.T) {
	srv := newServer(t, smtptest.Options{})
	s := newSender(t, smtp.Config{Host: srv.Host(), Port: srv.Port()})
	_, err := s.Send(context.Background(), testMessage())
	if err == nil || !strings.Contains(err.Error(), "STARTTLS") || omnimail.IsRetryable(err) {
		t.Fatalf("err = %v", err)
	}
}

func TestAuthNotOffered(t *testing.T) {
	serverTLS, clientTLS := tlsConfigs(t)
	srv := newServer(t, smtptest.Options{TLSConfig: serverTLS})
	s := newSender(t, smtp.Config{Host: srv.Host(), Port: srv.Port(), Username: testUser, Password: testPass, TLSConfig: clientTLS})
	if _, err := s.Send(context.Background(), testMessage()); omnimail.KindOf(err) != omnimail.KindAuth {
		t.Fatalf("err = %v", err)
	}
}

func TestUntrustedCertificate(t *testing.T) {
	serverTLS, _ := tlsConfigs(t)
	for _, implicit := range []bool{false, true} {
		srv := newServer(t, smtptest.Options{TLSConfig: serverTLS, ImplicitTLS: implicit})
		sec := smtp.StartTLS
		if implicit {
			sec = smtp.ImplicitTLS
		}
		s := newSender(t, smtp.Config{Host: srv.Host(), Port: srv.Port(), Security: sec})
		_, err := s.Send(context.Background(), testMessage())
		if err == nil || omnimail.IsRetryable(err) || omnimail.KindOf(err) != omnimail.KindUnknown {
			t.Fatalf("implicit=%v: err = %v, want non-retryable certificate error", implicit, err)
		}
	}
}

func TestSMTPUTF8Required(t *testing.T) {
	srv := newServer(t, smtptest.Options{})
	s := newSender(t, smtp.Config{Host: srv.Host(), Port: srv.Port(), Security: smtp.Plaintext})
	for _, mutate := range []func(*omnimail.Message){
		func(m *omnimail.Message) { m.To[0].Email = "用户@例子.广告" },
		func(m *omnimail.Message) { m.From.Email = "josé@example.com" },
		func(m *omnimail.Message) { m.ReplyTo = []omnimail.Address{{Email: "josé@example.com"}} },
	} {
		msg := testMessage()
		mutate(msg)
		if _, err := s.Send(context.Background(), msg); !errors.Is(err, omnimail.ErrInvalidAddress) {
			t.Fatalf("err = %v", err)
		}
	}
}

func TestConnectionRefused(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	s := newSender(t, smtp.Config{Host: "127.0.0.1", Port: port, Security: smtp.Plaintext})
	_, err = s.Send(context.Background(), testMessage())
	if !errors.Is(err, omnimail.ErrTransient) || !omnimail.IsRetryable(err) {
		t.Fatalf("err = %v", err)
	}
}

func TestGreetingFailure(t *testing.T) {
	srv := newServer(t, smtptest.Options{})
	srv.FailNext(smtptest.Failure{Stage: smtptest.StageConnect, Code: 421, Message: "4.7.0 Too many connections"})
	s := newSender(t, smtp.Config{Host: srv.Host(), Port: srv.Port(), Security: smtp.Plaintext})
	if _, err := s.Send(context.Background(), testMessage()); !errors.Is(err, omnimail.ErrThrottled) {
		t.Fatalf("err = %v", err)
	}
	// net/smtp falls back from EHLO to HELO, so fail both.
	srv.FailNext(smtptest.Failure{Stage: smtptest.StageHelo, Code: 554, Message: "5.7.1 Go away"})
	srv.FailNext(smtptest.Failure{Stage: smtptest.StageHelo, Code: 554, Message: "5.7.1 Go away"})
	if _, err := s.Send(context.Background(), testMessage()); !errors.Is(err, omnimail.ErrRejected) {
		t.Fatalf("err = %v", err)
	}
}

func TestStallCanceledByContext(t *testing.T) {
	srv := newServer(t, smtptest.Options{})
	srv.FailNext(smtptest.Failure{Stage: smtptest.StageData, Stall: true})
	s := newSender(t, smtp.Config{Host: srv.Host(), Port: srv.Port(), Security: smtp.Plaintext})
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(100*time.Millisecond, cancel)
	start := time.Now()
	_, err := s.Send(ctx, testMessage())
	if !errors.Is(err, context.Canceled) || omnimail.IsRetryable(err) {
		t.Fatalf("err = %v", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("cancellation did not interrupt the session")
	}
}

func TestStallTimeout(t *testing.T) {
	srv := newServer(t, smtptest.Options{})
	srv.FailNext(smtptest.Failure{Stage: smtptest.StageMail, Stall: true})
	s := newSender(t, smtp.Config{Host: srv.Host(), Port: srv.Port(), Security: smtp.Plaintext, Timeout: 200 * time.Millisecond})
	_, err := s.Send(context.Background(), testMessage())
	if !errors.Is(err, omnimail.ErrTransient) || !omnimail.IsRetryable(err) {
		t.Fatalf("err = %v", err)
	}
}

func TestContextDeadline(t *testing.T) {
	srv := newServer(t, smtptest.Options{})
	srv.FailNext(smtptest.Failure{Stage: smtptest.StageRcpt, Stall: true})
	s := newSender(t, smtp.Config{Host: srv.Host(), Port: srv.Port(), Security: smtp.Plaintext})
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if _, err := s.Send(ctx, testMessage()); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v", err)
	}
}

type failingDialer struct{ conn net.Conn }

func (d failingDialer) DialContext(context.Context, string, string) (net.Conn, error) {
	return d.conn, nil
}

type deadlineErrConn struct{ net.Conn }

func (deadlineErrConn) SetDeadline(time.Time) error { return errors.New("no deadlines") }
func (deadlineErrConn) Close() error                { return nil }

func TestCustomDialerDeadlineError(t *testing.T) {
	s := newSender(t, smtp.Config{Host: "mail.example.com", Security: smtp.Plaintext, Dialer: failingDialer{conn: deadlineErrConn{}}})
	if _, err := s.Send(context.Background(), testMessage()); err == nil || !strings.Contains(err.Error(), "no deadlines") {
		t.Fatalf("err = %v", err)
	}
}

func TestSendInvalidAndCanceledBeforeDial(t *testing.T) {
	s := newSender(t, smtp.Config{Host: "mail.example.com"})
	if _, err := s.Send(context.Background(), nil); !errors.Is(err, omnimail.ErrInvalidMessage) {
		t.Fatalf("nil: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Send(ctx, testMessage()); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled: %v", err)
	}
}

func TestNewConfig(t *testing.T) {
	bad := []smtp.Config{
		{},
		{Host: "h", Security: smtp.Security(9)},
		{Host: "h", Auth: "CRAM-MD5"},
		{Host: "h", Port: 70000},
	}
	for _, cfg := range bad {
		if _, err := smtp.New(cfg); err == nil {
			t.Errorf("New(%+v) should fail", cfg)
		}
	}
	if _, err := smtp.New(smtp.Config{Host: "h", TLSConfig: &tls.Config{MinVersion: tls.VersionTLS13}}); err != nil {
		t.Fatal(err)
	}
}

func TestSecurityString(t *testing.T) {
	for s, want := range map[smtp.Security]string{smtp.StartTLS: "starttls", smtp.ImplicitTLS: "tls", smtp.Plaintext: "plaintext", smtp.Security(7): "invalid"} {
		if s.String() != want {
			t.Errorf("%d: %q", int(s), s.String())
		}
	}
}
