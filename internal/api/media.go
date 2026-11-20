package api

import (
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/victor1mor/Zylpher/internal/domain"
	"github.com/victor1mor/Zylpher/internal/engine"
	"github.com/victor1mor/Zylpher/internal/media"
)

// defaultMaxMediaBytes applies when the router has no media store (sending only).
const defaultMaxMediaBytes = 16 << 20

const (
	maxCaptionRunes  = 1024
	maxFileNameRunes = 255
)

// allowedMedia lists the MIME types WhatsApp renders for each kind. Documents accept any type.
var allowedMedia = map[domain.MediaKind][]string{
	domain.MediaImage: {"image/jpeg", "image/png", "image/webp"},
	domain.MediaAudio: {"audio/ogg", "audio/mpeg", "audio/mp4", "audio/aac", "audio/amr"},
	domain.MediaVideo: {"video/mp4", "video/3gpp"},
}

func (s *Server) maxMediaBytes() int64 {
	if s.media != nil {
		return s.media.MaxBytes()
	}
	return defaultMaxMediaBytes
}

// mediaType validates the Content-Type for the kind. Voice notes must be OGG/Opus.
func mediaType(kind domain.MediaKind, contentType string, voice bool) (string, bool) {
	mediatype, params, err := mime.ParseMediaType(contentType)
	if err != nil || !kind.Valid() {
		return "", false
	}
	if voice && (kind != domain.MediaAudio || mediatype != "audio/ogg") {
		return "", false
	}
	if allowed, limited := allowedMedia[kind]; limited {
		found := false
		for _, candidate := range allowed {
			if mediatype == candidate {
				found = true
				break
			}
		}
		if !found {
			return "", false
		}
	}
	return mime.FormatMediaType(mediatype, params), true
}

func validFileName(name string) bool {
	return utf8.RuneCountInString(name) <= maxFileNameRunes && !strings.ContainsAny(name, `/\`) && !strings.ContainsFunc(name, func(r rune) bool { return r < 0x20 })
}

// handleSendMedia sends one attachment. The body is the raw file; everything else is in the
// query string: to, kind, caption, file_name and voice=true.
func (s *Server) handleSendMedia(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	_, to, err := engine.ParseDestination(query.Get("to"))
	kind := domain.MediaKind(query.Get("kind"))
	voice := query.Get("voice") == "true"
	contentType, typeOK := mediaType(kind, r.Header.Get("Content-Type"), voice)
	caption := query.Get("caption")
	fileName := query.Get("file_name")
	if err != nil || !typeOK || utf8.RuneCountInString(caption) > maxCaptionRunes || !validFileName(fileName) ||
		(caption != "" && (kind == domain.MediaAudio)) {
		writeDomainError(w, domain.ErrInvalidInput)
		return
	}

	session, err := s.sessions.Get(r.Context(), r.PathValue("name"))
	if err != nil {
		writeDomainError(w, err)
		return
	}
	if session.Status != domain.SessionWorking {
		writeDomainError(w, domain.ErrSessionNotWorking)
		return
	}

	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, s.maxMediaBytes()))
	var tooLarge *http.MaxBytesError
	switch {
	case errors.As(err, &tooLarge):
		writeDomainError(w, domain.ErrTooLarge)
		return
	case err != nil:
		writeError(w, http.StatusBadRequest, "invalid_body", "could not read the media body")
		return
	case len(data) == 0:
		writeDomainError(w, domain.ErrInvalidInput)
		return
	}

	result, err := s.engine.SendMedia(r.Context(), session, domain.MediaMessageRequest{
		To: to, Kind: kind, MimeType: contentType, Caption: caption, FileName: fileName, Voice: voice, Data: data,
	})
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

func (s *Server) mediaSession(w http.ResponseWriter, r *http.Request) (string, string, bool) {
	if s.media == nil {
		writeError(w, http.StatusNotFound, "media_disabled", "media store is not enabled")
		return "", "", false
	}
	id := r.PathValue("id")
	if !media.ValidID(id) {
		writeDomainError(w, domain.ErrInvalidInput)
		return "", "", false
	}
	session, err := s.sessions.Get(r.Context(), r.PathValue("name"))
	if err != nil {
		writeDomainError(w, err)
		return "", "", false
	}
	return session.Name, id, true
}

// handleGetMedia returns the bytes of a received attachment, never rendered by the browser.
func (s *Server) handleGetMedia(w http.ResponseWriter, r *http.Request) {
	session, id, ok := s.mediaSession(w, r)
	if !ok {
		return
	}
	item, data, err := s.media.Open(session, id)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	contentType := "application/octet-stream"
	if _, _, err := mime.ParseMediaType(item.MimeType); err == nil {
		contentType = item.MimeType
	}
	header := w.Header()
	header.Set("Content-Type", contentType)
	header.Set("Content-Length", strconv.Itoa(len(data)))
	header.Set("Content-Disposition", "attachment")
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("X-Zylpher-SHA256", item.SHA256)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

// handleDeleteMedia removes a fetched attachment from disk.
func (s *Server) handleDeleteMedia(w http.ResponseWriter, r *http.Request) {
	session, id, ok := s.mediaSession(w, r)
	if !ok {
		return
	}
	if err := s.media.Delete(session, id); err != nil {
		writeDomainError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
