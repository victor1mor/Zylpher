package domain

import "errors"

var (
	ErrInvalidInput      = errors.New("invalid input")
	ErrNotFound          = errors.New("not found")
	ErrConflict          = errors.New("conflict")
	ErrSessionNotWorking = errors.New("session not working")
	ErrSendTimedOut      = errors.New("message send timed out")
	ErrRemoteFailure     = errors.New("remote message delivery failed")
	ErrTooLarge          = errors.New("media too large")
)

type ErrorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}
