package media

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/victor1mor/Zylpher/internal/domain"
)

func TestSaveOpenDelete(t *testing.T) {
	store := NewStore(t.TempDir(), 1024)
	saved, err := store.Save("default", domain.ReceivedMedia{ID: "3EB0ABC", Kind: domain.MediaImage, MimeType: "image/jpeg"}, []byte("foto"))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("foto"))
	if saved.Size != 4 || saved.SHA256 != hex.EncodeToString(sum[:]) || saved.ID != "3EB0ABC" {
		t.Fatalf("saved = %#v", saved)
	}
	item, data, err := store.Open("default", "3EB0ABC")
	if err != nil || string(data) != "foto" || item.MimeType != "image/jpeg" || item.Kind != domain.MediaImage || item.SHA256 != saved.SHA256 {
		t.Fatalf("open = %#v, %q, %v", item, data, err)
	}
	if err := store.Delete("default", "3EB0ABC"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Open("default", "3EB0ABC"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("open after delete = %v", err)
	}
	if err := store.Delete("default", "3EB0ABC"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("second delete = %v", err)
	}
}

func TestSaveRestrictsPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("os.Chmod does not replace NTFS ACLs")
	}
	directory := t.TempDir()
	store := NewStore(directory, 1024)
	if _, err := store.Save("default", domain.ReceivedMedia{ID: "a", Kind: domain.MediaAudio}, []byte("x")); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]os.FileMode{
		filepath.Join(directory, "default"):           0o700,
		filepath.Join(directory, "default", "a.bin"):  0o600,
		filepath.Join(directory, "default", "a.json"): 0o600,
	} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != want {
			t.Fatalf("mode %s = %v, want %v", path, got, want)
		}
	}
}

func TestRefusesUnsafeOrInvalid(t *testing.T) {
	store := NewStore(t.TempDir(), 4)
	cases := []struct {
		session string
		info    domain.ReceivedMedia
		data    []byte
		want    error
	}{
		{"default", domain.ReceivedMedia{ID: "../fora", Kind: domain.MediaImage}, []byte("x"), domain.ErrInvalidInput},
		{"default", domain.ReceivedMedia{ID: "a/b", Kind: domain.MediaImage}, []byte("x"), domain.ErrInvalidInput},
		{"..", domain.ReceivedMedia{ID: "a", Kind: domain.MediaImage}, []byte("x"), domain.ErrInvalidInput},
		{"default", domain.ReceivedMedia{ID: "", Kind: domain.MediaImage}, []byte("x"), domain.ErrInvalidInput},
		{"default", domain.ReceivedMedia{ID: "a", Kind: "sticker"}, []byte("x"), domain.ErrInvalidInput},
		{"default", domain.ReceivedMedia{ID: "a", Kind: domain.MediaImage}, []byte("grande"), domain.ErrTooLarge},
	}
	for _, c := range cases {
		if _, err := store.Save(c.session, c.info, c.data); !errors.Is(err, c.want) {
			t.Fatalf("save %q/%q = %v, want %v", c.session, c.info.ID, err, c.want)
		}
	}
	if _, _, err := store.Open("default", "../../etc/passwd"); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("open traversal = %v", err)
	}
}

func TestSweepRemovesOnlyOld(t *testing.T) {
	directory := t.TempDir()
	store := NewStore(directory, 1024)
	for _, id := range []string{"velha", "nova"} {
		if _, err := store.Save("default", domain.ReceivedMedia{ID: id, Kind: domain.MediaDocument}, []byte(id)); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().Add(-48 * time.Hour)
	for _, name := range []string{"velha.bin", "velha.json"} {
		if err := os.Chtimes(filepath.Join(directory, "default", name), old, old); err != nil {
			t.Fatal(err)
		}
	}
	removed, err := store.Sweep(24*time.Hour, time.Now())
	if err != nil || removed != 1 {
		t.Fatalf("sweep = %d, %v", removed, err)
	}
	if _, _, err := store.Open("default", "velha"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("old still there: %v", err)
	}
	if _, _, err := store.Open("default", "nova"); err != nil {
		t.Fatalf("new removed: %v", err)
	}
}
