package session

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/victor1mor/Zylpher/internal/domain"
	"github.com/victor1mor/Zylpher/internal/engine"
	"github.com/victor1mor/Zylpher/internal/storage"
)

type Manager struct {
	mu           sync.RWMutex
	sessions     map[string]domain.Session
	manualStops  map[string]bool
	store        *storage.FileSessionStore
	engine       engine.Engine
	eventHandler domain.EventHandler
}

func NewManager() *Manager {
	return NewManagerWithEngine(nil)
}

func NewManagerWithEngine(eng engine.Engine) *Manager {
	if eng == nil {
		eng = engine.NewFake()
	}
	return &Manager{sessions: make(map[string]domain.Session), manualStops: make(map[string]bool), engine: eng}
}

func NewManagerWithStore(ctx context.Context, store *storage.FileSessionStore) (*Manager, error) {
	return NewManagerWithStoreAndEngine(ctx, store, nil)
}

func NewManagerWithStoreAndEngine(ctx context.Context, store *storage.FileSessionStore, eng engine.Engine) (*Manager, error) {
	if eng == nil {
		eng = engine.NewFake()
	}
	if store == nil {
		return NewManagerWithEngine(eng), nil
	}
	sessions, err := store.Load(ctx)
	if err != nil {
		return nil, err
	}
	return &Manager{sessions: sessions, manualStops: make(map[string]bool), store: store, engine: eng}, nil
}

func (m *Manager) SetEventHandler(handler domain.EventHandler) {
	m.mu.Lock()
	m.eventHandler = handler
	m.mu.Unlock()
}

func (m *Manager) List(ctx context.Context) ([]domain.Session, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.sortedSessionsLocked(), nil
}

