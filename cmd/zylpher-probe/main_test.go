package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRenderQRURL(t *testing.T) {
	t.Setenv("ZYLPHER_API_KEY", "test-key")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-key" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":"test-qr"}`))
	}))
	defer server.Close()

	var out bytes.Buffer
	if err := renderQRURL(server.URL, &out); err != nil {
		t.Fatal(err)
	}
	if out.Len() == 0 {
		t.Fatal("expected QR rendering")
	}
}
