package omnimail

import "context"

// Sender delivers a message. Implementations must:
//
//   - validate the message (normally by calling [Message.Validate]) and
//     return an error matching [ErrInvalidMessage] without contacting the
//     provider when it is invalid;
//   - not modify msg;
//   - honor ctx: return promptly with an error matching ctx.Err() when ctx is
//     canceled or its deadline passes;
//   - classify provider failures with [*Error] so callers can use [KindOf],
//     [IsRetryable] and errors.Is with the sentinel errors;
//   - be safe for concurrent use.
//
// The providertest package verifies these rules.
type Sender interface {
	Send(ctx context.Context, msg *Message) (*SendResult, error)
}

// SendResult describes an accepted message.
type SendResult struct {
	// MessageID is the identifier assigned to the message: the provider's ID
	// when it returns one, otherwise the RFC 5322 Message-ID.
	MessageID string `json:"messageId"`
	// Provider names the sender that accepted the message ("smtp", "ses", ...).
	Provider string `json:"provider"`
}

// SenderFunc adapts a function to the [Sender] interface.
type SenderFunc func(ctx context.Context, msg *Message) (*SendResult, error)

// Send calls f(ctx, msg).
func (f SenderFunc) Send(ctx context.Context, msg *Message) (*SendResult, error) {
	return f(ctx, msg)
}
