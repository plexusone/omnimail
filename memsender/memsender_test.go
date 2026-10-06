package memsender

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/plexusone/omnimail"
)

func msg(subject string) *omnimail.Message {
	return &omnimail.Message{
		From:    omnimail.Address{Email: "from@example.com"},
		To:      []omnimail.Address{{Email: "to@example.org"}},
		Subject: subject,
		Text:    "body",
		Headers: map[string]string{"X-A": "1"},
	}
}

func TestSendCapturesCopies(t *testing.T) {
	s := New()
	if s.Last() != nil || s.Len() != 0 {
		t.Fatal("new sender not empty")
	}
	m := msg("one")
	res, err := s.Send(context.Background(), m)
	if err != nil {
		t.Fatal(err)
	}
	if res.Provider != ProviderName || res.MessageID != "memory-1" {
		t.Fatalf("result = %+v", res)
	}
	m.Subject = "mutated"
	m.Headers["X-A"] = "2"
	got := s.Last()
	if got.Subject != "one" || got.Headers["X-A"] != "1" {
		t.Fatal("captured message shares state with caller")
	}
	got.Subject = "mutated"
	if s.Messages()[0].Subject != "one" {
		t.Fatal("Messages returns shared state")
	}
	if len(s.Results()) != 1 {
		t.Fatal("Results")
	}
	s.Reset()
	if s.Len() != 0 || len(s.Results()) != 0 {
		t.Fatal("Reset did not clear")
	}
}

func TestSendValidationAndContext(t *testing.T) {
	var s Sender // zero value usable
	if _, err := s.Send(context.Background(), nil); !errors.Is(err, omnimail.ErrInvalidMessage) {
		t.Fatalf("nil message: %v", err)
	}
	bad := msg("x")
	bad.To = nil
	if _, err := s.Send(context.Background(), bad); !errors.Is(err, omnimail.ErrInvalidMessage) {
		t.Fatalf("invalid message: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Send(ctx, msg("x")); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled: %v", err)
	}
	if s.Len() != 0 {
		t.Fatal("failed sends must not be captured")
	}
}

func TestFailures(t *testing.T) {
	s := New()
	e1 := omnimail.NewError(omnimail.KindThrottled, ProviderName, nil)
	e2 := omnimail.NewError(omnimail.KindRejected, ProviderName, nil)
	s.FailNext(e1)
	s.FailNext(e2)
	if _, err := s.Send(context.Background(), msg("a")); !errors.Is(err, omnimail.ErrThrottled) {
		t.Fatalf("first: %v", err)
	}
	if _, err := s.Send(context.Background(), msg("a")); !errors.Is(err, omnimail.ErrRejected) {
		t.Fatalf("second: %v", err)
	}
	if _, err := s.Send(context.Background(), msg("a")); err != nil {
		t.Fatalf("third: %v", err)
	}
	s.SetFailFunc(func(m *omnimail.Message) error {
		if m.Subject == "fail" {
			return e2
		}
		return nil
	})
	if _, err := s.Send(context.Background(), msg("fail")); !errors.Is(err, e2) {
		t.Fatalf("fail func: %v", err)
	}
	if _, err := s.Send(context.Background(), msg("ok")); err != nil {
		t.Fatal(err)
	}
	if s.Len() != 2 {
		t.Fatalf("Len = %d", s.Len())
	}
}

func TestConcurrentSends(t *testing.T) {
	s := New()
	var wg sync.WaitGroup
	for range 50 {
		wg.Go(func() {
			if _, err := s.Send(context.Background(), msg("c")); err != nil {
				t.Error(err)
			}
			_ = s.Messages()
		})
	}
	wg.Wait()
	if s.Len() != 50 {
		t.Fatalf("Len = %d", s.Len())
	}
}
