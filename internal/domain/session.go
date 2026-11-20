package domain

import "time"

type SessionStatus string

const (
	SessionStopped  SessionStatus = "STOPPED"
	SessionStarting SessionStatus = "STARTING"
	SessionWorking  SessionStatus = "WORKING"
	SessionFailed   SessionStatus = "FAILED"
)

type Session struct {
	Name      string        `json:"name"`
	Status    SessionStatus `json:"status"`
	CreatedAt time.Time     `json:"created_at"`
	UpdatedAt time.Time     `json:"updated_at"`
}
