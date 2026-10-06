package providertest

import (
	"strings"
	"testing"
	"time"

	"github.com/plexusone/omnimail"
)

func TestDecodeMIMERoundTrip(t *testing.T) {
	msg := &omnimail.Message{
		From:    omnimail.Address{Name: "Zoë", Email: "from@example.com"},
		To:      []omnimail.Address{{Name: "Doe, Jane", Email: "to@example.org"}},
		Cc:      []omnimail.Address{{Email: "cc@example.org"}},
		Bcc:     []omnimail.Address{{Email: "bcc@example.org"}},
		ReplyTo: []omnimail.Address{{Email: "reply@example.com"}},
		Subject: "日本語の件名 and more",
		Text:    "text café\n",
		HTML:    "<p>" + strings.Repeat("日本語", 30) + "</p>",
		Headers: map[string]string{"X-Note": "naïve", "List-Unsubscribe": "<mailto:u@example.com>"},
	}
	built, err := omnimail.BuildMIME(msg, omnimail.BuildOptions{Date: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeMIME(built.Data, []string{"to@example.org", "CC@example.org", "bcc@example.org"})
	if err != nil {
		t.Fatal(err)
	}
	if got.From != msg.From || got.Subject != msg.Subject || got.To[0] != msg.To[0] || got.Cc[0] != msg.Cc[0] || got.ReplyTo[0] != msg.ReplyTo[0] {
		t.Fatalf("headers = %+v", got)
	}
	if len(got.Bcc) != 1 || got.Bcc[0].Email != "bcc@example.org" {
		t.Fatalf("Bcc = %v", got.Bcc)
	}
	if NormalizeBody(got.Text) != NormalizeBody(msg.Text) || got.HTML != msg.HTML {
		t.Fatalf("bodies = %q / %q", got.Text, got.HTML)
	}
	if got.Headers["X-Note"] != "naïve" || got.Headers["List-Unsubscribe"] != "<mailto:u@example.com>" || len(got.Headers) != 2 {
		t.Fatalf("Headers = %v", got.Headers)
	}
}

func TestDecodeMIMEPlainAndLeakedBcc(t *testing.T) {
	raw := "From: a@example.com\r\nBcc: hidden@example.org\r\nSubject: s\r\n\r\nbody\r\n"
	got, err := DecodeMIME([]byte(raw), nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Text != "body\r\n" || got.Headers["Bcc"] != "hidden@example.org" || len(got.To) != 0 {
		t.Fatalf("got %+v", got)
	}
}

func TestDecodeMIMEErrors(t *testing.T) {
	bad := []string{
		"not a message",
		"From: <<bad\r\n\r\nx",
		"To: <<bad\r\n\r\nx",
		"Cc: <<bad\r\n\r\nx",
		"Reply-To: <<bad\r\n\r\nx",
		"Subject: =?bogus?q?x?=\r\n\r\nx",
		"X-A: =?bogus?q?x?=\r\n\r\nx",
		"Content-Type: ;;;\r\n\r\nx",
		"Content-Type: multipart/alternative; boundary=b\r\n\r\n--b\r\nContent-Type: ;;\r\n\r\nx\r\n--b--\r\n",
		"Content-Transfer-Encoding: base64\r\n\r\n!!!!",
	}
	for _, raw := range bad {
		if _, err := DecodeMIME([]byte(raw), nil); err == nil {
			t.Errorf("DecodeMIME(%q) should fail", raw)
		}
	}
}

func TestNormalizeBody(t *testing.T) {
	if NormalizeBody("a\r\nb\rc\n\n") != "a\nb\nc" {
		t.Fatal(NormalizeBody("a\r\nb\rc\n\n"))
	}
}
