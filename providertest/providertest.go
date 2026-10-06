// Package providertest is the conformance suite for omnimail.Sender
// implementations. Every provider adapter (SMTP, SES, SendGrid, Gmail, ...)
// runs it in its own tests against a fake endpoint, so all providers behave
// the same for addressing, bodies, headers, validation, cancellation and
// error classification.
//
// An adapter supplies a [Harness]: the sender under test wired to a fake
// provider endpoint (an httptest server, an in-process SMTP server, or a
// capture hook) that records what the provider received and can be told to
// fail. Then:
//
//	func TestConformance(t *testing.T) {
//	    providertest.RunAll(t, providertest.Config{
//	        Harness:      newFakeSESHarness(t),
//	        Provider:     "ses",
//	        SupportsTags: true,
//	    })
//	}
//
// Raw-MIME providers can decode what their endpoint received with
// [DecodeMIME].
package providertest

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/plexusone/omnimail"
)

// Harness connects the suite to a provider adapter and its fake endpoint.
// Methods are called from a single test goroutine, except that Sender's
// result is used concurrently by the concurrency check.
type Harness interface {
	// Sender returns the sender under test, configured to talk to the fake
	// endpoint.
	Sender() omnimail.Sender
	// Captured returns the messages the endpoint accepted since the last
	// Reset, in arrival order, decoded back into omnimail.Message form as the
	// provider received them. Bcc must hold envelope recipients that are not
	// in To or Cc. Tags and IdempotencyKey are populated when the provider
	// transmits them. Headers holds custom headers only (it must include a
	// "Bcc" entry if a Bcc header was transmitted, so leaks are detected).
	Captured(t testing.TB) []*omnimail.Message
	// Reset clears captured messages and pending injected failures.
	Reset()
	// InjectFailure arranges for the next send to fail at the endpoint the
	// way the real provider reports a failure of the given kind (an SMTP
	// reply code, an API error code, an HTTP status). It returns false when
	// the kind cannot be simulated, and that check is skipped.
	InjectFailure(kind omnimail.Kind) bool
}

// Config configures the suite.
type Config struct {
	// Harness is required.
	Harness Harness
	// Provider is the expected SendResult.Provider and Error.Provider.
	// Empty skips those checks.
	Provider string
	// From is the sender used in test messages (default
	// "no-reply@example.com"). Set it when the fake enforces verified
	// senders.
	From omnimail.Address
	// SupportsTags asserts that Message.Tags reach the provider.
	SupportsTags bool
	// SupportsIdempotencyKey asserts that Message.IdempotencyKey reaches the
	// provider.
	SupportsIdempotencyKey bool
	// SupportsSMTPUTF8 asserts that internationalized addresses
	// (non-ASCII local part or domain) are delivered.
	SupportsSMTPUTF8 bool
	// SkipCustomHeaders skips the custom header check for providers that
	// cannot transmit arbitrary headers.
	SkipCustomHeaders bool
	// Timeout bounds each send (default 10s).
	Timeout time.Duration
}

func (c Config) withDefaults() Config {
	if c.From.Email == "" {
		c.From = omnimail.Address{Name: "Conformance Sender", Email: "no-reply@example.com"}
	}
	if c.Timeout <= 0 {
		c.Timeout = 10 * time.Second
	}
	return c
}

// RunAll runs every conformance check as subtests of t.
func RunAll(t *testing.T, cfg Config) {
	t.Helper()
	if cfg.Harness == nil {
		t.Fatal("providertest: Config.Harness is required")
	}
	cfg = cfg.withDefaults()
	s := &suite{cfg: cfg, h: cfg.Harness}
	checks := []struct {
		name string
		fn   func(*testing.T)
	}{
		{"SendResult", s.testSendResult},
		{"TextOnly", s.testTextOnly},
		{"HTMLOnly", s.testHTMLOnly},
		{"TextAndHTML", s.testTextAndHTML},
		{"Addressing", s.testAddressing},
		{"ReplyTo", s.testReplyTo},
		{"UnicodeHeaders", s.testUnicodeHeaders},
		{"CustomHeaders", s.testCustomHeaders},
		{"Tags", s.testTags},
		{"IdempotencyKey", s.testIdempotencyKey},
		{"InternationalAddress", s.testInternationalAddress},
		{"DoesNotMutateMessage", s.testDoesNotMutate},
		{"InvalidMessage", s.testInvalidMessage},
		{"ContextCanceled", s.testContextCanceled},
		{"ErrorClassification", s.testErrorClassification},
		{"ConcurrentSends", s.testConcurrentSends},
	}
	for _, c := range checks {
		t.Run(c.name, func(t *testing.T) {
			s.h.Reset()
			c.fn(t)
		})
	}
}

type suite struct {
	cfg Config
	h   Harness
}

