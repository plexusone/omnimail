package omnimail

import (
	"fmt"
	"net/mail"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Address is an email address with an optional display name.
//
// Email holds the bare addr-spec (local@domain). Internationalized addresses
// (UTF-8 local parts or domains, RFC 6531/6532) are accepted and passed
// through unchanged; delivering them requires a provider or SMTP server that
// supports SMTPUTF8. Display names may contain any printable Unicode text and
// are RFC 2047 encoded when a message is assembled.
type Address struct {
	Name  string `json:"name,omitempty"`
	Email string `json:"email"`
}

// ParseAddress parses a single RFC 5322 address such as
// "Jane Doe <jane@example.com>" or "jane@example.com".
func ParseAddress(s string) (Address, error) {
	if hasLineBreak(s) {
		return Address{}, invalidAddress("address", s, "contains a line break")
	}
	a, err := mail.ParseAddress(s)
	if err != nil {
		return Address{}, invalidAddressErr("address", s, err)
	}
	addr := Address{Name: a.Name, Email: a.Address}
	if err := addr.Validate(); err != nil {
		return Address{}, err
	}
	return addr, nil
}

// MustParseAddress is like ParseAddress but panics on error. It is intended
// for addresses that are compile-time constants.
func MustParseAddress(s string) Address {
	a, err := ParseAddress(s)
	if err != nil {
		panic(err)
	}
	return a
}

// ParseAddressList parses a comma-separated list of RFC 5322 addresses.
func ParseAddressList(s string) ([]Address, error) {
	if hasLineBreak(s) {
		return nil, invalidAddress("address", s, "contains a line break")
	}
	list, err := mail.ParseAddressList(s)
	if err != nil {
		return nil, invalidAddressErr("address", s, err)
	}
	out := make([]Address, 0, len(list))
	for _, a := range list {
		addr := Address{Name: a.Name, Email: a.Address}
		if err := addr.Validate(); err != nil {
			return nil, err
		}
		out = append(out, addr)
	}
	return out, nil
}

// Validate reports whether the address is well formed. It returns an
// [*Error] of kind [KindInvalidAddress] when it is not.
func (a Address) Validate() error {
	return a.validate("address")
}

func (a Address) validate(field string) error {
	if a.Email == "" {
		return invalidAddress(field, a.Email, "email is empty")
	}
	if !utf8.ValidString(a.Email) || !utf8.ValidString(a.Name) {
		return invalidAddress(field, a.Email, "not valid UTF-8")
	}
	if hasControl(a.Email) || hasControl(a.Name) {
		return invalidAddress(field, a.Email, "contains control characters")
	}
	parsed, err := mail.ParseAddress(a.Email)
	if err != nil {
		return invalidAddressErr(field, a.Email, err)
	}
	if parsed.Name != "" || parsed.Address != a.Email {
		return invalidAddress(field, a.Email, "email must be a bare addr-spec without display name or brackets")
	}
	at := strings.LastIndexByte(a.Email, '@')
	if at <= 0 || at == len(a.Email)-1 {
		return invalidAddress(field, a.Email, "missing local part or domain")
	}
	return nil
}

// Domain returns the part of the email after the last '@', or "" when there
// is none.
func (a Address) Domain() string {
	at := strings.LastIndexByte(a.Email, '@')
	if at < 0 {
		return ""
	}
	return a.Email[at+1:]
}

// IsASCII reports whether the email (not the display name) is pure ASCII.
// Non-ASCII addresses require SMTPUTF8 support from the delivery path.
func (a Address) IsASCII() bool {
	return isASCII(a.Email)
}

// String formats the address for an RFC 5322 header. A display name is
// quoted or RFC 2047 encoded as needed. An address without a display name is
// returned as the bare email.
func (a Address) String() string {
	if a.Name == "" {
		return a.Email
	}
	return (&mail.Address{Name: a.Name, Address: a.Email}).String()
}

func hasLineBreak(s string) bool {
	return strings.ContainsAny(s, "\r\n")
}

func hasControl(s string) bool {
	for _, r := range s {
		if unicode.IsControl(r) {
			return true
		}
	}
	return false
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}

func invalidAddress(field, email, reason string) *Error {
	return &Error{
		Kind:    KindInvalidAddress,
		Field:   field,
		Message: fmt.Sprintf("invalid address %q: %s", email, reason),
	}
}

func invalidAddressErr(field, email string, err error) *Error {
	return &Error{
		Kind:    KindInvalidAddress,
		Field:   field,
		Message: fmt.Sprintf("invalid address %q", email),
		Err:     err,
	}
}
