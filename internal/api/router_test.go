package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victor1mor/Zylpher/internal/domain"
	"github.com/victor1mor/Zylpher/internal/engine"
	"github.com/victor1mor/Zylpher/internal/session"
	"github.com/victor1mor/Zylpher/internal/storage"
	"github.com/victor1mor/Zylpher/internal/webhook"
)

func TestHealth(t *testing.T) {
	router := newTestRouter()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	res := httptest.NewRecorder()

	router.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", res.Code, http.StatusOK)
	}
	if contentType := res.Header().Get("Content-Type"); !strings.HasPrefix(contentType, "application/json") {
		t.Fatalf("content type = %q, want application/json", contentType)
	}
	if body := strings.TrimSpace(res.Body.String()); body != `{"status":"ok"}` {
		t.Fatalf("body = %s, want %s", body, `{"status":"ok"}`)
	}
}

func TestSessionsFlow(t *testing.T) {
	router := newTestRouter()

	created := doJSON[domain.Session](t, router, http.MethodPost, "/sessions", `{"name":"default"}`, http.StatusCreated)
	if created.Name != "default" || created.Status != domain.SessionStopped {
		t.Fatalf("created session = %#v", created)
	}

	started := doJSON[domain.Session](t, router, http.MethodPost, "/sessions/default/start", "", http.StatusOK)
	if started.Status != domain.SessionWorking {
		t.Fatalf("started status = %s, want %s", started.Status, domain.SessionWorking)
	}

	stopped := doJSON[domain.Session](t, router, http.MethodPost, "/sessions/default/stop", "", http.StatusOK)
	if stopped.Status != domain.SessionStopped {
		t.Fatalf("stopped status = %s, want %s", stopped.Status, domain.SessionStopped)
	}

	got := doJSON[domain.Session](t, router, http.MethodGet, "/sessions/default", "", http.StatusOK)
	if got.Name != "default" || got.Status != domain.SessionStopped {
		t.Fatalf("session = %#v", got)
	}
	doJSON[domain.ErrorResponse](t, router, http.MethodGet, "/sessions/default/qr", "", http.StatusNotFound)

	sessions := doJSON[[]domain.Session](t, router, http.MethodGet, "/sessions", "", http.StatusOK)
	if len(sessions) != 1 || sessions[0].Name != "default" {
		t.Fatalf("sessions = %#v, want default", sessions)
	}
}

