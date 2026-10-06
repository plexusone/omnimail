package omnimail

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestKindString(t *testing.T) {
	want := map[Kind]string{
		KindUnknown: "unknown", KindInvalidMessage: "invalid_message", KindInvalidAddress: "invalid_address",
		KindRejected: "rejected", KindThrottled: "throttled", KindTransient: "transient", KindAuth: "auth",
		Kind(99): "unknown",
	}
	for k, s := range want {
		if k.String() != s {
			t.Errorf("Kind(%d).String() = %q, want %q", int(k), k.String(), s)
		}
	}
	if len(Kinds()) != 6 {
		t.Errorf("Kinds() = %v", Kinds())
	}
}

func TestErrorIsAndClassification(t *testing.T) {
	tests := []struct {
		kind      Kind
		sentinel  error
		temporary bool
	}{
		{KindInvalidMessage, ErrInvalidMessage, false},
		{KindInvalidAddress, ErrInvalidAddress, false},
		{KindRejected, ErrRejected, false},
		{KindThrottled, ErrThrottled, true},
		{KindTransient, ErrTransient, true},
		{KindAuth, ErrAuth, false},
	}
	for _, tt := range tests {
		err := fmt.Errorf("wrapped: %w", NewError(tt.kind, "test", errors.New("cause")))
		if !errors.Is(err, tt.sentinel) {
			t.Errorf("%v: errors.Is(sentinel) = false", tt.kind)
		}
		if KindOf(err) != tt.kind {
			t.Errorf("KindOf = %v, want %v", KindOf(err), tt.kind)
		}
		if IsRetryable(err) != tt.temporary {
			t.Errorf("%v: IsRetryable = %v", tt.kind, IsRetryable(err))
		}
		var e *Error
		if !errors.As(err, &e) || e.Temporary() != tt.temporary {
			t.Errorf("%v: Temporary mismatch", tt.kind)
		}
		for _, other := range tests {
			if other.kind == tt.kind || (other.sentinel == ErrInvalidMessage && tt.kind == KindInvalidAddress) {
				continue
			}
			if errors.Is(err, other.sentinel) {
				t.Errorf("%v unexpectedly matches %v", tt.kind, other.sentinel)
			}
		}
	}
	if !errors.Is(NewError(KindInvalidAddress, "", nil), ErrInvalidMessage) {
		t.Error("invalid address should match ErrInvalidMessage")
	}
	if KindOf(errors.New("plain")) != KindUnknown || IsRetryable(errors.New("plain")) {
		t.Error("plain errors should be unknown and not retryable")
	}
	if errors.Is(NewError(KindUnknown, "x", nil), ErrTransient) {
		t.Error("unknown kind should not match sentinels")
	}
}

func TestErrorRetryableContext(t *testing.T) {
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded} {
		e := NewError(KindTransient, "smtp", cause)
		if !e.Temporary() || e.Retryable() {
			t.Errorf("%v: Temporary=%v Retryable=%v", cause, e.Temporary(), e.Retryable())
		}
	}
}

func TestErrorMessageAndRetryAfter(t *testing.T) {
	e := &Error{Kind: KindThrottled, Provider: "ses", Code: "Throttling", Field: "to", Message: "slow down", RetryAfter: 2 * time.Second, Err: errors.New("429")}
	got := e.Error()
	for _, part := range []string{"omnimail/ses", "throttled", "(Throttling)", "to", "slow down", "429"} {
		if !strings.Contains(got, part) {
			t.Errorf("Error() = %q missing %q", got, part)
		}
	}
	if RetryAfter(fmt.Errorf("x: %w", e)) != 2*time.Second || RetryAfter(errors.New("x")) != 0 {
		t.Error("RetryAfter mismatch")
	}
	if !errors.Is(e, e.Err) {
		t.Error("Unwrap should expose the cause")
	}
	if got := (&Error{}).Error(); got != "omnimail: unknown" {
		t.Errorf("zero Error() = %q", got)
	}
}
