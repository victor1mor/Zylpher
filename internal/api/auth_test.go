package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRequireAPIKey(t *testing.T) {
	handler := RequireAPIKey(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}), "secret")

	for _, test := range []struct {
		header string
		want   int
	}{
		{"", http.StatusUnauthorized},
		{"secret", http.StatusUnauthorized},
		{"Bearer wrong", http.StatusUnauthorized},
		{"Bearer secret", http.StatusNoContent},
	} {
		req := httptest.NewRequest(http.MethodGet, "/health", nil)
		req.Header.Set("Authorization", test.header)
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		if res.Code != test.want {
			t.Fatalf("header %q: status = %d, want %d", test.header, res.Code, test.want)
		}
	}
}
