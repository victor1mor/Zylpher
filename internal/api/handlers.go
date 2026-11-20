package api

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/victor1mor/Zylpher/internal/domain"
	"github.com/victor1mor/Zylpher/internal/engine"
)

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleListSessions(w http.ResponseWriter, r *http.Request) {
	sessions, err := s.sessions.List(r.Context())
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, sessions)
}

func (s *Server) handleGetSession(w http.ResponseWriter, r *http.Request) {
	session, err := s.sessions.Refresh(r.Context(), r.PathValue("name"))
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, session)
}

func (s *Server) handleGetSessionQR(w http.ResponseWriter, r *http.Request) {
	session, err := s.sessions.Get(r.Context(), r.PathValue("name"))
	if err != nil {
		writeDomainError(w, err)
		return
	}
	code, available, err := s.engine.QRCode(r.Context(), session)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	if !available {
		writeError(w, http.StatusNotFound, "qr_unavailable", "QR code is not available")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"session": session.Name, "code": code})
}

func (s *Server) handleCreateSession(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Name string `json:"name"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}

	session, err := s.sessions.Create(r.Context(), request.Name)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, session)
}

func (s *Server) handleStartSession(w http.ResponseWriter, r *http.Request) {
	session, err := s.sessions.Start(r.Context(), r.PathValue("name"))
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, session)
}

func (s *Server) handleStopSession(w http.ResponseWriter, r *http.Request) {
	session, err := s.sessions.Stop(r.Context(), r.PathValue("name"))
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, session)
}

func (s *Server) handleSendText(w http.ResponseWriter, r *http.Request) {
	var request domain.TextMessageRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	_, to, err := engine.ParseDestination(request.To)
	if err != nil || strings.TrimSpace(request.Text) == "" {
		writeDomainError(w, domain.ErrInvalidInput)
		return
	}
	request.To = to

	session, err := s.sessions.Get(r.Context(), r.PathValue("name"))
	if err != nil {
		writeDomainError(w, err)
		return
	}
	if session.Status != domain.SessionWorking {
		writeDomainError(w, domain.ErrSessionNotWorking)
		return
	}

	result, err := s.engine.SendText(r.Context(), session, request)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	s.dispatchWebhook(r, domain.WebhookEvent{
		ID:        result.ID,
		Event:     "message.sent",
		Session:   session.Name,
		Timestamp: result.Timestamp.UnixMilli(),
		Payload:   result,
	})
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) dispatchWebhook(r *http.Request, event domain.WebhookEvent) {
	if s.webhook == nil {
		return
	}

	// WhatsApp already accepted the send, even if the HTTP caller disconnected.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 5*time.Second)
	defer cancel()
	if err := s.webhook.Dispatch(ctx, event); err != nil {
		s.log.Warn("webhook dispatch failed", "event", event.Event)
	}
}
