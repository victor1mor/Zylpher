package storage

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/victor1mor/Zylpher/internal/domain"
)

func TestFileSessionStoreSaveLoad(t *testing.T) {
	ctx := context.Background()
	store := NewFileSessionStore(filepath.Join(t.TempDir(), "sessions.json"))
	now := time.Now().UTC()

	err := store.Save(ctx, []domain.Session{
		{Name: "b", Status: domain.SessionWorking, CreatedAt: now, UpdatedAt: now},
		{Name: "a", Status: domain.SessionStopped, CreatedAt: now, UpdatedAt: now},
	})
	if err != nil {
		t.Fatalf("Save returned error: %v", err)
	}

	sessions, err := store.Load(ctx)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if len(sessions) != 2 {
		t.Fatalf("len(sessions) = %d, want 2", len(sessions))
	}
	if sessions["a"].Status != domain.SessionStopped {
		t.Fatalf("session a status = %s, want %s", sessions["a"].Status, domain.SessionStopped)
	}
	if sessions["b"].Status != domain.SessionWorking {
		t.Fatalf("session b status = %s, want %s", sessions["b"].Status, domain.SessionWorking)
	}
}

func TestFileSessionStoreLoadMissingFile(t *testing.T) {
	sessions, err := NewFileSessionStore(filepath.Join(t.TempDir(), "missing.json")).Load(context.Background())
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if len(sessions) != 0 {
		t.Fatalf("len(sessions) = %d, want 0", len(sessions))
	}
}
