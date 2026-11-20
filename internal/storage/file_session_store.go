package storage

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"

	"github.com/victor1mor/Zylpher/internal/domain"
)

type FileSessionStore struct {
	path string
}

func NewFileSessionStore(path string) *FileSessionStore {
	return &FileSessionStore{path: path}
}

func (s *FileSessionStore) Load(ctx context.Context) (map[string]domain.Session, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	file, err := os.Open(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]domain.Session{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var sessions []domain.Session
	if err := json.NewDecoder(file).Decode(&sessions); err != nil {
		return nil, err
	}

	result := make(map[string]domain.Session, len(sessions))
	for _, session := range sessions {
		result[session.Name] = session
	}
	return result, nil
}

func (s *FileSessionStore) Save(ctx context.Context, sessions []domain.Session) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].Name < sessions[j].Name
	})

	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return err
	}

	tmp, err := os.CreateTemp(dir, ".sessions-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	encoder := json.NewEncoder(tmp)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(sessions); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}

	return os.Rename(tmpName, s.path)
}
