package smtp

import (
	"errors"
	"fmt"
	netsmtp "net/smtp"
	"strings"
)

type insecureError struct{ mech string }

func (e *insecureError) Error() string {
	return fmt.Sprintf("refusing AUTH %s over an unencrypted connection (set AllowInsecureAuth to permit)", e.mech)
}

// plainAuth implements AUTH PLAIN. Unlike net/smtp.PlainAuth it applies the
// same TLS requirement to every host and can be relaxed explicitly.
type plainAuth struct {
	user, pass string
	insecure   bool
}

func (a *plainAuth) Start(si *netsmtp.ServerInfo) (string, []byte, error) {
	if !si.TLS && !a.insecure {
		return "", nil, &insecureError{mech: "PLAIN"}
	}
	return "PLAIN", []byte("\x00" + a.user + "\x00" + a.pass), nil
}

func (a *plainAuth) Next(_ []byte, more bool) ([]byte, error) {
	if more {
		return nil, errors.New("smtp: unexpected server challenge during AUTH PLAIN")
	}
	return nil, nil
}

// loginAuth implements the AUTH LOGIN mechanism.
type loginAuth struct {
	user, pass string
	insecure   bool
}

func (a *loginAuth) Start(si *netsmtp.ServerInfo) (string, []byte, error) {
	if !si.TLS && !a.insecure {
		return "", nil, &insecureError{mech: "LOGIN"}
	}
	return "LOGIN", nil, nil
}

func (a *loginAuth) Next(fromServer []byte, more bool) ([]byte, error) {
	if !more {
		return nil, nil
	}
	prompt := strings.ToLower(strings.TrimSpace(string(fromServer)))
	switch {
	case strings.HasPrefix(prompt, "username"):
		return []byte(a.user), nil
	case strings.HasPrefix(prompt, "password"):
		return []byte(a.pass), nil
	default:
		return nil, fmt.Errorf("smtp: unexpected AUTH LOGIN challenge %q", fromServer)
	}
}
