package api

import (
	"log/slog"
	"net/http"

	"github.com/victor1mor/Zylpher/internal/engine"
	"github.com/victor1mor/Zylpher/internal/media"
	"github.com/victor1mor/Zylpher/internal/session"
	"github.com/victor1mor/Zylpher/internal/webhook"
)

type Server struct {
	log      *slog.Logger
	sessions *session.Manager
	engine   engine.Engine
	webhook  *webhook.Dispatcher
	media    *media.Store
}

func NewRouter(log *slog.Logger, sessions *session.Manager, eng engine.Engine, webhooks *webhook.Dispatcher) http.Handler {
	return NewRouterWithMedia(log, sessions, eng, webhooks, nil)
}

// NewRouterWithMedia also serves the received attachments kept in store (nil disables them).
func NewRouterWithMedia(log *slog.Logger, sessions *session.Manager, eng engine.Engine, webhooks *webhook.Dispatcher, store *media.Store) http.Handler {
	if log == nil {
		log = slog.Default()
	}
	if sessions == nil {
		sessions = session.NewManager()
	}
	if eng == nil {
		eng = engine.NewFake()
	}

	server := &Server{log: log, sessions: sessions, engine: eng, webhook: webhooks, media: store}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", server.handleHealth)
	mux.HandleFunc("GET /metrics", server.handleMetrics)
	mux.HandleFunc("GET /sessions", server.handleListSessions)
	mux.HandleFunc("GET /sessions/{name}", server.handleGetSession)
	mux.HandleFunc("GET /sessions/{name}/qr", server.handleGetSessionQR)
	mux.HandleFunc("POST /sessions", server.handleCreateSession)
	mux.HandleFunc("POST /sessions/{name}/start", server.handleStartSession)
	mux.HandleFunc("POST /sessions/{name}/stop", server.handleStopSession)
	mux.HandleFunc("POST /sessions/{name}/logout", server.handleLogoutSession)
	mux.HandleFunc("POST /sessions/{name}/messages/text", server.handleSendText)
	mux.HandleFunc("POST /sessions/{name}/messages/media", server.handleSendMedia)
	mux.HandleFunc("GET /sessions/{name}/media/{id}", server.handleGetMedia)
	mux.HandleFunc("DELETE /sessions/{name}/media/{id}", server.handleDeleteMedia)
	return mux
}