func TestStoppedSessionIgnoresStaleEngineStatus(t *testing.T) {
	for _, source := range []string{"status_poll", "late_event"} {
		t.Run(source, func(t *testing.T) {
			eng := staleStatusEngine{engine.NewFake()}
			store := storage.NewFileSessionStore(filepath.Join(t.TempDir(), "sessions.json"))
			manager, err := session.NewManagerWithStoreAndEngine(t.Context(), store, eng)
			if err != nil {
				t.Fatal(err)
			}
			router := NewRouter(slog.New(slog.NewTextHandler(io.Discard, nil)), manager, eng, nil)
			doJSON[domain.Session](t, router, http.MethodPost, "/sessions", `{"name":"default"}`, http.StatusCreated)
			doJSON[domain.Session](t, router, http.MethodPost, "/sessions/default/start", "", http.StatusOK)
			stopped := doJSON[domain.Session](t, router, http.MethodPost, "/sessions/default/stop", "", http.StatusOK)
			if stopped.Status != domain.SessionStopped {
				t.Fatalf("stop status = %s, want STOPPED", stopped.Status)
			}

			if source == "late_event" {
				if err := manager.ApplyEngineStatus(t.Context(), "default", domain.SessionWorking); err != nil {
					t.Fatal(err)
				}
				sessions := doJSON[[]domain.Session](t, router, http.MethodGet, "/sessions", "", http.StatusOK)
				if len(sessions) != 1 || sessions[0].Status != domain.SessionStopped {
					t.Fatalf("sessions after late event = %#v, want one STOPPED session", sessions)
				}
			}

			got := doJSON[domain.Session](t, router, http.MethodGet, "/sessions/default", "", http.StatusOK)
			if got.Status != domain.SessionStopped || !got.UpdatedAt.Equal(stopped.UpdatedAt) {
				t.Fatalf("session after stop = %#v, want %#v", got, stopped)
			}
			repeatedStop := doJSON[domain.Session](t, router, http.MethodPost, "/sessions/default/stop", "", http.StatusOK)
			if repeatedStop.Status != domain.SessionStopped || !repeatedStop.UpdatedAt.Equal(stopped.UpdatedAt) {
				t.Fatalf("session after repeated stop = %#v, want %#v", repeatedStop, stopped)
			}
			persisted, err := store.Load(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if saved := persisted["default"]; saved.Status != domain.SessionStopped || !saved.UpdatedAt.Equal(stopped.UpdatedAt) {
				t.Fatalf("saved session = %#v, want %#v", saved, stopped)
			}

			restarted := doJSON[domain.Session](t, router, http.MethodPost, "/sessions/default/start", "", http.StatusOK)
			if restarted.Status != domain.SessionWorking {
				t.Fatalf("restart status = %s, want WORKING", restarted.Status)
			}
			if err := manager.ApplyEngineStatus(t.Context(), "default", domain.SessionStarting); err != nil {
				t.Fatal(err)
			}
			sessions := doJSON[[]domain.Session](t, router, http.MethodGet, "/sessions", "", http.StatusOK)
			if len(sessions) != 1 || sessions[0].Status != domain.SessionStarting {
				t.Fatalf("sessions after reconnect event = %#v, want one STARTING session", sessions)
			}
		})
	}
}

type staleStatusEngine struct{ *engine.Fake }

// WhatsApp can retain its logged-in flag after the socket is disconnected.
func (staleStatusEngine) Disconnect(ctx context.Context, _ domain.Session) error {
	return ctx.Err()
}

func TestCreateSessionErrors(t *testing.T) {
	router := newTestRouter()

	doJSON[domain.ErrorResponse](t, router, http.MethodPost, "/sessions", `{`, http.StatusBadRequest)
	doJSON[domain.ErrorResponse](t, router, http.MethodPost, "/sessions", `{"name":" "}`, http.StatusBadRequest)
	doJSON[domain.ErrorResponse](t, router, http.MethodPost, "/sessions", `{"name":"bad name"}`, http.StatusBadRequest)
	doJSON[domain.ErrorResponse](t, router, http.MethodPost, "/sessions", `{"name":"large","padding":"`+strings.Repeat("a", maxBodyBytes)+`"}`, http.StatusBadRequest)
	doJSON[domain.Session](t, router, http.MethodPost, "/sessions", `{"name":"default"}`, http.StatusCreated)
	doJSON[domain.ErrorResponse](t, router, http.MethodPost, "/sessions", `{"name":"default"}`, http.StatusConflict)
}

func TestStartMissingSession(t *testing.T) {
	router := newTestRouter()
	doJSON[domain.ErrorResponse](t, router, http.MethodPost, "/sessions/missing/start", "", http.StatusNotFound)
}

func TestSendTextFlow(t *testing.T) {
	router := newTestRouter()
	doJSON[domain.Session](t, router, http.MethodPost, "/sessions", `{"name":"default"}`, http.StatusCreated)
	doJSON[domain.Session](t, router, http.MethodPost, "/sessions/default/start", "", http.StatusOK)

	result := doJSON[domain.MessageResult](t, router, http.MethodPost, "/sessions/default/messages/text", `{"to":"+5511999999999","text":"oi"}`, http.StatusOK)
	if result.Session != "default" || result.To != "5511999999999" || result.Status != "sent" || result.ID == "" || result.Timestamp.IsZero() {
		t.Fatalf("message result = %#v", result)
	}
}

func TestSendTextDispatchesWebhook(t *testing.T) {
	received := make(chan domain.WebhookEvent, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var event domain.WebhookEvent
		if err := json.NewDecoder(r.Body).Decode(&event); err != nil {
			t.Errorf("decode webhook: %v", err)
		}
		received <- event
	}))
	defer server.Close()

	router := NewRouter(
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		session.NewManager(),
		engine.NewFake(),
		webhook.NewDispatcher(server.URL, "test-secret", server.Client()),
	)
	doJSON[domain.Session](t, router, http.MethodPost, "/sessions", `{"name":"default"}`, http.StatusCreated)
	doJSON[domain.Session](t, router, http.MethodPost, "/sessions/default/start", "", http.StatusOK)
	result := doJSON[domain.MessageResult](t, router, http.MethodPost, "/sessions/default/messages/text", `{"to":"5511999999999","text":"oi"}`, http.StatusOK)

	event := <-received
	if event.Event != "message.sent" || event.ID != result.ID || event.Session != "default" {
		t.Fatalf("webhook event = %#v", event)
	}
}

