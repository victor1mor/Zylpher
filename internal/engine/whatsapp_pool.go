package engine

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"github.com/victor1mor/Zylpher/internal/domain"
	"github.com/victor1mor/Zylpher/internal/media"
)

type WhatsAppPool struct {
	mu       sync.RWMutex
	dataDir  string
	clients  map[string]*WhatsApp
	events   domain.EventHandler
	statuses StatusHandler
	media    *media.Store
}

// SetMediaStore enables attachments on every session, current and future.
func (p *WhatsAppPool) SetMediaStore(store *media.Store) {
	p.mu.Lock()
	p.media = store
	for _, client := range p.clients {
		client.SetMediaStore(store)
	}
	p.mu.Unlock()
}

func (p *WhatsAppPool) SetStatusHandler(handler StatusHandler) {
	p.mu.Lock()
	p.statuses = handler
	for _, client := range p.clients {
		client.SetStatusHandler(handler)
	}
	p.mu.Unlock()
}

func NewWhatsAppPool(dataDir string) *WhatsAppPool {
	return &WhatsAppPool{dataDir: dataDir, clients: make(map[string]*WhatsApp)}
}

func (p *WhatsAppPool) SetEventHandler(handler domain.EventHandler) {
	p.mu.Lock()
	p.events = handler
	for _, client := range p.clients {
		client.SetEventHandler(handler)
	}
	p.mu.Unlock()
}

func (p *WhatsAppPool) Connect(ctx context.Context, session domain.Session) error {
	client, err := p.client(ctx, session.Name)
	if err != nil {
		return err
	}
	return client.Connect(ctx, session)
}

func (p *WhatsAppPool) Disconnect(ctx context.Context, session domain.Session) error {
	client := p.existing(session.Name)
	if client == nil {
		return nil
	}
	return client.Disconnect(ctx, session)
}

func (p *WhatsAppPool) Status(ctx context.Context, session domain.Session) (domain.SessionStatus, error) {
	client := p.existing(session.Name)
	if client == nil {
		return domain.SessionStopped, nil
	}
	return client.Status(ctx, session)
}

func (p *WhatsAppPool) QRCode(ctx context.Context, session domain.Session) (string, bool, error) {
	client := p.existing(session.Name)
	if client == nil {
		return "", false, nil
	}
	return client.QRCode(ctx, session)
}

func (p *WhatsAppPool) SendText(ctx context.Context, session domain.Session, request domain.TextMessageRequest) (domain.MessageResult, error) {
	client := p.existing(session.Name)
	if client == nil {
		return domain.MessageResult{}, domain.ErrSessionNotWorking
	}
	return client.SendText(ctx, session, request)
}

func (p *WhatsAppPool) SendMedia(ctx context.Context, session domain.Session, request domain.MediaMessageRequest) (domain.MessageResult, error) {
	client := p.existing(session.Name)
	if client == nil {
		return domain.MessageResult{}, domain.ErrSessionNotWorking
	}
	return client.SendMedia(ctx, session, request)
}

func (p *WhatsAppPool) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	var failures []error
	for name, client := range p.clients {
		if err := client.Close(); err != nil {
			failures = append(failures, fmt.Errorf("close WhatsApp session %q: %w", name, err))
		}
	}
	return errors.Join(failures...)
}

func (p *WhatsAppPool) client(ctx context.Context, name string) (*WhatsApp, error) {
	if strings.TrimSpace(name) == "" || strings.TrimSpace(p.dataDir) == "" {
		return nil, domain.ErrInvalidInput
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if client := p.clients[name]; client != nil {
		return client, nil
	}
	client, err := NewWhatsApp(ctx, p.database(name))
	if err != nil {
		return nil, err
	}
	client.SetEventHandler(p.events)
	client.SetStatusHandler(p.statuses)
	client.SetMediaStore(p.media)
	p.clients[name] = client
	return client, nil
}

func (p *WhatsAppPool) existing(name string) *WhatsApp {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.clients[name]
}

func (p *WhatsAppPool) database(name string) string {
	// ponytail: preserve the existing default credentials; hash only new session filenames.
	if name == "default" {
		return filepath.Join(p.dataDir, "whatsapp.db")
	}
	return filepath.Join(p.dataDir, fmt.Sprintf("whatsapp-%x.db", sha256.Sum256([]byte(name))))
}
