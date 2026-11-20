package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/victor1mor/Zylpher/internal/domain"
)

const maxBodyBytes = 64 << 10

func decodeJSON(w http.ResponseWriter, r *http.Request, value any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	if err := json.NewDecoder(r.Body).Decode(value); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "invalid JSON body")
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	body, err := json.Marshal(value)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "encode_error", "failed to encode response")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(append(body, '\n'))
}

func writeError(w http.ResponseWriter, status int, code string, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(domain.ErrorResponse{
		Error:   code,
		Message: message,
	})
}

func writeDomainError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, domain.ErrInvalidInput):
		writeError(w, http.StatusBadRequest, "invalid_input", err.Error())
	case errors.Is(err, domain.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", err.Error())
	case errors.Is(err, domain.ErrConflict):
		writeError(w, http.StatusConflict, "conflict", err.Error())
	case errors.Is(err, domain.ErrSessionNotWorking):
		writeError(w, http.StatusConflict, "session_not_working", "session must be WORKING")
	case errors.Is(err, domain.ErrSendTimedOut):
		writeError(w, http.StatusGatewayTimeout, "send_timeout", "message send timed out")
	case errors.Is(err, domain.ErrTooLarge):
		writeError(w, http.StatusRequestEntityTooLarge, "media_too_large", "media exceeds the configured limit")
	case errors.Is(err, domain.ErrRemoteFailure):
		writeError(w, http.StatusBadGateway, "remote_failure", "remote message delivery failed")
	default:
		writeError(w, http.StatusInternalServerError, "internal_error", "internal server error")
	}
}
