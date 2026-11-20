package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const maxEventBytes = 1 << 20

func main() {
	secret := os.Getenv("ZYLPHER_WEBHOOK_SECRET")
	if secret == "" {
		log.Fatal("ZYLPHER_WEBHOOK_SECRET is required")
	}
	db, err := sql.Open("sqlite", "events.db")
	if err != nil {
		log.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	handler, err := newReceiver(ctx, db, []byte(secret))
	cancel()
	if err != nil {
		log.Fatal("could not initialize webhook store")
	}
	server := &http.Server{
		Addr:              "127.0.0.1:18441",
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
	}
	log.Fatal(server.ListenAndServe())
}

func newReceiver(ctx context.Context, db *sql.DB, secret []byte) (http.Handler, error) {
	// ponytail: one SQLite connection serializes writes; revisit if insertion latency limits delivery.
	db.SetMaxOpenConns(1)
	if _, err := db.ExecContext(ctx, `
		PRAGMA synchronous = FULL;
		CREATE TABLE IF NOT EXISTS events (
			digest BLOB PRIMARY KEY,
			body BLOB NOT NULL,
			received_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
		);`); err != nil {
		return nil, err
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /ready", func(w http.ResponseWriter, r *http.Request) {
		if err := db.PingContext(r.Context()); err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /webhook", func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxEventBytes))
		if err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				w.WriteHeader(http.StatusRequestEntityTooLarge)
				return
			}
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		mac := hmac.New(sha256.New, secret)
		_, _ = mac.Write(body)
		signature, ok := strings.CutPrefix(r.Header.Get("X-Zylpher-Signature"), "sha256=")
		rawSignature, err := hex.DecodeString(signature)
		if !ok || err != nil || !hmac.Equal(rawSignature, mac.Sum(nil)) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if !json.Valid(body) {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		digest := sha256.Sum256(body)
		if _, err := db.ExecContext(r.Context(), `INSERT INTO events (digest, body) VALUES (?, ?) ON CONFLICT(digest) DO NOTHING`, digest[:], body); err != nil {
			log.Print("webhook store write failed")
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	return mux, nil
}
