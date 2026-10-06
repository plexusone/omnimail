package omnimail

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func validMessage() *Message {
	return &Message{
		From:    Address{Name: "Example", Email: "no-reply@example.com"},
		To:      []Address{{Email: "to@example.org"}},
		Subject: "Hello",
		Text:    "Hi there",
	}
}

func TestMessageValidate(t *testing.T) {
	if err := validMessage().Validate(); err != nil {
		t.Fatalf("valid message: %v", err)
	}
	onlyBcc := validMessage()
	onlyBcc.To = nil
	onlyBcc.Bcc = []Address{{Email: "b@example.org"}}
	if err := onlyBcc.Validate(); err != nil {
		t.Fatalf("bcc-only message: %v", err)
	}
	htmlOnly := validMessage()
	htmlOnly.Text, htmlOnly.HTML = "", "<p>Hi</p>"
	if err := htmlOnly.Validate(); err != nil {
		t.Fatalf("html-only message: %v", err)
	}

	tests := map[string]struct {
		mutate func(*Message)
		kind   Kind
		field  string
	}{
		"no from":            {func(m *Message) { m.From = Address{} }, KindInvalidAddress, "from"},
		"no recipients":      {func(m *Message) { m.To = nil }, KindInvalidMessage, "to"},
		"bad to":             {func(m *Message) { m.To = []Address{{Email: "x"}} }, KindInvalidAddress, "to[0]"},
		"bad cc":             {func(m *Message) { m.Cc = []Address{{Email: "x"}} }, KindInvalidAddress, "cc[0]"},
		"bad bcc":            {func(m *Message) { m.Bcc = []Address{{Email: "x"}} }, KindInvalidAddress, "bcc[0]"},
		"bad reply-to":       {func(m *Message) { m.ReplyTo = []Address{{Email: "x"}} }, KindInvalidAddress, "replyTo[0]"},
		"subject CRLF":       {func(m *Message) { m.Subject = "Hi\r\nBcc: evil@example.com" }, KindInvalidMessage, "subject"},
		"subject LF":         {func(m *Message) { m.Subject = "Hi\nthere" }, KindInvalidMessage, "subject"},
		"subject control":    {func(m *Message) { m.Subject = "Hi\x00" }, KindInvalidMessage, "subject"},
		"subject bad utf8":   {func(m *Message) { m.Subject = "\xff" }, KindInvalidMessage, "subject"},
		"no body":            {func(m *Message) { m.Text = "" }, KindInvalidMessage, "text"},
		"text bad utf8":      {func(m *Message) { m.Text = "\xff" }, KindInvalidMessage, "text"},
		"html bad utf8":      {func(m *Message) { m.HTML = "\xff" }, KindInvalidMessage, "html"},
		"header CRLF":        {func(m *Message) { m.Headers = map[string]string{"X-A": "a\r\nBcc: e@x.com"} }, KindInvalidMessage, "headers[X-A]"},
		"header name colon":  {func(m *Message) { m.Headers = map[string]string{"X:A": "a"} }, KindInvalidMessage, "headers"},
		"header name space":  {func(m *Message) { m.Headers = map[string]string{"X A": "a"} }, KindInvalidMessage, "headers"},
		"header name empty":  {func(m *Message) { m.Headers = map[string]string{"": "a"} }, KindInvalidMessage, "headers"},
		"reserved header":    {func(m *Message) { m.Headers = map[string]string{"content-type": "text/plain"} }, KindInvalidMessage, "headers"},
		"tag empty name":     {func(m *Message) { m.Tags = map[string]string{"": "a"} }, KindInvalidMessage, "tags"},
		"tag name newline":   {func(m *Message) { m.Tags = map[string]string{"a\n": "a"} }, KindInvalidMessage, "tags"},
		"tag value newline":  {func(m *Message) { m.Tags = map[string]string{"a": "a\n"} }, KindInvalidMessage, "tags[a]"},
		"idempotency CRLF":   {func(m *Message) { m.IdempotencyKey = "k\r\n" }, KindInvalidMessage, "idempotencyKey"},
		"display name CRLF":  {func(m *Message) { m.To[0].Name = "x\r\nBcc: e@x.com" }, KindInvalidAddress, "to[0]"},
		"from email CRLF":    {func(m *Message) { m.From.Email = "a@example.com\r\nX: y" }, KindInvalidAddress, "from"},
		"header value del":   {func(m *Message) { m.Headers = map[string]string{"X-A": "a\x7f"} }, KindInvalidMessage, "headers[X-A]"},
		"header value utf-8": {func(m *Message) { m.Headers = map[string]string{"X-A": "\xff"} }, KindInvalidMessage, "headers[X-A]"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			m := validMessage()
			tt.mutate(m)
			err := m.Validate()
			if !errors.Is(err, ErrInvalidMessage) {
				t.Fatalf("Validate() = %v, want ErrInvalidMessage", err)
			}
			var e *Error
			if !errors.As(err, &e) || e.Kind != tt.kind || e.Field != tt.field {
				t.Fatalf("Validate() = %#v, want kind %v field %q", err, tt.kind, tt.field)
			}
		})
	}
}

func TestValidateMessageNil(t *testing.T) {
	if err := ValidateMessage(nil); !errors.Is(err, ErrInvalidMessage) {
		t.Fatalf("ValidateMessage(nil) = %v", err)
	}
	if err := ValidateMessage(validMessage()); err != nil {
		t.Fatal(err)
	}
}

func TestHeaderTabAllowed(t *testing.T) {
	m := validMessage()
	m.Subject = "a\tb"
	m.Headers = map[string]string{"List-Unsubscribe": "<mailto:u@example.com>"}
	if err := m.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestReservedHeader(t *testing.T) {
	for _, h := range []string{"from", "TO", "Message-ID", "mime-version", "Content-Transfer-Encoding", "bcc"} {
		if !ReservedHeader(h) {
			t.Errorf("%q should be reserved", h)
		}
	}
	if ReservedHeader("X-Custom") || ReservedHeader("List-Unsubscribe") {
		t.Error("custom headers should not be reserved")
	}
}

func TestRecipientsAndClone(t *testing.T) {
	m := validMessage()
	m.Cc = []Address{{Email: "cc@example.org"}}
	m.Bcc = []Address{{Email: "bcc@example.org"}}
	m.ReplyTo = []Address{{Email: "reply@example.org"}}
	m.Headers = map[string]string{"X-A": "1"}
	m.Tags = map[string]string{"t": "1"}
	got := m.Recipients()
	if len(got) != 3 || got[0].Email != "to@example.org" || got[2].Email != "bcc@example.org" {
		t.Fatalf("Recipients = %+v", got)
	}
	c := m.Clone()
	if !reflect.DeepEqual(c, m) {
		t.Fatal("clone differs")
	}
	c.To[0].Email = "changed@example.org"
	c.Headers["X-A"] = "2"
	c.Tags["t"] = "2"
	if m.To[0].Email != "to@example.org" || m.Headers["X-A"] != "1" || m.Tags["t"] != "1" {
		t.Fatal("clone shares state with original")
	}
	var nilMsg *Message
	if nilMsg.Clone() != nil {
		t.Fatal("Clone(nil) should be nil")
	}
	if !strings.Contains(m.From.String(), "no-reply@example.com") {
		t.Fatal("from string")
	}
}
