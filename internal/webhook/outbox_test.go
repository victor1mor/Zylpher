package webhook

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/victor1mor/Zylpher/internal/domain"
)

func TestPersistentDispatcherRestoresAndRetriesPendingEvent(t *testing.T) {
	const secret = "outbox-test-secret"
	type delivery struct {
		body string
		at   time.Time
	}
	received := make(chan delivery, 2)
	acknowledge := make(chan struct{})
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read event: %v", err)
		}
		mac := hmac.New(sha256.New, []byte(secret))
		_, _ = mac.Write(body)
		if got := r.Header.Get("X-Zylpher-Signature"); got != fmt.Sprintf("sha256=%x", mac.Sum(nil)) {
			t.Error("invalid signature for the persisted body")
		}
		select {
		case received <- delivery{body: string(body), at: time.Now()}:
		case <-r.Context().Done():
			return
		}
		if attempts.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		select {
		case <-acknowledge:
			w.WriteHeader(http.StatusNoContent)
		case <-r.Context().Done():
		}
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	path := filepath.Join(t.TempDir(), "data #1", "webhooks.db")
	first, err := OpenPersistentDispatcher(ctx, path, server.URL, secret, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	event := domain.WebhookEvent{ID: "msg-1", Event: "message.received", Session: "default", Timestamp: 42, Payload: map[string]string{"text": "oi"}}
	if err := first.Dispatch(ctx, event); err != nil {
		_ = first.Close()
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-received:
		t.Fatal("persisting the event made an HTTP request")
	default:
	}

	reopened, err := OpenPersistentDispatcher(ctx, path, server.URL, secret, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		reopened.Run(ctx, slog.New(slog.NewTextHandler(io.Discard, nil)))
	}()
	defer func() {
		cancel()
		<-done
		if err := reopened.Close(); err != nil {
			t.Error(err)
		}
	}()

	want, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	var firstAttempt time.Time
	for range 2 {
		select {
		case got := <-received:
			if got.body != string(want) {
				t.Fatalf("recovered body = %s, want %s", got.body, want)
			}
			if !firstAttempt.IsZero() && got.at.Sub(firstAttempt) < 900*time.Millisecond {
				t.Fatal("the failed delivery was retried without backoff")
			}
			firstAttempt = got.at
		case <-ctx.Done():
			t.Fatal("pending event was not recovered and retried")
		}
	}
	metrics, err := reopened.Metrics(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if metrics.Pending != 1 || metrics.DeliveryFailures != 1 || metrics.Retries != 1 || metrics.OldestAgeSeconds < 0.9 {
		t.Fatalf("metrics during retry = %+v", metrics)
	}
	close(acknowledge)

	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for metrics.Pending != 0 {
		select {
		case <-ticker.C:
		case <-ctx.Done():
			t.Fatal("successful retry did not clear the pending event")
		}
		metrics, err = reopened.Metrics(ctx)
		if err != nil {
			t.Fatal(err)
		}
	}
	if want := (QueueMetrics{DeliveryFailures: 1, Retries: 1}); metrics != want {
		t.Fatalf("metrics after delivery = %+v, want %+v", metrics, want)
	}
}

func TestPersistentDispatcherMetricsUpgradesLegacyQueue(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "legacy.db")
	const endpoint = "http://127.0.0.1/webhook"
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.ExecContext(ctx, `
		CREATE TABLE webhook_outbox (
			id INTEGER PRIMARY KEY AUTOINCREMENT, destination BLOB NOT NULL, body BLOB NOT NULL,
			next_attempt INTEGER NOT NULL, attempts INTEGER NOT NULL DEFAULT 0
		);
		CREATE INDEX webhook_outbox_due ON webhook_outbox (destination, next_attempt, id);
	`); err != nil {
		t.Fatal(err)
	}
	stamp := time.Now().Add(-45 * time.Second).UnixMilli()
	body := fmt.Appendf(nil, `{"id":"legacy","event":"message.received","timestamp":%d}`, stamp)
	destination := sha256.Sum256([]byte(endpoint))
	if _, err := db.ExecContext(ctx, `
		INSERT INTO webhook_outbox (destination, body, next_attempt, attempts) VALUES (?, ?, ?, 2)
	`, destination[:], body, time.Now().Add(time.Minute).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	other, err := OpenPersistentDispatcher(ctx, path, endpoint+"/different", "hook-secret", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if metrics, err := other.Metrics(ctx); err != nil || metrics != (QueueMetrics{}) {
		t.Fatalf("metrics for another receiver = %+v, err = %v", metrics, err)
	}
	d, err := OpenPersistentDispatcher(ctx, path, endpoint, "hook-secret", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if err := d.Dispatch(ctx, domain.WebhookEvent{ID: "new", Timestamp: time.Now().UnixMilli()}); err != nil {
		t.Fatal(err)
	}
	metrics, err := d.Metrics(ctx)
	if err != nil || metrics.Pending != 2 || metrics.DeliveryFailures != 0 || metrics.Retries != 0 ||
		metrics.OldestAgeSeconds < 44.9 || metrics.OldestAgeSeconds >= 120 {
		t.Fatalf("metrics after upgrading and reopening = %+v, err = %v", metrics, err)
	}
}
