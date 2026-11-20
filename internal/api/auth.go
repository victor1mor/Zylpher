package api

import (
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
	"strings"
)

func RequireAPIKey(next http.Handler, key string) http.Handler {
	expected := sha256.Sum256([]byte(key))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		provided, bearer := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		actual := sha256.Sum256([]byte(provided))
		if !bearer || key == "" || subtle.ConstantTimeCompare(actual[:], expected[:]) != 1 {
			writeError(w, http.StatusUnauthorized, "unauthorized", "valid API key required")
			return
		}
		next.ServeHTTP(w, r)
	})
}
