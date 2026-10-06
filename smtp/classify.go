package smtp

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/textproto"
	"regexp"
	"strconv"
	"strings"

	"github.com/plexusone/omnimail"
)

var enhancedCode = regexp.MustCompile(`^[245]\.\d{1,3}\.\d{1,3}\b`)

var throttleHints = []string{"rate", "limit", "too many", "throttl", "slow down", "try again later"}

// classify maps an SMTP session error to an *omnimail.Error. stage is the
// session step that failed ("connect", "tls", "ehlo", "starttls", "auth",
// "mail", "rcpt", "data", "message").
func classify(stage string, err error) *omnimail.Error {
	e := &omnimail.Error{Kind: omnimail.KindUnknown, Provider: ProviderName, Message: stage + " failed", Err: err}

	var tpErr *textproto.Error
	if errors.As(err, &tpErr) {
		e.Code = strconv.Itoa(tpErr.Code)
		e.Kind = classifyReply(stage, tpErr.Code, tpErr.Msg)
		return e
	}

	var certErr *tls.CertificateVerificationError
	var unknownAuth x509.UnknownAuthorityError
	var hostErr x509.HostnameError
	if errors.As(err, &certErr) || errors.As(err, &unknownAuth) || errors.As(err, &hostErr) {
		// Certificate problems are configuration errors; retrying won't help.
		return e
	}

	var netErr net.Error
	if errors.As(err, &netErr) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, net.ErrClosed) {
		e.Kind = omnimail.KindTransient
		return e
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		e.Kind = omnimail.KindTransient
	}
	return e
}

// classifyReply classifies an SMTP reply code (RFC 5321) with its enhanced
// status code (RFC 3463) when present.
func classifyReply(stage string, code int, msg string) omnimail.Kind {
	enhanced := enhancedCode.FindString(strings.TrimSpace(msg))
	lower := strings.ToLower(msg)
	switch {
	case code == 530 || code == 534 || code == 535 || code == 538 ||
		strings.HasPrefix(enhanced, "5.7.8") || strings.HasPrefix(enhanced, "5.7.0") && stage == "auth":
		return omnimail.KindAuth
	case code >= 400 && code < 500:
		if enhanced == "4.7.28" || strings.HasPrefix(enhanced, "4.4.5") {
			return omnimail.KindThrottled
		}
		for _, h := range throttleHints {
			if strings.Contains(lower, h) {
				return omnimail.KindThrottled
			}
		}
		return omnimail.KindTransient
	case code >= 500 && code < 600:
		if strings.HasPrefix(enhanced, "5.1.") || strings.HasPrefix(enhanced, "5.6.7") {
			return omnimail.KindInvalidAddress
		}
		if enhanced == "" && (stage == "rcpt" || stage == "mail") &&
			(code == 550 || code == 551 || code == 553 || code == 501) {
			return omnimail.KindInvalidAddress
		}
		if stage == "auth" {
			return omnimail.KindAuth
		}
		return omnimail.KindRejected
	}
	return omnimail.KindUnknown
}
