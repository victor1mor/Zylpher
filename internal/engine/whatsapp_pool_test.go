package engine

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
)

func TestWhatsAppPoolIsolatesSessions(t *testing.T) {
	pool := NewWhatsAppPool(t.TempDir())
	defer pool.Close()

	first, err := pool.client(context.Background(), "first")
	if err != nil {
		t.Fatal(err)
	}
	second, err := pool.client(context.Background(), "second")
	if err != nil {
		t.Fatal(err)
	}
	again, err := pool.client(context.Background(), "first")
	if err != nil {
		t.Fatal(err)
	}
	if first == second || first != again {
		t.Fatal("sessions did not receive stable isolated clients")
	}
}

func TestWhatsAppPoolConcurrentAccess(t *testing.T) {
	pool := NewWhatsAppPool(t.TempDir())
	defer pool.Close()

	clients := make(chan *WhatsApp, 10)
	var wait sync.WaitGroup
	for range 10 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			client, err := pool.client(context.Background(), "same")
			if err != nil {
				t.Error(err)
				return
			}
			clients <- client
		}()
	}
	wait.Wait()
	close(clients)

	var first *WhatsApp
	for client := range clients {
		if first == nil {
			first = client
		} else if client != first {
			t.Fatal("concurrent access created more than one client")
		}
	}
}

func TestWhatsAppPoolPreservesDefaultDatabase(t *testing.T) {
	dataDir := t.TempDir()
	pool := NewWhatsAppPool(dataDir)
	if got, want := pool.database("default"), filepath.Join(dataDir, "whatsapp.db"); got != want {
		t.Fatalf("database = %q, want %q", got, want)
	}
	if got := pool.database(`..\other`); filepath.Dir(got) != dataDir || got == filepath.Join(dataDir, "whatsapp.db") {
		t.Fatalf("isolated database = %q", got)
	}
}

func TestWhatsAppPoolClosesAllClients(t *testing.T) {
	ctx := context.Background()
	pool := NewWhatsAppPool(t.TempDir())
	first, err := pool.client(ctx, "first")
	if err != nil {
		t.Fatal(err)
	}
	second, err := pool.client(ctx, "second")
	if err != nil {
		t.Fatal(err)
	}
	if err := pool.Close(); err != nil {
		t.Fatal(err)
	}
	for _, client := range []*WhatsApp{first, second} {
		if _, err := client.container.GetFirstDevice(ctx); err == nil {
			t.Fatal("credential store remained open")
		}
	}
}
