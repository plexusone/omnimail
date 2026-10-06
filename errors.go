package omnimail

import (
	"context"
	"errors"
	"strings"
	"time"
)

// Kind classifies a send failure so callers can decide how to react without
// knowing the provider.
type Kind int

const (
	// KindUnknown is a failure that could not be classified.
	KindUnknown Kind = iota
	// KindInvalidMessage means the message failed validation (missing
	// recipients or body, header injection, malformed headers). Not retryable.
	KindInvalidMessage
	// KindInvalidAddress means an address is malformed or was refused by the
	// provider as undeliverable. Not retryable.
	KindInvalidAddress
	// KindRejected means the provider refused the message (policy, content,
	// suppression list, unverified sender). Not retryable as-is.
	KindRejected
	// KindThrottled means a rate or quota limit was hit. Retryable after a
	// delay; see [Error.RetryAfter].
	KindThrottled
	// KindTransient means a temporary failure (network, 4xx SMTP reply,
	// provider 5xx). Retryable.
	KindTransient
	// KindAuth means the provider rejected the sender's credentials or
	// permissions. Not retryable until configuration changes.
	KindAuth
)

var kindNames = map[Kind]string{
	KindUnknown:        "unknown",
	KindInvalidMessage: "invalid_message",
	KindInvalidAddress: "invalid_address",
	KindRejected:       "rejected",
	KindThrottled:      "throttled",
	KindTransient:      "transient",
	KindAuth:           "auth",
}

// String returns the snake_case name of the kind.
func (k Kind) String() string {
	if s, ok := kindNames[k]; ok {
		return s
	}
	return "unknown"
}

// Kinds returns every failure kind except KindUnknown, in declaration order.
func Kinds() []Kind {
	return []Kind{KindInvalidMessage, KindInvalidAddress, KindRejected, KindThrottled, KindTransient, KindAuth}
}

// Sentinel errors matched by [errors.Is] against an [*Error] of the
// corresponding kind. ErrInvalidMessage also matches KindInvalidAddress,
// because an unusable address makes the message invalid.
var (
	ErrInvalidMessage = errors.New("omnimail: invalid message")
	ErrInvalidAddress = errors.New("omnimail: invalid address")
	ErrRejected       = errors.New("omnimail: rejected")
	ErrThrottled      = errors.New("omnimail: throttled")
	ErrTransient      = errors.New("omnimail: transient failure")
	ErrAuth           = errors.New("omnimail: authentication failed")
)

var kindSentinels = map[Kind]error{
	KindInvalidMessage: ErrInvalidMessage,
	KindInvalidAddress: ErrInvalidAddress,
	KindRejected:       ErrRejected,
	KindThrottled:      ErrThrottled,
	KindTransient:      ErrTransient,
	KindAuth:           ErrAuth,
}

// Error is a classified send or validation failure. Providers return it (or
// an error wrapping it) so callers can branch on [Error.Kind] or use
// [errors.Is] with the sentinel errors.
type Error struct {
	// Kind classifies the failure.
	Kind Kind
	// Provider names the sender that produced the error ("smtp", "ses", ...).
	// Empty for validation errors raised by the core.
	Provider string
	// Code is the provider-specific code, e.g. an SMTP reply code ("550") or
	// an API error code ("Throttling").
	Code string
	// Field names the message field that failed validation, if any.
	Field string
	// Message is a human-readable description.
	Message string
	// RetryAfter is a provider-suggested delay before retrying, if known.
	RetryAfter time.Duration
	// Err is the underlying cause, if any.
	Err error
}

// NewError returns an *Error of the given kind for a provider, wrapping err.
func NewError(kind Kind, provider string, err error) *Error {
	return &Error{Kind: kind, Provider: provider, Err: err}
}

// Error implements the error interface.
func (e *Error) Error() string {
	var b strings.Builder
	b.WriteString("omnimail")
	if e.Provider != "" {
		b.WriteString("/")
		b.WriteString(e.Provider)
	}
	b.WriteString(": ")
	b.WriteString(e.Kind.String())
	if e.Code != "" {
		b.WriteString(" (")
		b.WriteString(e.Code)
		b.WriteString(")")
	}
	if e.Field != "" {
		b.WriteString(": ")
		b.WriteString(e.Field)
	}
	if e.Message != "" {
		b.WriteString(": ")
		b.WriteString(e.Message)
	}
	if e.Err != nil {
		b.WriteString(": ")
		b.WriteString(e.Err.Error())
	}
	return b.String()
}

// Unwrap returns the underlying cause.
func (e *Error) Unwrap() error { return e.Err }

// Is matches the sentinel error for the error's kind.
func (e *Error) Is(target error) bool {
	if target == ErrInvalidMessage {
		return e.Kind == KindInvalidMessage || e.Kind == KindInvalidAddress
	}
	s, ok := kindSentinels[e.Kind]
	return ok && s == target
}

// Temporary reports whether the failure condition is expected to clear on its
// own (throttling or a transient fault).
func (e *Error) Temporary() bool {
	return e.Kind == KindThrottled || e.Kind == KindTransient
}

// Retryable reports whether resending the same message may succeed. It is
// Temporary, except that failures caused by the caller's context being
// canceled or timing out are not retryable under that context.
func (e *Error) Retryable() bool {
	if !e.Temporary() {
		return false
	}
	return !errors.Is(e.Err, context.Canceled) && !errors.Is(e.Err, context.DeadlineExceeded)
}

// KindOf returns the kind of the first [*Error] in err's chain, or
// KindUnknown when there is none.
func KindOf(err error) Kind {
	var e *Error
	if errors.As(err, &e) {
		return e.Kind
	}
	return KindUnknown
}

// IsRetryable reports whether err is an [*Error] whose Retryable method
// returns true.
func IsRetryable(err error) bool {
	var e *Error
	return errors.As(err, &e) && e.Retryable()
}

// RetryAfter returns the provider-suggested retry delay carried by err, or 0.
func RetryAfter(err error) time.Duration {
	var e *Error
	if errors.As(err, &e) {
		return e.RetryAfter
	}
	return 0
}

func invalidMessage(field, reason string) *Error {
	return &Error{Kind: KindInvalidMessage, Field: field, Message: reason}
}