func (s *suite) message(t *testing.T) *omnimail.Message {
	return &omnimail.Message{
		From:    s.cfg.From,
		To:      []omnimail.Address{{Name: "Recipient One", Email: "to1@example.org"}},
		Subject: "omnimail conformance " + t.Name(),
		Text:    "Hello from the omnimail conformance suite.\nSecond line.\n",
	}
}

func (s *suite) send(t *testing.T, msg *omnimail.Message) (*omnimail.SendResult, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), s.cfg.Timeout)
	defer cancel()
	return s.h.Sender().Send(ctx, msg)
}

// sendOne sends msg, requires success and returns the single captured
// message.
func (s *suite) sendOne(t *testing.T, msg *omnimail.Message) *omnimail.Message {
	t.Helper()
	if _, err := s.send(t, msg); err != nil {
		t.Fatalf("Send: %v", err)
	}
	got := s.h.Captured(t)
	if len(got) != 1 {
		t.Fatalf("endpoint captured %d messages, want 1", len(got))
	}
	return got[0]
}

func (s *suite) testSendResult(t *testing.T) {
	res, err := s.send(t, s.message(t))
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if res == nil {
		t.Fatal("Send returned nil result and nil error")
	}
	if res.MessageID == "" {
		t.Error("SendResult.MessageID is empty")
	}
	if s.cfg.Provider != "" && res.Provider != s.cfg.Provider {
		t.Errorf("SendResult.Provider = %q, want %q", res.Provider, s.cfg.Provider)
	}
}

func (s *suite) testTextOnly(t *testing.T) {
	msg := s.message(t)
	got := s.sendOne(t, msg)
	checkCommon(t, msg, got)
	if got.HTML != "" {
		t.Errorf("HTML = %q, want empty for a text-only message", got.HTML)
	}
}

func (s *suite) testHTMLOnly(t *testing.T) {
	msg := s.message(t)
	msg.Text = ""
	msg.HTML = `<!doctype html><p>Hello <b>conformance</b> &amp; friends.</p>`
	got := s.sendOne(t, msg)
	checkCommon(t, msg, got)
	if got.Text != "" {
		t.Errorf("Text = %q, want empty for an HTML-only message", got.Text)
	}
}

func (s *suite) testTextAndHTML(t *testing.T) {
	msg := s.message(t)
	msg.Text = "Plain café body with a long line: " + strings.Repeat("abcdefghij", 12) + "\n"
	msg.HTML = "<p>HTML café body</p>\n<p>" + strings.Repeat("abcdefghij", 12) + "</p>"
	checkCommon(t, msg, s.sendOne(t, msg))
}

func (s *suite) testAddressing(t *testing.T) {
	msg := s.message(t)
	msg.To = []omnimail.Address{
		{Name: "Recipient One", Email: "to1@example.org"},
		{Email: "to2@example.org"},
		{Name: "Doe, Jane", Email: "to3@example.org"},
	}
	msg.Cc = []omnimail.Address{{Name: "Copy", Email: "cc1@example.org"}, {Email: "cc2@example.org"}}
	msg.Bcc = []omnimail.Address{{Email: "bcc1@example.org"}, {Email: "bcc2@example.org"}}
	got := s.sendOne(t, msg)
	checkCommon(t, msg, got)
	checkEmails(t, "Bcc", msg.Bcc, got.Bcc)
	for k := range got.Headers {
		if strings.EqualFold(k, "Bcc") {
			t.Errorf("Bcc header was transmitted: %q", got.Headers[k])
		}
	}

	s.h.Reset()
	bccOnly := s.message(t)
	bccOnly.To = nil
	bccOnly.Bcc = []omnimail.Address{{Email: "bcc-only@example.org"}}
	got = s.sendOne(t, bccOnly)
	checkEmails(t, "Bcc", bccOnly.Bcc, got.Bcc)
}

func (s *suite) testReplyTo(t *testing.T) {
	msg := s.message(t)
	msg.ReplyTo = []omnimail.Address{{Name: "Support", Email: "support@example.com"}}
	got := s.sendOne(t, msg)
	checkAddresses(t, "ReplyTo", msg.ReplyTo, got.ReplyTo)
}

func (s *suite) testUnicodeHeaders(t *testing.T) {
	msg := s.message(t)
	msg.Subject = "Vérifiez votre adresse — 確認してください " + t.Name()
	msg.From.Name = "Zoë Sender"
	msg.To = []omnimail.Address{{Name: "José Ünicode", Email: "jose@example.org"}}
	msg.Text = "Grüße, 世界\n"
	checkCommon(t, msg, s.sendOne(t, msg))
}

