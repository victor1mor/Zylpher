package session

import (
	"context"
	"errors"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/victor1mor/Zylpher/internal/domain"
	"github.com/victor1mor/Zylpher/internal/engine"
	"github.com/victor1mor/Zylpher/internal/storage"
)

func TestManagerCreateListStartStop(t *testing.T) {
	ctx := context.Background()
	manager := NewManager()

	created, err := manager.Create(ctx, "default")
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if created.Status != domain.SessionStopped {
		t.Fatalf("created status = %s, want %s", created.Status, domain.SessionStopped)
	}

	got, err := manager.Get(ctx, "default")
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if got.Name != "default" {
		t.Fatalf("got name = %s, want default", got.Name)
	}

	started, err := manager.Start(ctx, "default")
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	if started.Status != domain.SessionWorking {
		t.Fatalf("started status = %s, want %s", started.Status, domain.SessionWorking)
	}

	stopped, err := manager.Stop(ctx, "default")
	if err != nil {
		t.Fatalf("Stop returned error: %v", err)
	}
	if stopped.Status != domain.SessionStopped {
		t.Fatalf("stopped status = %s, want %s", stopped.Status, domain.SessionStopped)
	}

	sessions, err := manager.List(ctx)
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if len(sessions) != 1 || sessions[0].Name != "default" {
		t.Fatalf("sessions = %#v, want default", sessions)
	}
}

func TestManagerCreateRejectsDuplicateAndBlankName(t *testing.T) {
	ctx := context.Background()
	manager := NewManager()

	if _, err := manager.Create(ctx, " "); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("blank Create error = %v, want %v", err, domain.ErrInvalidInput)
	}
	if _, err := manager.Create(ctx, "default"); err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if _, err := manager.Create(ctx, "default"); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("duplicate Create error = %v, want %v", err, domain.ErrConflict)
	}
}

func TestManagerStartMissingSession(t *testing.T) {
	_, err := NewManager().Start(context.Background(), "missing")
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Start error = %v, want %v", err, domain.ErrNotFound)
	}
}

func TestManagerRespectsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := NewManager().Create(ctx, "default")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Create error = %v, want %v", err, context.Canceled)
	}
}

func TestManagerConcurrentCreate(t *testing.T) {
	ctx := context.Background()
	manager := NewManager()
	var wg sync.WaitGroup

	for i := range 50 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if _, err := manager.Create(ctx, "session-"+strconv.Itoa(i)); err != nil {
				t.Errorf("Create(%d) returned error: %v", i, err)
			}
		}(i)
	}
	wg.Wait()

	sessions, err := manager.List(ctx)
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if len(sessions) != 50 {
		t.Fatalf("len(sessions) = %d, want 50", len(sessions))
	}
}

func TestManagerPersistsSessions(t *testing.T) {
	ctx := context.Background()
	store := storage.NewFileSessionStore(filepath.Join(t.TempDir(), "sessions.json"))

	manager, err := NewManagerWithStore(ctx, store)
	if err != nil {
		t.Fatalf("NewManagerWithStore returned error: %v", err)
	}
	if _, err := manager.Create(ctx, "default"); err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if _, err := manager.Start(ctx, "default"); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}

	reloaded, err := NewManagerWithStore(ctx, store)
	if err != nil {
		t.Fatalf("reload returned error: %v", err)
	}
	session, err := reloaded.Get(ctx, "default")
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if session.Status != domain.SessionWorking {
		t.Fatalf("status = %s, want %s", session.Status, domain.SessionWorking)
	}
}

func TestManagerReconnectsOnlyPersistedActiveSessions(t *testing.T) {
	ctx := context.Background()
	store := storage.NewFileSessionStore(filepath.Join(t.TempDir(), "sessions.json"))
	if err := store.Save(ctx, []domain.Session{
		{Name: "active", Status: domain.SessionWorking},
		{Name: "stopped", Status: domain.SessionStopped},
	}); err != nil {
		t.Fatal(err)
	}
	fake := engine.NewFake()
	manager, err := NewManagerWithStoreAndEngine(ctx, store, fake)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.ReconnectActive(ctx); err != nil {
		t.Fatal(err)
	}
	if status, _ := fake.Status(ctx, domain.Session{Name: "active"}); status != domain.SessionWorking {
		t.Fatalf("active status = %s", status)
	}
	if status, _ := fake.Status(ctx, domain.Session{Name: "stopped"}); status != domain.SessionStopped {
		t.Fatalf("stopped status = %s", status)
	}
}

