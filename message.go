package omnimail

import (
	"fmt"
	"maps"
	"net/textproto"
	"slices"
	"strings"
	"unicode/utf8"
)

// Message is a transactional email.
//
// At least one recipient (To, Cc or Bcc) and at least one body (Text or HTML)
// are required. When both bodies are set the message is sent as
// multipart/alternative.
type Message struct {
	From    Address   `json:"from"`
	To      []Address `json:"to,omitempty"`
	Cc      []Address `json:"cc,omitempty"`
	Bcc     []Address `json:"bcc,omitempty"`
	ReplyTo []Address `json:"replyTo,omitempty"`

	// Subject is plain text; it must not contain line breaks. Non-ASCII
	// subjects are RFC 2047 encoded on assembly.
	Subject string `json:"subject"`

	// Text is the plain-text body.
	Text string `json:"text,omitempty"`
	// HTML is the HTML body.
	HTML string `json:"html,omitempty"`

	// Headers are additional header fields, e.g. "List-Unsubscribe".
	// Structural headers managed by the library (From, To, Subject,
	// Content-Type, ...) cannot be set here; see [ReservedHeader].
	Headers map[string]string `json:"headers,omitempty"`

	// Tags are provider metadata (e.g. SES message tags, SendGrid categories)
	// used for analytics and event routing. Providers that do not support
	// tags ignore them.
	Tags map[string]string `json:"tags,omitempty"`

	// IdempotencyKey, when set, asks providers that support it to deliver the
	// message at most once per key.
	IdempotencyKey string `json:"idempotencyKey,omitempty"`
}

var reservedHeaders = map[string]bool{
	"Bcc":                       true,
	"Cc":                        true,
	"Content-Transfer-Encoding": true,
	"Content-Type":              true,
	"Date":                      true,
	"From":                      true,
	"Message-Id":                true,
	"Mime-Version":              true,
	"Reply-To":                  true,
	"Return-Path":               true,
	"Sender":                    true,
	"Subject":                   true,
	"To":                        true,
}

// ReservedHeader reports whether name is a header managed by the library that
// cannot be set through [Message.Headers].
func ReservedHeader(name string) bool {
	return reservedHeaders[textproto.CanonicalMIMEHeaderKey(name)]
}

// Recipients returns To, Cc and Bcc in that order (the SMTP envelope
// recipients).
func (m *Message) Recipients() []Address {
	out := make([]Address, 0, len(m.To)+len(m.Cc)+len(m.Bcc))
	out = append(out, m.To...)
	out = append(out, m.Cc...)
	out = append(out, m.Bcc...)
	return out
}

// Clone returns a deep copy of m.
func (m *Message) Clone() *Message {
	if m == nil {
		return nil
	}
	c := *m
	c.To = slices.Clone(m.To)
	c.Cc = slices.Clone(m.Cc)
	c.Bcc = slices.Clone(m.Bcc)
	c.ReplyTo = slices.Clone(m.ReplyTo)
	c.Headers = maps.Clone(m.Headers)
	c.Tags = maps.Clone(m.Tags)
	return &c
}

// ValidateMessage validates msg, treating nil as invalid. Senders call it
// before contacting a provider.
func ValidateMessage(msg *Message) error {
	if msg == nil {
		return invalidMessage("message", "message is nil")
	}
	return msg.Validate()
}

// Validate checks that the message can be sent safely. It returns an [*Error]
// of kind [KindInvalidMessage] or [KindInvalidAddress]; both match
// [ErrInvalidMessage] with errors.Is.
//
// Validation rejects line breaks and other control characters in every value
// that becomes a header (subject, display names, custom headers, tags,
// idempotency key), which prevents header injection.
func (m *Message) Validate() error {
	if err := m.From.validate("from"); err != nil {
		return err
	}
	if len(m.To)+len(m.Cc)+len(m.Bcc) == 0 {
		return invalidMessage("to", "at least one recipient is required")
	}
	lists := []struct {
		field string
		addrs []Address
	}{{"to", m.To}, {"cc", m.Cc}, {"bcc", m.Bcc}, {"replyTo", m.ReplyTo}}
	for _, l := range lists {
		for i, a := range l.addrs {
			if err := a.validate(fmt.Sprintf("%s[%d]", l.field, i)); err != nil {
				return err
			}
		}
	}
	if err := checkHeaderValue("subject", m.Subject); err != nil {
		return err
	}
	if m.Text == "" && m.HTML == "" {
		return invalidMessage("text", "a text or HTML body is required")
	}
	if !utf8.ValidString(m.Text) {
		return invalidMessage("text", "body is not valid UTF-8")
	}
	if !utf8.ValidString(m.HTML) {
		return invalidMessage("html", "body is not valid UTF-8")
	}
	for _, name := range slices.Sorted(maps.Keys(m.Headers)) {
		if err := checkHeaderName(name); err != nil {
			return err
		}
		if ReservedHeader(name) {
			return invalidMessage("headers", fmt.Sprintf("header %q is managed by omnimail and cannot be set", name))
		}
		if err := checkHeaderValue("headers["+name+"]", m.Headers[name]); err != nil {
			return err
		}
	}
	for _, k := range slices.Sorted(maps.Keys(m.Tags)) {
		if k == "" {
			return invalidMessage("tags", "tag name is empty")
		}
		if err := checkHeaderValue("tags", k); err != nil {
			return err
		}
		if err := checkHeaderValue("tags["+k+"]", m.Tags[k]); err != nil {
			return err
		}
	}
	return checkHeaderValue("idempotencyKey", m.IdempotencyKey)
}

// checkHeaderName enforces RFC 5322 field-name syntax: printable US-ASCII
// except colon.
func checkHeaderName(name string) error {
	if name == "" {
		return invalidMessage("headers", "header name is empty")
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if c < 33 || c > 126 || c == ':' {
			return invalidMessage("headers", fmt.Sprintf("invalid header name %q", name))
		}
	}
	return nil
}

// checkHeaderValue rejects line breaks, control characters and invalid UTF-8
// in a value that is written into a header.
func checkHeaderValue(field, v string) error {
	if !utf8.ValidString(v) {
		return invalidMessage(field, "not valid UTF-8")
	}
	if hasLineBreak(v) {
		return invalidMessage(field, "contains a line break (possible header injection)")
	}
	if strings.ContainsFunc(v, func(r rune) bool { return r != '\t' && (r < 0x20 || r == 0x7f) }) {
		return invalidMessage(field, "contains control characters")
	}
	return nil
}
