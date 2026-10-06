package omnimail

import (
	"bytes"
	"encoding/base64"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"strings"
	"testing"
	"time"
)

var fixedDate = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

func TestBuildMIMEGoldenTextOnly(t *testing.T) {
	m := validMessage()
	got, err := BuildMIME(m, BuildOptions{Date: fixedDate, MessageID: "<abc@example.com>"})
	if err != nil {
		t.Fatal(err)
	}
	want := "Date: Fri, 02 Jan 2026 03:04:05 +0000\r\n" +
		"Message-ID: <abc@example.com>\r\n" +
		"From: \"Example\" <no-reply@example.com>\r\n" +
		"To: to@example.org\r\n" +
		"Subject: Hello\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: text/plain; charset=utf-8\r\n" +
		"Content-Transfer-Encoding: 7bit\r\n" +
		"\r\n" +
		"Hi there\r\n"
	if string(got.Data) != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got.Data, want)
	}
	if got.MessageID != "abc@example.com" {
		t.Fatalf("MessageID = %q", got.MessageID)
	}
}

func TestBuildMIMEMultipartRoundTrip(t *testing.T) {
	m := &Message{
		From:    Address{Name: "Zoë Sender", Email: "no-reply@example.com"},
		To:      []Address{{Name: "Doe, Jane", Email: "jane@example.org"}, {Email: "b@example.org"}},
		Cc:      []Address{{Email: "cc@example.org"}},
		Bcc:     []Address{{Email: "secret@example.org"}},
		ReplyTo: []Address{{Email: "support@example.com"}},
		Subject: "Vérifiez votre adresse e-mail pour continuer — " + strings.Repeat("long subject ", 6),
		Text:    "Bonjour,\n\nOuvrez ce lien : https://example.com/verify?token=" + strings.Repeat("x", 100) + "\n",
		HTML:    "<p>Bonjour,</p>\n<p><a href=\"https://example.com/verify\">Vérifier</a></p>",
		Headers: map[string]string{"x-entity-ref-id": "abc-123", "X-Note": "naïve"},
		Tags:    map[string]string{"purpose": "verify"},
	}
	got, err := BuildMIME(m, BuildOptions{Date: fixedDate})
	if err != nil {
		t.Fatal(err)
	}
	for i, line := range strings.Split(string(got.Data), "\r\n") {
		if len(line) > 998 {
			t.Fatalf("line %d exceeds 998 octets", i)
		}
		if strings.Contains(line, "\n") || strings.Contains(line, "\r") {
			t.Fatalf("bare line ending in line %d", i)
		}
	}
	if bytes.Contains(got.Data, []byte("secret@example.org")) || bytes.Contains(got.Data, []byte("Bcc:")) {
		t.Fatal("Bcc leaked into headers")
	}
	if bytes.Contains(got.Data, []byte("purpose")) {
		t.Fatal("tags must not be written to the message")
	}
	if !strings.HasSuffix(got.MessageID, "@example.com") {
		t.Fatalf("MessageID = %q", got.MessageID)
	}

	parsed, err := mail.ReadMessage(bytes.NewReader(got.Data))
	if err != nil {
		t.Fatal(err)
	}
	dec := new(mime.WordDecoder)
	subj, err := dec.DecodeHeader(parsed.Header.Get("Subject"))
	if err != nil {
		t.Fatal(err)
	}
	if subj != m.Subject {
		t.Fatalf("subject = %q, want %q", subj, m.Subject)
	}
	from, err := parsed.Header.AddressList("From")
	if err != nil || from[0].Name != "Zoë Sender" {
		t.Fatalf("from = %v, %v", from, err)
	}
	to, err := parsed.Header.AddressList("To")
	if err != nil || len(to) != 2 || to[0].Name != "Doe, Jane" {
		t.Fatalf("to = %v, %v", to, err)
	}
	if note, _ := dec.DecodeHeader(parsed.Header.Get("X-Note")); note != "naïve" {
		t.Fatalf("X-Note = %q", note)
	}
	if parsed.Header.Get("X-Entity-Ref-Id") != "abc-123" {
		t.Fatalf("custom header missing: %v", parsed.Header)
	}
	d, err := parsed.Header.Date()
	if err != nil || !d.Equal(fixedDate) {
		t.Fatalf("date = %v, %v", d, err)
	}

	mt, params, err := mime.ParseMediaType(parsed.Header.Get("Content-Type"))
	if err != nil || mt != "multipart/alternative" {
		t.Fatalf("content type = %q, %v", mt, err)
	}
	mr := multipart.NewReader(parsed.Body, params["boundary"])
	var bodies []string
	var types []string
	for {
		p, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		b, err := io.ReadAll(p)
		if err != nil {
			t.Fatal(err)
		}
		types = append(types, p.Header.Get("Content-Type"))
		bodies = append(bodies, strings.ReplaceAll(string(b), "\r\n", "\n"))
	}
	if len(bodies) != 2 || types[0] != "text/plain; charset=utf-8" || types[1] != "text/html; charset=utf-8" {
		t.Fatalf("parts = %v", types)
	}
	if bodies[0] != m.Text || bodies[1] != m.HTML {
		t.Fatalf("bodies = %q", bodies)
	}
}

