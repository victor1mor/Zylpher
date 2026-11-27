package engine

import (
	"context"

	"github.com/victor1mor/Zylpher/internal/domain"
)

type Engine interface {
	Connect(ctx context.Context, session domain.Session) error
	Disconnect(ctx context.Context, session domain.Session) error
	Logout(ctx context.Context, session domain.Session) error
	Status(ctx context.Context, session domain.Session) (domain.SessionStatus, error)
	QRCode(ctx context.Context, session domain.Session) (string, bool, error)
	SendText(ctx context.Context, session domain.Session, request domain.TextMessageRequest) (domain.MessageResult, error)
	SendMedia(ctx context.Context, session domain.Session, request domain.MediaMessageRequest) (domain.MessageResult, error)
}

type StatusHandler func(ctx context.Context, name string, status domain.SessionStatus)
