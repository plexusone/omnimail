package omnimail

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"maps"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/textproto"
	"slices"
	"strings"
	"time"
)

// maxLineLen is the RFC 5322 recommended maximum line length (excluding CRLF).
const maxLineLen = 78

// BuildOptions controls [BuildMIME]. The zero value is valid.
type BuildOptions struct {
	// Date is written to the Date header. Zero means time.Now().
	Date time.Time
	// MessageID is the Message-ID without angle brackets. Empty means a
	// random ID is generated.
	MessageID string
	// MessageIDDomain is the right-hand side of a generated Message-ID.
	// Empty means the From domain when it is ASCII, otherwise
	// "omnimail.invalid".
	MessageIDDomain string
	// Boundary fixes the multipart boundary (useful for golden tests).
	// Empty means a random boundary.
	Boundary string
}

// MIMEMessage is an assembled RFC 5322 message.
type MIMEMessage struct {
	// MessageID is the Message-ID header value without angle brackets.
	MessageID string
	// Data is the full message (headers and body) with CRLF line endings,
	// ready for SMTP DATA or a provider's raw-message API. Bcc recipients are
	// never included.
	Data []byte
}

// BuildMIME validates msg and assembles it into an RFC 5322 message: a
// text/plain or text/html single part, or multipart/alternative when both
// bodies are set. Bodies are UTF-8 and encoded 7bit, quoted-printable or
// base64, whichever is safest and smallest. Non-ASCII header values (subject,
// display names, custom headers) are RFC 2047 encoded and long headers are
// folded. Bcc is used only for the envelope and never written to a header.
//
// Tags and IdempotencyKey are provider metadata and are not written.
func BuildMIME(msg *Message, opts BuildOptions) (*MIMEMessage, error) {
	if err := ValidateMessage(msg); err != nil {
		return nil, err
	}
	date := opts.Date
	if date.IsZero() {
		date = time.Now()
	}
	id := strings.Trim(opts.MessageID, "<>")
	if id == "" {
		domain := opts.MessageIDDomain
		if domain == "" {
			domain = msg.From.Domain()
			if !isASCII(domain) || domain == "" {
				domain = "omnimail.invalid"
			}
		}
		id = strings.ToLower(rand.Text()) + "@" + domain
	}
	if err := checkHeaderValue("messageId", id); err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	writeHeader(&buf, "Date", date.Format(time.RFC1123Z))
	writeHeader(&buf, "Message-ID", "<"+id+">")
	writeAddressHeader(&buf, "From", []Address{msg.From})
	writeAddressHeader(&buf, "Reply-To", msg.ReplyTo)
	writeAddressHeader(&buf, "To", msg.To)
	writeAddressHeader(&buf, "Cc", msg.Cc)
	writeHeader(&buf, "Subject", encodeHeaderText(msg.Subject))
	writeHeader(&buf, "MIME-Version", "1.0")
	for _, name := range slices.Sorted(maps.Keys(msg.Headers)) {
		writeHeader(&buf, textproto.CanonicalMIMEHeaderKey(name), encodeHeaderText(msg.Headers[name]))
	}

	switch {
	case msg.Text != "" && msg.HTML != "":
		mw := multipart.NewWriter(&buf)
		if opts.Boundary != "" {
			if err := mw.SetBoundary(opts.Boundary); err != nil {
				return nil, invalidMessage("boundary", err.Error())
			}
		} else if err := mw.SetBoundary("omnimail-" + strings.ToLower(rand.Text())); err != nil {
			panic(err) // generated boundary is always valid
		}
		writeHeader(&buf, "Content-Type", mime.FormatMediaType("multipart/alternative", map[string]string{"boundary": mw.Boundary()}))
		buf.WriteString("\r\n")
		for _, p := range []struct{ ctype, body string }{{"text/plain", msg.Text}, {"text/html", msg.HTML}} {
			cte, encoded := encodeBody(p.body)
			h := textproto.MIMEHeader{}
			h.Set("Content-Type", p.ctype+"; charset=utf-8")
			h.Set("Content-Transfer-Encoding", cte)
			w, err := mw.CreatePart(h)
			if err != nil {
				return nil, fmt.Errorf("omnimail: create MIME part: %w", err)
			}
			if _, err := w.Write(encoded); err != nil {
				return nil, fmt.Errorf("omnimail: write MIME part: %w", err)
			}
		}
		if err := mw.Close(); err != nil {
			return nil, fmt.Errorf("omnimail: close multipart: %w", err)
		}
	default:
		ctype, body := "text/plain", msg.Text
		if body == "" {
			ctype, body = "text/html", msg.HTML
		}
		cte, encoded := encodeBody(body)
		writeHeader(&buf, "Content-Type", ctype+"; charset=utf-8")
		writeHeader(&buf, "Content-Transfer-Encoding", cte)
		buf.WriteString("\r\n")
		buf.Write(encoded)
	}
	if !bytes.HasSuffix(buf.Bytes(), []byte("\r\n")) {
		buf.WriteString("\r\n")
	}
	return &MIMEMessage{MessageID: id, Data: buf.Bytes()}, nil
}