func TestManagerReconnectsActiveSessionsConcurrently(t *testing.T) {
	ctx := context.Background()
	store := storage.NewFileSessionStore(filepath.Join(t.TempDir(), "sessions.json"))
	if err := store.Save(ctx, []domain.Session{
		{Name: "first", Status: domain.SessionWorking},
		{Name: "second", Status: domain.SessionWorking},
	}); err != nil {
		t.Fatal(err)
	}
	eng := &parallelReconnectEngine{
		Fake:    engine.NewFake(),
		started: make(chan struct{}, 2),
		release: make(chan struct{}),
	}
	manager, err := NewManagerWithStoreAndEngine(ctx, store, eng)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- manager.ReconnectActive(ctx) }()

	for range 2 {
		select {
		case <-eng.started:
		case <-time.After(time.Second):
			close(eng.release)
			t.Fatal("active sessions were reconnected serially")
		}
	}
	close(eng.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

type parallelReconnectEngine struct {
	*engine.Fake
	started chan struct{}
	release chan struct{}
}

func (e *parallelReconnectEngine) Connect(ctx context.Context, session domain.Session) error {
	e.started <- struct{}{}
	select {
	case <-e.release:
		return e.Fake.Connect(ctx, session)
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestManagerAppliesEngineStatus(t *testing.T) {
	manager := NewManager()
	if _, err := manager.Create(context.Background(), "default"); err != nil {
		t.Fatal(err)
	}
	if err := manager.ApplyEngineStatus(context.Background(), "default", domain.SessionFailed); err != nil {
		t.Fatal(err)
	}
	session, err := manager.Get(context.Background(), "default")
	if err != nil || session.Status != domain.SessionFailed {
		t.Fatalf("session = %#v, error = %v", session, err)
	}
}

func TestManagerRejectsConcurrentStarts(t *testing.T) {
	ctx := context.Background()
	manager := NewManager()
	if _, err := manager.Create(ctx, "default"); err != nil {
		t.Fatal(err)
	}

	var successes, conflicts int
	var mu sync.Mutex
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := manager.Start(ctx, "default")
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				successes++
			} else if errors.Is(err, domain.ErrConflict) {
				conflicts++
			} else {
				t.Errorf("Start returned error: %v", err)
			}
		}()
	}
	wg.Wait()

	if successes != 1 || conflicts != 19 {
		t.Fatalf("successes = %d, conflicts = %d", successes, conflicts)
	}
}

func TestManagerMarksFailedWhenEngineCannotConnect(t *testing.T) {
	manager := NewManagerWithEngine(failingEngine{})
	if _, err := manager.Create(context.Background(), "default"); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Start(context.Background(), "default"); err == nil {
		t.Fatal("Start succeeded")
	}

	session, err := manager.Get(context.Background(), "default")
	if err != nil || session.Status != domain.SessionFailed {
		t.Fatalf("session = %#v, err = %v", session, err)
	}
}

func TestManagerEmitsSessionStatus(t *testing.T) {
	manager := NewManager()
	events := make(chan domain.WebhookEvent, 3)
	manager.SetEventHandler(func(event domain.WebhookEvent) { events <- event })
	if _, err := manager.Create(context.Background(), "default"); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Start(context.Background(), "default"); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Stop(context.Background(), "default"); err != nil {
		t.Fatal(err)
	}

	for _, status := range []domain.SessionStatus{domain.SessionStarting, domain.SessionWorking, domain.SessionStopped} {
		event := <-events
		session, ok := event.Payload.(domain.Session)
		if !ok || event.Event != "session.status" || session.Status != status {
			t.Fatalf("event = %#v, want status %s", event, status)
		}
	}
}

type failingEngine struct{}

func (failingEngine) Connect(context.Context, domain.Session) error {
	return errors.New("connection failed")
}

func (failingEngine) Disconnect(context.Context, domain.Session) error {
	return nil
}

func (failingEngine) Status(context.Context, domain.Session) (domain.SessionStatus, error) {
	return domain.SessionStopped, nil
}

func (failingEngine) QRCode(context.Context, domain.Session) (string, bool, error) {
	return "", false, nil
}

func (failingEngine) SendText(context.Context, domain.Session, domain.TextMessageRequest) (domain.MessageResult, error) {
	return domain.MessageResult{}, nil
}

func (failingEngine) SendMedia(context.Context, domain.Session, domain.MediaMessageRequest) (domain.MessageResult, error) {
	return domain.MessageResult{}, nil
}
