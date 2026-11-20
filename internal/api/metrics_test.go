package api

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/victor1mor/Zylpher/internal/domain"
	"github.com/victor1mor/Zylpher/internal/webhook"
)

func TestWebhookMetricsRequireAuthAndReportPendingEvent(t *testing.T) {
	d, err := webhook.OpenPersistentDispatcher(t.Context(), filepath.Join(t.TempDir(), "webhooks.db"), "http://127.0.0.1/webhook", "hook-secret", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	event := domain.WebhookEvent{ID: "private-message-id", Event: "message.received", Session: "private-session", Timestamp: time.Now().Add(-24 * time.Hour).UnixMilli()}
	if err := d.Dispatch(t.Context(), event); err != nil {
		t.Fatal(err)
	}

	router := RequireAPIKey(NewRouter(nil, nil, nil, d), "api-secret")
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)
	if res.Code != http.StatusUnauthorized {
		t.Fatalf("without key: status = %d", res.Code)
	}

	req.Header.Set("Authorization", "Bearer api-secret")
	res = httptest.NewRecorder()
	router.ServeHTTP(res, req)
	if res.Code != http.StatusOK || !strings.HasPrefix(res.Header().Get("Content-Type"), "text/plain; version=0.0.4") {
		t.Fatalf("metrics response = %d %q: %s", res.Code, res.Header().Get("Content-Type"), res.Body)
	}
	body := res.Body.String()
	for _, line := range []string{
		"zylpher_webhook_pending 1\n",
		"zylpher_webhook_delivery_failures_total 0\n",
		"zylpher_webhook_retries_total 0\n",
	} {
		if !strings.Contains(body, line) {
			t.Fatalf("missing metric %q in %s", line, body)
		}
	}
	_, ageLine, found := strings.Cut(body, "\nzylpher_webhook_oldest_pending_age_seconds ")
	age, err := strconv.ParseFloat(strings.SplitN(ageLine, "\n", 2)[0], 64)
	if !found || err != nil || age < 0 || age >= 60 {
		t.Fatalf("oldest age = %v, found = %t, err = %v", age, found, err)
	}
	if strings.Contains(body, event.ID) || strings.Contains(body, event.Session) || strings.Contains(body, "/webhook") {
		t.Fatal("metrics leaked event or receiver information")
	}
}
