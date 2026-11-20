// Package media keeps received attachments on local disk until the consumer fetches them.
//
// Layout: <dir>/<session>/<id>.bin (the bytes) and <id>.json (the description). Directories are
// 0700 and files 0600, like the credential store. Nothing here is served without the API key.
package media

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/victor1mor/Zylpher/internal/domain"
)

const maxIDLength = 128

type Store struct {
	dir      string
	maxBytes int64
}

// Item is a stored attachment and when it arrived.
type Item struct {
	domain.ReceivedMedia
	StoredAt time.Time `json:"stored_at"`
}

func NewStore(dir string, maxBytes int64) *Store {
	return &Store{dir: dir, maxBytes: maxBytes}
}

// MaxBytes is the largest attachment accepted in either direction.
func (s *Store) MaxBytes() int64 {
	return s.maxBytes
}

// ValidID accepts the WhatsApp message IDs seen in practice: letters, digits, '_' and '-'.
// Anything else could escape the session directory, so it is refused.
func ValidID(id string) bool {
	if id == "" || len(id) > maxIDLength {
		return false
	}
	for _, r := range id {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
			return false
		}
	}
	return true
}

func validSession(session string) bool {
	return ValidID(session) && len(session) <= 64
}

func (s *Store) paths(session, id string) (string, string, string, error) {
	if strings.TrimSpace(s.dir) == "" || !validSession(session) || !ValidID(id) {
		return "", "", "", domain.ErrInvalidInput
	}
	directory := filepath.Join(s.dir, session)
	return directory, filepath.Join(directory, id+".bin"), filepath.Join(directory, id+".json"), nil
}

// Save stores the bytes and returns the description with ID, size and SHA-256 filled in.
func (s *Store) Save(session string, info domain.ReceivedMedia, data []byte) (domain.ReceivedMedia, error) {
	directory, binary, description, err := s.paths(session, info.ID)
	if err != nil {
		return domain.ReceivedMedia{}, err
	}
	if !info.Kind.Valid() {
		return domain.ReceivedMedia{}, domain.ErrInvalidInput
	}
	if int64(len(data)) > s.maxBytes {
		return domain.ReceivedMedia{}, domain.ErrTooLarge
	}
	sum := sha256.Sum256(data)
	info.Size = int64(len(data))
	info.SHA256 = hex.EncodeToString(sum[:])

	if err := os.MkdirAll(directory, 0o700); err != nil {
		return domain.ReceivedMedia{}, fmt.Errorf("create media directory: %w", err)
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return domain.ReceivedMedia{}, fmt.Errorf("restrict media directory: %w", err)
	}
	meta, err := json.Marshal(Item{ReceivedMedia: info, StoredAt: time.Now().UTC()})
	if err != nil {
		return domain.ReceivedMedia{}, err
	}
	// The bytes first, the description last: a description always points to complete bytes.
	if err := writeAtomic(binary, data); err != nil {
		return domain.ReceivedMedia{}, err
	}
	if err := writeAtomic(description, meta); err != nil {
		_ = os.Remove(binary)
		return domain.ReceivedMedia{}, err
	}
	return info, nil
}

// Open returns the description and the bytes of a stored attachment.
func (s *Store) Open(session, id string) (Item, []byte, error) {
	_, binary, description, err := s.paths(session, id)
	if err != nil {
		return Item{}, nil, err
	}
	meta, err := os.ReadFile(description)
	if errors.Is(err, os.ErrNotExist) {
		return Item{}, nil, domain.ErrNotFound
	}
	if err != nil {
		return Item{}, nil, err
	}
	var item Item
	if err := json.Unmarshal(meta, &item); err != nil {
		return Item{}, nil, fmt.Errorf("read media description: %w", err)
	}
	data, err := os.ReadFile(binary)
	if errors.Is(err, os.ErrNotExist) {
		return Item{}, nil, domain.ErrNotFound
	}
	if err != nil {
		return Item{}, nil, err
	}
	return item, data, nil
}

// Delete removes a stored attachment. Missing → domain.ErrNotFound.
func (s *Store) Delete(session, id string) error {
	_, binary, description, err := s.paths(session, id)
	if err != nil {
		return err
	}
	err = os.Remove(description)
	if errors.Is(err, os.ErrNotExist) {
		return domain.ErrNotFound
	}
	if err != nil {
		return err
	}
	if err := os.Remove(binary); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// Sweep deletes what nobody fetched in time, so the disk does not keep attachments forever.
// It returns how many attachments it removed.
func (s *Store) Sweep(maxAge time.Duration, now time.Time) (int, error) {
	if strings.TrimSpace(s.dir) == "" {
		return 0, nil
	}
	sessions, err := os.ReadDir(s.dir)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	removed := 0
	var failures []error
	for _, session := range sessions {
		if !session.IsDir() {
			continue
		}
		directory := filepath.Join(s.dir, session.Name())
		files, err := os.ReadDir(directory)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		for _, file := range files {
			if file.IsDir() {
				continue
			}
			info, err := file.Info()
			if err != nil {
				failures = append(failures, err)
				continue
			}
			if now.Sub(info.ModTime()) < maxAge {
				continue
			}
			if err := os.Remove(filepath.Join(directory, file.Name())); err != nil && !errors.Is(err, os.ErrNotExist) {
				failures = append(failures, err)
				continue
			}
			if strings.HasSuffix(file.Name(), ".json") {
				removed++
			}
		}
	}
	return removed, errors.Join(failures...)
}

func writeAtomic(path string, data []byte) error {
	temporary, err := os.CreateTemp(filepath.Dir(path), ".media-*")
	if err != nil {
		return fmt.Errorf("create media file: %w", err)
	}
	name := temporary.Name()
	_, writeErr := temporary.Write(data)
	closeErr := temporary.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		_ = os.Remove(name)
		return fmt.Errorf("write media file: %w", err)
	}
	if err := os.Chmod(name, 0o600); err != nil {
		_ = os.Remove(name)
		return fmt.Errorf("restrict media file: %w", err)
	}
	if err := os.Rename(name, path); err != nil {
		_ = os.Remove(name)
		return fmt.Errorf("store media file: %w", err)
	}
	return nil
}
