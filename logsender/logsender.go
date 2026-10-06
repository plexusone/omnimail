// Package logsender provides an omnimail.Sender that writes messages to a
// slog.Logger instead of delivering them. It is intended for local
// development. Bodies are redacted by default because transactional mail
// carries secrets such as verification links; enable them with WithBodies
// only where logs are private.
package logsender

import (
	"context"
	"crypto/rand"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strings"

	"github.com/plexusone/omnimail"
)

// ProviderName is reported in SendResult.Provider.
const ProviderName = "log"

// Redacted replaces body content in log records unless WithBodies is set.
const Redacted = "[redacted]"

// Option configures a Sender.
type Option func(*Sender)

// WithBodies logs the text and HTML bodies instead of redacting them.
func WithBodies() Option { return func(s *Sender) { s.bodies = true } }

// WithLevel sets the log level (default slog.LevelInfo).
func WithLevel(l slog.Level) Option { return func(s *Sender) { s.level = l } }

// Sender logs messages. It is safe for concurrent use.
type Sender struct {
	logger *slog.Logger
	level  slog.Level
	bodies bool
}

var _ omnimail.Sender = (*Sender)(nil)

// New returns a Sender writing to logger, or slog.Default() when nil.
func New(logger *slog.Logger, opts ...Option) *Sender {
	if logger == nil {
		logger = slog.Default()
	}
	s := &Sender{logger: logger, level: slog.LevelInfo}
	for _, o := range opts {
		o(s)
	}
	return s
}

// Send validates msg and logs it as one record.
func (s *Sender) Send(ctx context.Context, msg *omnimail.Message) (*omnimail.SendResult, error) {
	if err := omnimail.ValidateMessage(msg); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("logsender: %w", err)
	}
	id := strings.ToLower(rand.Text()) + "@logsender.invalid"
	text, html := Redacted, Redacted
	if s.bodies {
		text, html = msg.Text, msg.HTML
	}
	if msg.Text == "" {
		text = ""
	}
	if msg.HTML == "" {
		html = ""
	}
	attrs := []slog.Attr{
		slog.String("provider", ProviderName),
		slog.String("message_id", id),
		slog.String("from", msg.From.String()),
		slog.Any("to", addrs(msg.To)),
		slog.Any("cc", addrs(msg.Cc)),
		slog.Any("bcc", addrs(msg.Bcc)),
		slog.Any("reply_to", addrs(msg.ReplyTo)),
		slog.String("subject", msg.Subject),
		slog.Int("text_bytes", len(msg.Text)),
		slog.Int("html_bytes", len(msg.HTML)),
		slog.String("text", text),
		slog.String("html", html),
	}
	if len(msg.Headers) > 0 {
		attrs = append(attrs, slog.Any("headers", group(msg.Headers)))
	}
	if len(msg.Tags) > 0 {
		attrs = append(attrs, slog.Any("tags", group(msg.Tags)))
	}
	if msg.IdempotencyKey != "" {
		attrs = append(attrs, slog.String("idempotency_key", msg.IdempotencyKey))
	}
	s.logger.LogAttrs(ctx, s.level, "omnimail: message logged, not delivered", attrs...)
	return &omnimail.SendResult{MessageID: id, Provider: ProviderName}, nil
}

func addrs(list []omnimail.Address) []string {
	out := make([]string, len(list))
	for i, a := range list {
		out[i] = a.String()
	}
	return out
}

func group(m map[string]string) slog.Value {
	attrs := make([]slog.Attr, 0, len(m))
	for _, k := range slices.Sorted(maps.Keys(m)) {
		attrs = append(attrs, slog.String(k, m[k]))
	}
	return slog.GroupValue(attrs...)
}
