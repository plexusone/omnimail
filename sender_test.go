package omnimail

import (
	"context"
	"testing"
)

func TestSenderFunc(t *testing.T) {
	var s Sender = SenderFunc(func(_ context.Context, msg *Message) (*SendResult, error) {
		return &SendResult{MessageID: msg.Subject, Provider: "func"}, nil
	})
	res, err := s.Send(context.Background(), &Message{Subject: "id"})
	if err != nil {
		t.Fatal(err)
	}
	if res.MessageID != "id" || res.Provider != "func" {
		t.Fatalf("got %+v", res)
	}
}