// encodeHeaderText RFC 2047 encodes an unstructured header value when it is
// not plain ASCII, choosing B encoding for mostly non-ASCII text.
func encodeHeaderText(s string) string {
	if isASCII(s) {
		return s
	}
	if nonASCIIRatio(s) > 0.3 {
		return mime.BEncoding.Encode("utf-8", s)
	}
	return mime.QEncoding.Encode("utf-8", s)
}

func writeAddressHeader(buf *bytes.Buffer, name string, addrs []Address) {
	if len(addrs) == 0 {
		return
	}
	tokens := make([]string, len(addrs))
	for i, a := range addrs {
		tokens[i] = a.String()
	}
	line := name + ":"
	lineLen := len(line)
	buf.WriteString(line)
	for i, t := range tokens {
		if i < len(tokens)-1 {
			t += ","
		}
		if lineLen+1+len(t) > maxLineLen && lineLen > len(name)+1 {
			buf.WriteString("\r\n")
			lineLen = 0
		}
		buf.WriteString(" ")
		buf.WriteString(t)
		lineLen += 1 + len(t)
	}
	buf.WriteString("\r\n")
}

// writeHeader writes "Name: value" folding at spaces so lines stay within
// maxLineLen where the value allows it.
func writeHeader(buf *bytes.Buffer, name, value string) {
	buf.WriteString(name)
	buf.WriteString(":")
	lineLen := len(name) + 1
	words := strings.Split(value, " ")
	for i, w := range words {
		if i > 0 && lineLen+1+len(w) > maxLineLen {
			buf.WriteString("\r\n")
			lineLen = 0
		}
		buf.WriteString(" ")
		buf.WriteString(w)
		lineLen += 1 + len(w)
	}
	buf.WriteString("\r\n")
}

// encodeBody normalizes line endings and picks a transfer encoding:
// 7bit for short-lined ASCII, base64 for mostly non-ASCII text and
// quoted-printable otherwise.
func encodeBody(body string) (string, []byte) {
	body = normalizeNewlines(body)
	if isASCII(body) && maxLine(body) <= maxLineLen {
		return "7bit", []byte(strings.ReplaceAll(body, "\n", "\r\n"))
	}
	if nonASCIIRatio(body) > 0.3 {
		enc := base64.StdEncoding.EncodeToString([]byte(strings.ReplaceAll(body, "\n", "\r\n")))
		var b bytes.Buffer
		for len(enc) > 76 {
			b.WriteString(enc[:76])
			b.WriteString("\r\n")
			enc = enc[76:]
		}
		b.WriteString(enc)
		return "base64", b.Bytes()
	}
	var b bytes.Buffer
	w := quotedprintable.NewWriter(&b)
	if _, err := w.Write([]byte(body)); err != nil {
		panic(err) // writes to bytes.Buffer cannot fail
	}
	if err := w.Close(); err != nil {
		panic(err) // writes to bytes.Buffer cannot fail
	}
	return "quoted-printable", b.Bytes()
}

func normalizeNewlines(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.ReplaceAll(s, "\r", "\n")
}

func maxLine(s string) int {
	longest := 0
	for line := range strings.SplitSeq(s, "\n") {
		longest = max(longest, len(line))
	}
	return longest
}

func nonASCIIRatio(s string) float64 {
	if s == "" {
		return 0
	}
	n := 0
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			n++
		}
	}
	return float64(n) / float64(len(s))
}
