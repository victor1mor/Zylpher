package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestReceiverPersistsOnlyAuthenticatedEvents(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	secret := []byte("receiver-test-key")
	handler, err := newReceiver(t.Context(), db, secret)
	if err != nil {
		t.Fatal(err)
	}
	const event = `{"event":"session.status","session":"demo","payload":{"status":"STOPPED"}}`
	post := func(body string, key []byte, want int) {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(body))
		mac := hmac.New(sha256.New, key)
		_, _ = mac.Write([]byte(body))
		req.Header.Set("X-Zylpher-Signature", fmt.Sprintf("sha256=%x", mac.Sum(nil)))
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		if res.Code != want {
			t.Fatalf("webhook HTTP status = %d, want %d", res.Code, want)
		}
	}
	post(event, []byte("wrong-key"), http.StatusUnauthorized)
	post("{", secret, http.StatusBadRequest)
	post(strings.Repeat("x", maxEventBytes+1), secret, http.StatusRequestEntityTooLarge)
	post(event, secret, http.StatusNoContent)
	post(event, secret, http.StatusNoContent)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	var count int
	var body []byte
	if err := reopened.QueryRowContext(t.Context(), "SELECT count(*), min(body) FROM events").Scan(&count, &body); err != nil {
		t.Fatal(err)
	}
	if count != 1 || !bytes.Equal(body, []byte(event)) {
		t.Fatalf("reopened store has count=%d, body=%q", count, body)
	}
}
