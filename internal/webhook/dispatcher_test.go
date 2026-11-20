package webhook

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/victor1mor/Zylpher/internal/domain"
)

func TestDispatcherDispatchesEvent(t *testing.T) {
	received := make(chan domain.WebhookEvent, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("content type = %q, want application/json", r.Header.Get("Content-Type"))
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read event: %v", err)
		}
		signature := hmac.New(sha256.New, []byte("test-secret"))
		_, _ = signature.Write(body)
		if r.Header.Get("X-Zylpher-Signature") != fmt.Sprintf("sha256=%x", signature.Sum(nil)) {
			t.Errorf("invalid webhook signature")
		}
		var event domain.WebhookEvent
		if err := json.Unmarshal(body, &event); err != nil {
			t.Errorf("decode event: %v", err)
		}
		received <- event
	}))
	defer server.Close()

	event := domain.WebhookEvent{ID: "msg-1", Event: "message.sent", Session: "default", Timestamp: 1}
	if err := NewDispatcher(server.URL, "test-secret", server.Client()).Dispatch(context.Background(), event); err != nil {
		t.Fatalf("Dispatch returned error: %v", err)
	}

	got := <-received
	if got.ID != event.ID || got.Event != event.Event || got.Session != event.Session {
		t.Fatalf("event = %#v, want %#v", got, event)
	}
}

func TestDispatcherNoURLIsNoop(t *testing.T) {
	if err := NewDispatcher("", "", nil).Dispatch(context.Background(), domain.WebhookEvent{}); err != nil {
		t.Fatalf("Dispatch returned error: %v", err)
	}
}

func TestDispatcherRequiresSecret(t *testing.T) {
	if err := NewDispatcher("http://127.0.0.1", "", nil).Dispatch(context.Background(), domain.WebhookEvent{}); err == nil {
		t.Fatal("Dispatch returned nil error without secret")
	}
}

func TestDispatcherRejectsBadStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusBadGateway)
	}))
	defer server.Close()

	err := NewDispatcher(server.URL, "test-secret", server.Client()).Dispatch(context.Background(), domain.WebhookEvent{ID: "msg-1"})
	if err == nil {
		t.Fatal("Dispatch returned nil error, want status error")
	}
}

func TestDispatcherHonorsContextTimeout(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	err := NewDispatcher(server.URL, "test-secret", server.Client()).Dispatch(ctx, domain.WebhookEvent{})
	close(release)
	if err == nil {
		t.Fatal("Dispatch returned nil error after timeout")
	}
}