func TestSendTextFailureDoesNotDispatchWebhook(t *testing.T) {
	called := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called <- struct{}{}
	}))
	defer server.Close()

	router := NewRouter(
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		session.NewManager(),
		sendErrorEngine{Fake: engine.NewFake(), err: domain.ErrRemoteFailure},
		webhook.NewDispatcher(server.URL, "test-secret", server.Client()),
	)
	doJSON[domain.Session](t, router, http.MethodPost, "/sessions", `{"name":"default"}`, http.StatusCreated)
	doJSON[domain.Session](t, router, http.MethodPost, "/sessions/default/start", "", http.StatusOK)
	doJSON[domain.ErrorResponse](t, router, http.MethodPost, "/sessions/default/messages/text", `{"to":"5511999999999","text":"oi"}`, http.StatusBadGateway)

	select {
	case <-called:
		t.Fatal("webhook was dispatched after a send failure")
	default:
	}
}

func TestSendTextErrors(t *testing.T) {
	router := newTestRouter()
	doJSON[domain.ErrorResponse](t, router, http.MethodPost, "/sessions/missing/messages/text", `{"to":"5511999999999","text":"oi"}`, http.StatusNotFound)
	doJSON[domain.ErrorResponse](t, router, http.MethodPost, "/sessions/missing/messages/text", `{`, http.StatusBadRequest)
	doJSON[domain.ErrorResponse](t, router, http.MethodPost, "/sessions/missing/messages/text", `{"to":"","text":"oi"}`, http.StatusBadRequest)

	doJSON[domain.Session](t, router, http.MethodPost, "/sessions", `{"name":"default"}`, http.StatusCreated)
	doJSON[domain.ErrorResponse](t, router, http.MethodPost, "/sessions/default/messages/text", `{"to":"5511999999999","text":"oi"}`, http.StatusConflict)
	for _, body := range []string{
		`{"to":"+0123456789","text":"oi"}`,
		`{"to":"5511 99999999","text":"oi"}`,
		`{"to":"5511999999999999","text":"oi"}`,
	} {
		doJSON[domain.ErrorResponse](t, router, http.MethodPost, "/sessions/default/messages/text", body, http.StatusBadRequest)
	}
}

func TestSendTextMapsEngineErrors(t *testing.T) {
	for _, tt := range []struct {
		err        error
		wantStatus int
		wantCode   string
	}{
		{domain.ErrSessionNotWorking, http.StatusConflict, "session_not_working"},
		{domain.ErrInvalidInput, http.StatusBadRequest, "invalid_input"},
		{domain.ErrSendTimedOut, http.StatusGatewayTimeout, "send_timeout"},
		{domain.ErrRemoteFailure, http.StatusBadGateway, "remote_failure"},
	} {
		t.Run(tt.wantCode, func(t *testing.T) {
			manager := session.NewManager()
			router := NewRouter(
				slog.New(slog.NewTextHandler(io.Discard, nil)),
				manager,
				sendErrorEngine{Fake: engine.NewFake(), err: tt.err},
				nil,
			)
			doJSON[domain.Session](t, router, http.MethodPost, "/sessions", `{"name":"default"}`, http.StatusCreated)
			doJSON[domain.Session](t, router, http.MethodPost, "/sessions/default/start", "", http.StatusOK)
			response := doJSON[domain.ErrorResponse](t, router, http.MethodPost, "/sessions/default/messages/text", `{"to":"5511999999999","text":"oi"}`, tt.wantStatus)
			if response.Error != tt.wantCode {
				t.Fatalf("error code = %q, want %q", response.Error, tt.wantCode)
			}
		})
	}
}

type sendErrorEngine struct {
	*engine.Fake
	err error
}

func (e sendErrorEngine) SendText(context.Context, domain.Session, domain.TextMessageRequest) (domain.MessageResult, error) {
	return domain.MessageResult{}, e.err
}

func newTestRouter() http.Handler {
	return NewRouter(slog.New(slog.NewTextHandler(io.Discard, nil)), session.NewManager(), engine.NewFake(), nil)
}

func doJSON[T any](t *testing.T, handler http.Handler, method string, path string, body string, wantStatus int) T {
	t.Helper()

	var reader *bytes.Reader
	if body == "" {
		reader = bytes.NewReader(nil)
	} else {
		reader = bytes.NewReader([]byte(body))
	}
	req := httptest.NewRequest(method, path, reader)
	res := httptest.NewRecorder()

	handler.ServeHTTP(res, req)

	if res.Code != wantStatus {
		t.Fatalf("%s %s status = %d, want %d; body: %s", method, path, res.Code, wantStatus, res.Body.String())
	}

	var value T
	if err := json.NewDecoder(res.Body).Decode(&value); err != nil {
		t.Fatalf("decode response: %v; body: %s", err, res.Body.String())
	}
	return value
}
