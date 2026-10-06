package smtp

import (
	"crypto/x509"
	"errors"
	"io"
	netsmtp "net/smtp"
	"net/textproto"
	"testing"

	"github.com/plexusone/omnimail"
)

func TestClassifyReply(t *testing.T) {
	tests := []struct {
		stage string
		code  int
		msg   string
		want  omnimail.Kind
	}{
		{"auth", 535, "5.7.8 Authentication credentials invalid", omnimail.KindAuth},
		{"mail", 530, "5.7.0 Authentication required", omnimail.KindAuth},
		{"auth", 554, "5.7.0 Denied", omnimail.KindAuth},
		{"auth", 504, "Unrecognized", omnimail.KindAuth},
		{"auth", 454, "4.7.0 Temporary authentication failure", omnimail.KindTransient},
		{"rcpt", 450, "4.2.1 Mailbox busy", omnimail.KindTransient},
		{"mail", 421, "4.7.28 Our system has detected an unusual rate", omnimail.KindThrottled},
		{"mail", 451, "4.4.5 Insufficient system storage", omnimail.KindThrottled},
		{"rcpt", 452, "Too many recipients", omnimail.KindThrottled},
		{"rcpt", 550, "5.1.1 No such user", omnimail.KindInvalidAddress},
		{"rcpt", 553, "Mailbox name not allowed", omnimail.KindInvalidAddress},
		{"mail", 501, "Bad sender address syntax", omnimail.KindInvalidAddress},
		{"mail", 553, "5.6.7 SMTPUTF8 required", omnimail.KindInvalidAddress},
		{"rcpt", 550, "5.7.1 Relaying denied", omnimail.KindRejected},
		{"message", 554, "5.7.1 Spam", omnimail.KindRejected},
		{"message", 552, "Message size exceeds limit", omnimail.KindRejected},
		{"ehlo", 250, "weird", omnimail.KindUnknown},
	}
	for _, tt := range tests {
		if got := classifyReply(tt.stage, tt.code, tt.msg); got != tt.want {
			t.Errorf("classifyReply(%s, %d, %q) = %s, want %s", tt.stage, tt.code, tt.msg, got, tt.want)
		}
	}
}

func TestClassifyErrors(t *testing.T) {
	if e := classify("rcpt", &textproto.Error{Code: 550, Msg: "5.1.1 unknown"}); e.Kind != omnimail.KindInvalidAddress || e.Code != "550" || e.Provider != ProviderName {
		t.Fatalf("textproto: %+v", e)
	}
	if e := classify("connect", io.EOF); e.Kind != omnimail.KindTransient {
		t.Fatalf("EOF: %+v", e)
	}
	if e := classify("tls", x509.UnknownAuthorityError{}); e.Kind != omnimail.KindUnknown {
		t.Fatalf("x509: %+v", e)
	}
	if e := classify("x", errors.New("other")); e.Kind != omnimail.KindUnknown {
		t.Fatalf("other: %+v", e)
	}
}

func TestAuthMechanisms(t *testing.T) {
	plain := &plainAuth{user: "u", pass: "p"}
	if _, _, err := plain.Start(&netsmtp.ServerInfo{}); err == nil {
		t.Fatal("PLAIN over plaintext should be refused")
	}
	proto, resp, err := plain.Start(&netsmtp.ServerInfo{TLS: true})
	if err != nil || proto != "PLAIN" || string(resp) != "\x00u\x00p" {
		t.Fatalf("PLAIN start = %q %q %v", proto, resp, err)
	}
	if _, err := plain.Next([]byte("?"), true); err == nil {
		t.Fatal("PLAIN challenge should fail")
	}
	if b, err := plain.Next(nil, false); b != nil || err != nil {
		t.Fatal("PLAIN final")
	}

	login := &loginAuth{user: "u", pass: "p", insecure: true}
	if proto, _, err := login.Start(&netsmtp.ServerInfo{}); err != nil || proto != "LOGIN" {
		t.Fatalf("LOGIN start = %q %v", proto, err)
	}
	if b, err := login.Next([]byte("Username:"), true); err != nil || string(b) != "u" {
		t.Fatal("LOGIN username")
	}
	if b, err := login.Next([]byte("Password:"), true); err != nil || string(b) != "p" {
		t.Fatal("LOGIN password")
	}
	if _, err := login.Next([]byte("Something else"), true); err == nil {
		t.Fatal("LOGIN unexpected challenge should fail")
	}
	if b, err := login.Next(nil, false); b != nil || err != nil {
		t.Fatal("LOGIN final")
	}
	if _, _, err := (&loginAuth{}).Start(&netsmtp.ServerInfo{}); err == nil {
		t.Fatal("LOGIN over plaintext should be refused")
	}
}