func (s *suite) testCustomHeaders(t *testing.T) {
	if s.cfg.SkipCustomHeaders {
		t.Skip("provider does not transmit custom headers")
	}
	msg := s.message(t)
	msg.Headers = map[string]string{
		"X-Omnimail-Test":  "conformance",
		"List-Unsubscribe": "<mailto:unsubscribe@example.com>",
	}
	got := s.sendOne(t, msg)
	for k, v := range msg.Headers {
		if gv, ok := lookupHeader(got.Headers, k); !ok || gv != v {
			t.Errorf("header %s = %q (present %v), want %q", k, gv, ok, v)
		}
	}
}

func (s *suite) testTags(t *testing.T) {
	msg := s.message(t)
	msg.Tags = map[string]string{"purpose": "verify_email", "tenant": "t-123"}
	_, err := s.send(t, msg)
	if err != nil {
		t.Fatalf("Send with tags: %v", err)
	}
	if !s.cfg.SupportsTags {
		t.Skip("provider does not support tags; checked that tags are accepted")
	}
	got := s.h.Captured(t)
	if len(got) != 1 {
		t.Fatalf("captured %d messages, want 1", len(got))
	}
	if !reflect.DeepEqual(got[0].Tags, msg.Tags) {
		t.Errorf("Tags = %v, want %v", got[0].Tags, msg.Tags)
	}
}

func (s *suite) testIdempotencyKey(t *testing.T) {
	msg := s.message(t)
	msg.IdempotencyKey = "conformance-key-1"
	if _, err := s.send(t, msg); err != nil {
		t.Fatalf("Send with idempotency key: %v", err)
	}
	if !s.cfg.SupportsIdempotencyKey {
		t.Skip("provider does not support idempotency keys; checked that the key is accepted")
	}
	got := s.h.Captured(t)
	if len(got) == 0 || got[0].IdempotencyKey != msg.IdempotencyKey {
		t.Errorf("IdempotencyKey not transmitted: %+v", got)
	}
}

func (s *suite) testInternationalAddress(t *testing.T) {
	if !s.cfg.SupportsSMTPUTF8 {
		t.Skip("provider does not declare SMTPUTF8 support")
	}
	msg := s.message(t)
	msg.To = []omnimail.Address{{Name: "用户", Email: "用户@例子.广告"}}
	got := s.sendOne(t, msg)
	checkAddresses(t, "To", msg.To, got.To)
}

func (s *suite) testDoesNotMutate(t *testing.T) {
	msg := s.message(t)
	msg.Cc = []omnimail.Address{{Email: "cc@example.org"}}
	msg.Bcc = []omnimail.Address{{Email: "bcc@example.org"}}
	msg.HTML = "<p>hi</p>"
	msg.Headers = map[string]string{"X-A": "1"}
	msg.Tags = map[string]string{"t": "1"}
	before := msg.Clone()
	if _, err := s.send(t, msg); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if !reflect.DeepEqual(before, msg) {
		t.Errorf("Send modified the message:\nbefore %+v\nafter  %+v", before, msg)
	}
}

func (s *suite) testInvalidMessage(t *testing.T) {
	cases := map[string]func(*omnimail.Message){
		"NoRecipients":       func(m *omnimail.Message) { m.To = nil },
		"NoBody":             func(m *omnimail.Message) { m.Text = "" },
		"SubjectInjection":   func(m *omnimail.Message) { m.Subject = "Hi\r\nBcc: victim@example.org" },
		"HeaderInjection":    func(m *omnimail.Message) { m.Headers = map[string]string{"X-A": "1\r\nBcc: victim@example.org"} },
		"DisplayNameNewline": func(m *omnimail.Message) { m.To[0].Name = "x\nBcc: victim@example.org" },
		"BadAddress":         func(m *omnimail.Message) { m.To[0].Email = "not an address" },
		"ReservedHeader":     func(m *omnimail.Message) { m.Headers = map[string]string{"Content-Type": "text/plain"} },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			s.h.Reset()
			msg := s.message(t)
			mutate(msg)
			res, err := s.send(t, msg)
			if !errors.Is(err, omnimail.ErrInvalidMessage) {
				t.Fatalf("Send = %+v, %v; want error matching ErrInvalidMessage", res, err)
			}
			if got := s.h.Captured(t); len(got) != 0 {
				t.Errorf("invalid message reached the endpoint (%d captured)", len(got))
			}
		})
	}
	t.Run("Nil", func(t *testing.T) {
		if _, err := s.send(t, nil); !errors.Is(err, omnimail.ErrInvalidMessage) {
			t.Fatalf("Send(nil) error = %v, want ErrInvalidMessage", err)
		}
	})
}

func (s *suite) testContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res, err := s.h.Sender().Send(ctx, s.message(t))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Send with canceled context = %+v, %v; want error matching context.Canceled", res, err)
	}
	if omnimail.IsRetryable(err) {
		t.Error("cancellation must not be reported as retryable")
	}
	if got := s.h.Captured(t); len(got) != 0 {
		t.Errorf("message sent despite canceled context (%d captured)", len(got))
	}
}