func (m *Manager) Get(ctx context.Context, name string) (domain.Session, error) {
	if err := ctx.Err(); err != nil {
		return domain.Session{}, err
	}
	name, err := normalizeSessionName(name)
	if err != nil {
		return domain.Session{}, domain.ErrInvalidInput
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	session, exists := m.sessions[name]
	if !exists {
		return domain.Session{}, domain.ErrNotFound
	}
	return session, nil
}

func (m *Manager) Refresh(ctx context.Context, name string) (domain.Session, error) {
	if err := ctx.Err(); err != nil {
		return domain.Session{}, err
	}
	name, err := normalizeSessionName(name)
	if err != nil {
		return domain.Session{}, domain.ErrInvalidInput
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	session, exists := m.sessions[name]
	if !exists {
		return domain.Session{}, domain.ErrNotFound
	}
	if m.manualStops[name] {
		return session, nil
	}
	status, err := m.engine.Status(ctx, session)
	if err != nil {
		return session, err
	}
	if status == session.Status {
		return session, nil
	}
	return m.setStatusLocked(context.WithoutCancel(ctx), session, status)
}

func (m *Manager) Create(ctx context.Context, name string) (domain.Session, error) {
	if err := ctx.Err(); err != nil {
		return domain.Session{}, err
	}
	name, err := normalizeSessionName(name)
	if err != nil {
		return domain.Session{}, domain.ErrInvalidInput
	}

	now := time.Now().UTC()
	session := domain.Session{
		Name:      name,
		Status:    domain.SessionStopped,
		CreatedAt: now,
		UpdatedAt: now,
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.sessions[name]; exists {
		return domain.Session{}, domain.ErrConflict
	}
	m.sessions[name] = session
	if err := m.saveLocked(ctx); err != nil {
		delete(m.sessions, name)
		return domain.Session{}, err
	}
	return session, nil
}

func (m *Manager) Start(ctx context.Context, name string) (domain.Session, error) {
	if err := ctx.Err(); err != nil {
		return domain.Session{}, err
	}

	name, err := normalizeSessionName(name)
	if err != nil {
		return domain.Session{}, domain.ErrInvalidInput
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	session, exists := m.sessions[name]
	if !exists {
		return domain.Session{}, domain.ErrNotFound
	}
	if session.Status == domain.SessionStarting || session.Status == domain.SessionWorking {
		return domain.Session{}, domain.ErrConflict
	}
	return m.connectLocked(ctx, session)
}

func (m *Manager) ReconnectActive(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.RLock()
	sessions := m.sortedSessionsLocked()
	m.mu.RUnlock()

	failures := make(chan error, len(sessions))
	var connections sync.WaitGroup
	for _, session := range sessions {
		if session.Status != domain.SessionStarting && session.Status != domain.SessionWorking {
			continue
		}
		connections.Add(1)
		go func() {
			defer connections.Done()
			if err := m.reconnect(ctx, session); err != nil {
				failures <- fmt.Errorf("reconnect session %q: %w", session.Name, err)
			}
		}()
	}
	connections.Wait()
	close(failures)
	var joined []error
	for err := range failures {
		joined = append(joined, err)
	}
	return errors.Join(joined...)
}

func (m *Manager) reconnect(ctx context.Context, session domain.Session) error {
	if err := m.engine.Connect(ctx, session); err != nil {
		_ = m.ApplyEngineStatus(context.WithoutCancel(ctx), session.Name, domain.SessionFailed)
		return err
	}
	status, err := m.engine.Status(ctx, session)
	if err != nil || (status != domain.SessionStarting && status != domain.SessionWorking) {
		_ = m.ApplyEngineStatus(context.WithoutCancel(ctx), session.Name, domain.SessionFailed)
		if err != nil {
			return err
		}
		return errors.New("engine did not reach WORKING")
	}
	return m.ApplyEngineStatus(context.WithoutCancel(ctx), session.Name, status)
}

func (m *Manager) ApplyEngineStatus(ctx context.Context, name string, status domain.SessionStatus) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	session, exists := m.sessions[name]
	if !exists {
		return domain.ErrNotFound
	}
	if m.manualStops[name] || session.Status == status {
		return nil
	}
	_, err := m.setStatusLocked(ctx, session, status)
	return err
}

func (m *Manager) connectLocked(ctx context.Context, session domain.Session) (domain.Session, error) {
	starting, err := m.setStatusLocked(ctx, session, domain.SessionStarting)
	if err != nil {
		return domain.Session{}, err
	}
	delete(m.manualStops, session.Name)

	// ponytail: global manager lock during connection; use per-session locks in Phase 5.
	if err := m.engine.Connect(ctx, starting); err != nil {
		failed, persistErr := m.setStatusLocked(context.WithoutCancel(ctx), starting, domain.SessionFailed)
		if persistErr != nil {
			return domain.Session{}, persistErr
		}
		return failed, err
	}

	status, err := m.engine.Status(ctx, starting)
	if err != nil || (status != domain.SessionWorking && status != domain.SessionStarting) {
		failed, persistErr := m.setStatusLocked(context.WithoutCancel(ctx), starting, domain.SessionFailed)
		if persistErr != nil {
			return domain.Session{}, persistErr
		}
		if err != nil {
			return failed, err
		}
		return failed, errors.New("engine did not reach WORKING")
	}
	if status == domain.SessionStarting {
		return starting, nil
	}
	return m.setStatusLocked(context.WithoutCancel(ctx), starting, domain.SessionWorking)
}

func (m *Manager) Stop(ctx context.Context, name string) (domain.Session, error) {
	if err := ctx.Err(); err != nil {
		return domain.Session{}, err
	}

	name, err := normalizeSessionName(name)
	if err != nil {
		return domain.Session{}, domain.ErrInvalidInput
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	session, exists := m.sessions[name]
	if !exists {
		return domain.Session{}, domain.ErrNotFound
	}
	if m.manualStops[name] {
		return session, nil
	}
	if err := m.engine.Disconnect(ctx, session); err != nil {
		failed, persistErr := m.setStatusLocked(context.WithoutCancel(ctx), session, domain.SessionFailed)
		if persistErr != nil {
			return domain.Session{}, persistErr
		}
		return failed, err
	}
	stopped, err := m.setStatusLocked(context.WithoutCancel(ctx), session, domain.SessionStopped)
	if err == nil {
		// Keep residual engine status from overriding a requested stop until the next start.
		m.manualStops[name] = true
	}
	return stopped, err
}

func (m *Manager) setStatusLocked(ctx context.Context, session domain.Session, status domain.SessionStatus) (domain.Session, error) {
	previous := session
	session.Status = status
	session.UpdatedAt = time.Now().UTC()
	m.sessions[session.Name] = session
	if err := m.saveLocked(ctx); err != nil {
		m.sessions[session.Name] = previous
		return domain.Session{}, err
	}
	if m.eventHandler != nil {
		m.eventHandler(domain.SessionStatusEvent(session))
	}
	return session, nil
}

func (m *Manager) saveLocked(ctx context.Context) error {
	if m.store == nil {
		return nil
	}
	// ponytail: full-file rewrite under the manager lock; split per-session when throughput matters.
	return m.store.Save(ctx, m.sortedSessionsLocked())
}

func (m *Manager) sortedSessionsLocked() []domain.Session {
	sessions := make([]domain.Session, 0, len(m.sessions))
	for _, session := range m.sessions {
		sessions = append(sessions, session)
	}
	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].Name < sessions[j].Name
	})
	return sessions
}

func normalizeSessionName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 64 {
		return "", domain.ErrInvalidInput
	}
	for _, char := range name {
		if (char < 'a' || char > 'z') && (char < 'A' || char > 'Z') && (char < '0' || char > '9') && char != '_' && char != '-' {
			return "", domain.ErrInvalidInput
		}
	}
	return name, nil
}
