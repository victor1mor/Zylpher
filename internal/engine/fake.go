package engine

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/victor1mor/Zylpher/internal/domain"
)

type Fake struct {
	mu       sync.Mutex
	next     int
	statuses map[string]domain.SessionStatus
}

func NewFake() *Fake {
	return &Fake{statuses: make(map[string]domain.SessionStatus)}
}

func (f *Fake) Connect(ctx context.Context, session domain.Session) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f.mu.Lock()
	f.statuses[session.Name] = domain.SessionWorking
	f.mu.Unlock()
	return nil
}

func (f *Fake) Disconnect(ctx context.Context, session domain.Session) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f.mu.Lock()
	f.statuses[session.Name] = domain.SessionStopped
	f.mu.Unlock()
	return nil
}

func (f *Fake) Logout(ctx context.Context, session domain.Session) error {
	return f.Disconnect(ctx, session)
}

func (f *Fake) Status(ctx context.Context, session domain.Session) (domain.SessionStatus, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	status, exists := f.statuses[session.Name]
	if !exists {
		return domain.SessionStopped, nil
	}
	return status, nil
}

func (f *Fake) QRCode(ctx context.Context, session domain.Session) (string, bool, error) {
	if err := ctx.Err(); err != nil {
		return "", false, err
	}
	return "", false, nil
}

func (f *Fake) SendMedia(ctx context.Context, session domain.Session, request domain.MediaMessageRequest) (domain.MessageResult, error) {
	if len(request.Data) == 0 || !request.Kind.Valid() {
		return domain.MessageResult{}, domain.ErrInvalidInput
	}
	return f.SendText(ctx, session, domain.TextMessageRequest{To: request.To, Text: string(request.Kind)})
}

func (f *Fake) SendText(ctx context.Context, session domain.Session, request domain.TextMessageRequest) (domain.MessageResult, error) {
	if err := ctx.Err(); err != nil {
		return domain.MessageResult{}, err
	}
	if strings.TrimSpace(request.To) == "" || strings.TrimSpace(request.Text) == "" {
		return domain.MessageResult{}, domain.ErrInvalidInput
	}

	f.mu.Lock()
	f.next++
	id := fmt.Sprintf("fake-%s-%d", session.Name, f.next)
	f.mu.Unlock()

	// ponytail: fake engine only; replace behind Engine when a real WhatsApp driver exists.
	return domain.MessageResult{
		ID:        id,
		Session:   session.Name,
		To:        request.To,
		Status:    "sent",
		Timestamp: time.Now().UTC(),
	}, nil
}
