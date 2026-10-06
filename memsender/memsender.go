// Package memsender provides an omnimail.Sender that captures messages in
// memory. It is intended for tests: assert on what an application sent
// without a mail server, and inject failures to exercise error handling.
//
//	s := memsender.New()
//	app := NewApp(s)
//	app.SignUp(ctx, "user@example.org")
//	if s.Len() != 1 || !strings.Contains(s.Last().Text, "/verify?") { ... }
package memsender

import (
	"context"
	"fmt"
	"sync"

	"github.com/plexusone/omnimail"
)

// ProviderName is reported in SendResult.Provider.
const ProviderName = "memory"

// Sender captures messages in memory. It is safe for concurrent use. The zero
// value is ready to use.
type Sender struct {
	mu       sync.Mutex
	messages []*omnimail.Message
	results  []omnimail.SendResult
	failures []error
	failFunc func(*omnimail.Message) error
	seq      int
}

var _ omnimail.Sender = (*Sender)(nil)

// New returns an empty Sender.
func New() *Sender { return &Sender{} }

// Send validates msg, honors ctx and records a deep copy of msg. Queued
// failures (FailNext) and the fail function (SetFailFunc) are applied after
// validation; a failed send records nothing.
func (s *Sender) Send(ctx context.Context, msg *omnimail.Message) (*omnimail.SendResult, error) {
	if err := omnimail.ValidateMessage(msg); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("memsender: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.failures) > 0 {
		err := s.failures[0]
		s.failures = s.failures[1:]
		return nil, err
	}
	if s.failFunc != nil {
		if err := s.failFunc(msg); err != nil {
			return nil, err
		}
	}
	s.seq++
	res := omnimail.SendResult{MessageID: fmt.Sprintf("memory-%d", s.seq), Provider: ProviderName}
	s.messages = append(s.messages, msg.Clone())
	s.results = append(s.results, res)
	return &res, nil
}

// Messages returns deep copies of the captured messages in send order.
func (s *Sender) Messages() []*omnimail.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*omnimail.Message, len(s.messages))
	for i, m := range s.messages {
		out[i] = m.Clone()
	}
	return out
}

// Results returns the SendResults in send order.
func (s *Sender) Results() []omnimail.SendResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]omnimail.SendResult(nil), s.results...)
}

// Last returns a copy of the most recently captured message, or nil.
func (s *Sender) Last() *omnimail.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.messages) == 0 {
		return nil
	}
	return s.messages[len(s.messages)-1].Clone()
}

// Len returns the number of captured messages.
func (s *Sender) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.messages)
}

// Reset discards captured messages, queued failures and the fail function.
func (s *Sender) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.messages = nil
	s.results = nil
	s.failures = nil
	s.failFunc = nil
}

// FailNext queues err to be returned by the next valid Send. Each queued
// error is used once, in order.
func (s *Sender) FailNext(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failures = append(s.failures, err)
}

// SetFailFunc installs fn, called for every valid Send (after queued
// failures). A non-nil return fails the send. Pass nil to remove it.
func (s *Sender) SetFailFunc(fn func(*omnimail.Message) error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failFunc = fn
}