func (s *suite) testErrorClassification(t *testing.T) {
	kinds := []omnimail.Kind{
		omnimail.KindInvalidAddress, omnimail.KindRejected, omnimail.KindThrottled,
		omnimail.KindTransient, omnimail.KindAuth,
	}
	for _, kind := range kinds {
		t.Run(kind.String(), func(t *testing.T) {
			s.h.Reset()
			if !s.h.InjectFailure(kind) {
				t.Skipf("harness cannot simulate %s", kind)
			}
			res, err := s.send(t, s.message(t))
			if err == nil {
				t.Fatalf("Send = %+v, want %s error", res, kind)
			}
			if got := omnimail.KindOf(err); got != kind {
				t.Errorf("KindOf(%v) = %s, want %s", err, got, kind)
			}
			var e *omnimail.Error
			if !errors.As(err, &e) {
				t.Fatalf("error %v is not an *omnimail.Error", err)
			}
			if s.cfg.Provider != "" && e.Provider != s.cfg.Provider {
				t.Errorf("Error.Provider = %q, want %q", e.Provider, s.cfg.Provider)
			}
			wantRetry := kind == omnimail.KindThrottled || kind == omnimail.KindTransient
			if omnimail.IsRetryable(err) != wantRetry {
				t.Errorf("IsRetryable = %v, want %v", omnimail.IsRetryable(err), wantRetry)
			}
			if got := s.h.Captured(t); len(got) != 0 {
				t.Errorf("failed send was captured as accepted (%d)", len(got))
			}
			s.h.Reset()
			if _, err := s.send(t, s.message(t)); err != nil {
				t.Errorf("send after injected failure: %v (failures must not persist)", err)
			}
		})
	}
}

func (s *suite) testConcurrentSends(t *testing.T) {
	const n = 8
	sender := s.h.Sender()
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := range n {
		wg.Go(func() {
			msg := s.message(t)
			msg.Subject = fmt.Sprintf("%s #%d", msg.Subject, i)
			ctx, cancel := context.WithTimeout(context.Background(), s.cfg.Timeout)
			defer cancel()
			if _, err := sender.Send(ctx, msg); err != nil {
				errs <- err
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent Send: %v", err)
	}
	if got := s.h.Captured(t); len(got) != n {
		t.Errorf("captured %d messages, want %d", len(got), n)
	}
}

// checkCommon compares the fields every provider must transmit.
func checkCommon(t *testing.T, want, got *omnimail.Message) {
	t.Helper()
	if got.From.Email != want.From.Email {
		t.Errorf("From = %q, want %q", got.From.Email, want.From.Email)
	}
	if want.From.Name != "" && got.From.Name != want.From.Name {
		t.Errorf("From name = %q, want %q", got.From.Name, want.From.Name)
	}
	checkAddresses(t, "To", want.To, got.To)
	checkAddresses(t, "Cc", want.Cc, got.Cc)
	if got.Subject != want.Subject {
		t.Errorf("Subject = %q, want %q", got.Subject, want.Subject)
	}
	if NormalizeBody(got.Text) != NormalizeBody(want.Text) {
		t.Errorf("Text = %q, want %q", got.Text, want.Text)
	}
	if NormalizeBody(got.HTML) != NormalizeBody(want.HTML) {
		t.Errorf("HTML = %q, want %q", got.HTML, want.HTML)
	}
}

func checkAddresses(t *testing.T, field string, want, got []omnimail.Address) {
	t.Helper()
	checkEmails(t, field, want, got)
	if len(want) != len(got) {
		return
	}
	for i := range want {
		if want[i].Name != got[i].Name {
			t.Errorf("%s[%d] name = %q, want %q", field, i, got[i].Name, want[i].Name)
		}
	}
}

func checkEmails(t *testing.T, field string, want, got []omnimail.Address) {
	t.Helper()
	if len(want) != len(got) {
		t.Errorf("%s has %d addresses %v, want %d %v", field, len(got), got, len(want), want)
		return
	}
	for i := range want {
		if !strings.EqualFold(want[i].Email, got[i].Email) {
			t.Errorf("%s[%d] = %q, want %q", field, i, got[i].Email, want[i].Email)
		}
	}
}

func lookupHeader(h map[string]string, name string) (string, bool) {
	for k, v := range h {
		if strings.EqualFold(k, name) {
			return v, true
		}
	}
	return "", false
}

// NormalizeBody converts CRLF and CR line endings to LF and trims trailing
// line breaks, so bodies compare equal across transports that add a final
// newline or rewrite line endings.
func NormalizeBody(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	return strings.TrimRight(s, "\n")
}
