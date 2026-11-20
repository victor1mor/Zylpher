package engine

import (
	"context"
	"errors"
	"testing"

	"github.com/victor1mor/Zylpher/internal/domain"
)

func TestFakeSendText(t *testing.T) {
	fake := NewFake()
	session := domain.Session{Name: "default"}

	result, err := fake.SendText(context.Background(), session, domain.TextMessageRequest{
		To:   "5511999999999",
		Text: "oi",
	})
	if err != nil {
		t.Fatalf("SendText returned error: %v", err)
	}
	if result.ID != "fake-default-1" || result.Session != "default" || result.Status != "sent" {
		t.Fatalf("result = %#v", result)
	}
}

func TestFakeSendTextRejectsBlankFields(t *testing.T) {
	_, err := NewFake().SendText(context.Background(), domain.Session{Name: "default"}, domain.TextMessageRequest{})
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("SendText error = %v, want %v", err, domain.ErrInvalidInput)
	}
}

func TestFakeLifecycle(t *testing.T) {
	fake := NewFake()
	session := domain.Session{Name: "default"}

	if err := fake.Connect(context.Background(), session); err != nil {
		t.Fatal(err)
	}
	if status, err := fake.Status(context.Background(), session); err != nil || status != domain.SessionWorking {
		t.Fatalf("status = %s, %v", status, err)
	}
	if err := fake.Disconnect(context.Background(), session); err != nil {
		t.Fatal(err)
	}
	if status, err := fake.Status(context.Background(), session); err != nil || status != domain.SessionStopped {
		t.Fatalf("status = %s, %v", status, err)
	}
}