func TestBuildMIMEHTMLOnlyAndEncodings(t *testing.T) {
	m := validMessage()
	m.Text = ""
	m.HTML = "<p>" + strings.Repeat("日本語のテキスト", 20) + "</p>"
	m.Subject = "日本語の件名"
	got, err := BuildMIME(m, BuildOptions{Date: fixedDate, MessageIDDomain: "mail.example.net"})
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := mail.ReadMessage(bytes.NewReader(got.Data))
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Header.Get("Content-Transfer-Encoding") != "base64" || !strings.HasPrefix(parsed.Header.Get("Content-Type"), "text/html") {
		t.Fatalf("headers = %v", parsed.Header)
	}
	if !strings.HasPrefix(parsed.Header.Get("Subject"), "=?utf-8?b?") {
		t.Fatalf("subject = %q", parsed.Header.Get("Subject"))
	}
	if !strings.HasSuffix(got.MessageID, "@mail.example.net") {
		t.Fatalf("MessageID = %q", got.MessageID)
	}
	b, err := io.ReadAll(base64.NewDecoder(base64.StdEncoding, parsed.Body))
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != m.HTML {
		t.Fatalf("body = %q", b)
	}
}

func TestBuildMIMEQuotedPrintable(t *testing.T) {
	m := validMessage()
	m.Text = "Café\r\nline two\rline three"
	got, err := BuildMIME(m, BuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := mail.ReadMessage(bytes.NewReader(got.Data))
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Header.Get("Content-Transfer-Encoding") != "quoted-printable" {
		t.Fatalf("cte = %q", parsed.Header.Get("Content-Transfer-Encoding"))
	}
	b, err := io.ReadAll(quotedprintable.NewReader(parsed.Body))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimRight(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n") != "Café\nline two\nline three" {
		t.Fatalf("body = %q", b)
	}
	if parsed.Header.Get("Date") == "" || parsed.Header.Get("Message-Id") == "" {
		t.Fatal("missing Date or Message-ID")
	}
}

func TestBuildMIMEMessageIDFallbackDomain(t *testing.T) {
	m := validMessage()
	m.From = Address{Email: "no-reply@例子.广告"}
	got, err := BuildMIME(m, BuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(got.MessageID, "@omnimail.invalid") {
		t.Fatalf("MessageID = %q", got.MessageID)
	}
}

func TestBuildMIMEErrors(t *testing.T) {
	if _, err := BuildMIME(nil, BuildOptions{}); !errors.Is(err, ErrInvalidMessage) {
		t.Fatalf("nil: %v", err)
	}
	if _, err := BuildMIME(validMessage(), BuildOptions{MessageID: "a\r\nb"}); !errors.Is(err, ErrInvalidMessage) {
		t.Fatalf("bad id: %v", err)
	}
	m := validMessage()
	m.HTML = "<p>x</p>"
	if _, err := BuildMIME(m, BuildOptions{Boundary: strings.Repeat("x", 100)}); !errors.Is(err, ErrInvalidMessage) {
		t.Fatalf("bad boundary: %v", err)
	}
	got, err := BuildMIME(m, BuildOptions{Boundary: "fixed-boundary"})
	if err != nil || !bytes.Contains(got.Data, []byte("--fixed-boundary--")) {
		t.Fatalf("fixed boundary: %v", err)
	}
}

func TestHeaderFolding(t *testing.T) {
	var buf bytes.Buffer
	writeHeader(&buf, "Subject", strings.Repeat("word ", 40))
	for _, line := range strings.Split(strings.TrimSuffix(buf.String(), "\r\n"), "\r\n") {
		if len(line) > maxLineLen {
			t.Fatalf("line too long: %q", line)
		}
	}
	buf.Reset()
	var addrs []Address
	for range 10 {
		addrs = append(addrs, Address{Name: "Recipient Name", Email: "recipient@example.org"})
	}
	writeAddressHeader(&buf, "To", addrs)
	lines := strings.Split(strings.TrimSuffix(buf.String(), "\r\n"), "\r\n")
	if len(lines) < 2 {
		t.Fatalf("expected folding, got %q", buf.String())
	}
	for _, line := range lines {
		if len(line) > maxLineLen {
			t.Fatalf("line too long: %q", line)
		}
	}
	h, err := mail.ReadMessage(strings.NewReader(buf.String() + "\r\n"))
	if err != nil {
		t.Fatal(err)
	}
	list, err := h.Header.AddressList("To")
	if err != nil || len(list) != 10 {
		t.Fatalf("folded list = %v, %v", list, err)
	}
}

func TestEncodeHelpers(t *testing.T) {
	if nonASCIIRatio("") != 0 {
		t.Fatal("ratio of empty string")
	}
	if encodeHeaderText("plain") != "plain" {
		t.Fatal("ascii should pass through")
	}
	if !strings.HasPrefix(encodeHeaderText("naïve text"), "=?utf-8?q?") {
		t.Fatal("expected Q encoding")
	}
}
