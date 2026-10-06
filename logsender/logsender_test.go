package logsender

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/plexusone/omnimail"
)

func testMessage() *omnimail.Message {
	return &omnimail.Message{
		From:           omnimail.Address{Name: "App", Email: "no-reply@example.com"},
		To:             []omnimail.Address{{Email: "to@example.org"}},
		Bcc:            []omnimail.Address{{Email: "bcc@example.org"}},
		Subject:        "Verify",
		Text:           "secret link https://example.com/verify?token=SECRET",
		HTML:           "<a href=\"https://example.com/verify?token=SECRET\">x</a>",
		Headers:        map[string]string{"X-A": "1"},
		Tags:           map[string]string{"purpose": "verify"},
		IdempotencyKey: "k1",
	}
}

func decode(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()
	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("decode %q: %v", buf.String(), err)
	}
	return rec
}

func TestSendRedactsBodies(t *testing.T) {
	var buf bytes.Buffer
	s := New(slog.New(slog.NewJSONHandler(&buf, nil)))
	res, err := s.Send(context.Background(), testMessage())
	if err != nil {
		t.Fatal(err)
	}
	if res.Provider != ProviderName || res.MessageID == "" {
		t.Fatalf("result = %+v", res)
	}
	if strings.Contains(buf.String(), "SECRET") {
		t.Fatalf("body leaked into log: %s", buf.String())
	}
	rec := decode(t, &buf)
	if rec["text"] != Redacted || rec["html"] != Redacted || rec["subject"] != "Verify" || rec["message_id"] != res.MessageID {
		t.Fatalf("record = %v", rec)
	}
	if rec["idempotency_key"] != "k1" || rec["tags"].(map[string]any)["purpose"] != "verify" || rec["headers"].(map[string]any)["X-A"] != "1" {
		t.Fatalf("record = %v", rec)
	}
	if rec["from"] != `"App" <no-reply@example.com>` {
		t.Fatalf("from = %v", rec["from"])
	}
}

func TestSendWithBodiesAndLevel(t *testing.T) {
	var buf bytes.Buffer
	s := New(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})), WithBodies(), WithLevel(slog.LevelDebug))
	m := testMessage()
	m.HTML = ""
	m.Headers, m.Tags, m.IdempotencyKey = nil, nil, ""
	if _, err := s.Send(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	rec := decode(t, &buf)
	if rec["level"] != "DEBUG" || rec["text"] != m.Text || rec["html"] != "" {
		t.Fatalf("record = %v", rec)
	}
	if _, ok := rec["tags"]; ok {
		t.Fatal("empty tags should be omitted")
	}
}

func TestSendErrors(t *testing.T) {
	s := New(nil)
	if _, err := s.Send(context.Background(), nil); !errors.Is(err, omnimail.ErrInvalidMessage) {
		t.Fatalf("nil: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Send(ctx, testMessage()); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled: %v", err)
	}
	m := testMessage()
	m.Text = ""
	var buf bytes.Buffer
	s = New(slog.New(slog.NewJSONHandler(&buf, nil)))
	if _, err := s.Send(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	if rec := decode(t, &buf); rec["text"] != "" || rec["html"] != Redacted {
		t.Fatalf("record = %v", rec)
	}
}
