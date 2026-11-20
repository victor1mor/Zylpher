package engine

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"go.mau.fi/whatsmeow"
	waE2E "go.mau.fi/whatsmeow/proto/waE2E"
	waStore "go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"

	"github.com/victor1mor/Zylpher/internal/domain"
)

func TestWhatsAppCreatesUnpairedStore(t *testing.T) {
	directory := t.TempDir()
	database := filepath.Join(directory, "whatsapp.db")
	client, err := NewWhatsApp(context.Background(), database)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	if !client.NeedsPairing() {
		t.Fatal("expected an unpaired client")
	}
	if runtime.GOOS != "windows" {
		for path, want := range map[string]os.FileMode{directory: 0o700, database: 0o600} {
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if got := info.Mode().Perm(); got != want {
				t.Fatalf("mode %s = %v, want %v", path, got, want)
			}
		}
	}
	if code, available, err := client.QRCode(context.Background(), domain.Session{Name: "default"}); err != nil || available || code != "" {
		t.Fatalf("QR = %q, %t, %v", code, available, err)
	}
	status, err := client.Status(context.Background(), domain.Session{Name: "default"})
	if err != nil || status != domain.SessionStopped {
		t.Fatalf("status = %s, %v", status, err)
	}
}

func TestWhatsAppUsesZylpherDeviceName(t *testing.T) {
	client, err := NewWhatsApp(context.Background(), filepath.Join(t.TempDir(), "whatsapp.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if got := waStore.DeviceProps.GetOs(); got != "Zylpher" {
		t.Fatalf("device name = %q, want Zylpher", got)
	}
}

func TestParseDestination(t *testing.T) {
	for _, tc := range []struct {
		input, to, jid string
	}{
		{" +5511999999999 ", "5511999999999", "5511999999999@s.whatsapp.net"},
		{"5511999999999:2@s.whatsapp.net", "5511999999999@s.whatsapp.net", "5511999999999@s.whatsapp.net"},
		{"12345678901234567@lid", "12345678901234567@lid", "12345678901234567@lid"},
		{"12345678901234567.1:3@lid", "12345678901234567@lid", "12345678901234567@lid"},
		{"5511999999999:2", "", ""},
		{"12345678901234567", "", ""},
		{"0123456789@s.whatsapp.net", "", ""},
		{"12345678901234567:65536@lid", "", ""},
		{"12345678901234567@lid@lid", "", ""},
		{"18446744073709551616@lid", "", ""},
		{"0@lid", "", ""},
		{"status@broadcast", "", ""},
	} {
		t.Run(tc.input, func(t *testing.T) {
			jid, to, err := ParseDestination(tc.input)
			if tc.to == "" {
				if !errors.Is(err, domain.ErrInvalidInput) {
					t.Fatalf("invalid destination: %v", err)
				}
				return
			}
			if err != nil || jid.String() != tc.jid || to != tc.to {
				t.Fatalf("destination = (%q, %q, %v), want (%q, %q, nil)", jid.String(), to, err, tc.jid, tc.to)
			}
		})
	}
}

func TestTextMessage(t *testing.T) {
	if got := textMessage("oi").GetConversation(); got != "oi" {
		t.Fatalf("conversation = %q, want oi", got)
	}
}

func TestReceivedMessage(t *testing.T) {
	timestamp := time.Date(2026, time.July, 10, 18, 0, 0, 0, time.UTC)
	message := &events.Message{
		Info: types.MessageInfo{
			MessageSource: types.MessageSource{Sender: types.NewJID("5511999999999", types.DefaultUserServer)},
			ID:            "message-1",
			Timestamp:     timestamp,
		},
		Message: &waE2E.Message{Conversation: proto.String("oi")},
	}

	got, ok := receivedMessage("default", types.NewJID("5511888888888", types.DefaultUserServer), message)
	if !ok {
		t.Fatal("received message was ignored")
	}
	if got.ID != "message-1" || got.Session != "default" || got.From != "5511999999999@s.whatsapp.net" || got.To != "5511888888888@s.whatsapp.net" || got.Text != "oi" || !got.Timestamp.Equal(timestamp) {
		t.Fatalf("received message = %#v", got)
	}
}

func TestReceivedMessageIgnoresEventsWithoutText(t *testing.T) {
	got, ok := receivedMessage("default", types.JID{}, &events.Message{})
	if ok || got != (domain.ReceivedMessage{}) {
		t.Fatalf("received message = %#v, ok = %t", got, ok)
	}
}

func TestReceivedMessageReadsExtendedText(t *testing.T) {
	message := &events.Message{Message: &waE2E.Message{
		ExtendedTextMessage: &waE2E.ExtendedTextMessage{Text: proto.String("oi")},
	}}

	got, ok := receivedMessage("default", types.JID{}, message)
	if !ok || got.Text != "oi" {
		t.Fatalf("received message = %#v, ok = %t", got, ok)
	}
}

func TestWhatsAppEmitsReceivedMessageWithoutBlocking(t *testing.T) {
	client, err := NewWhatsApp(context.Background(), filepath.Join(t.TempDir(), "whatsapp.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	recipient := types.NewJID("5511999999999", types.DefaultUserServer)
	client.client.Store.ID = &recipient
	client.session = "default"
	received := make(chan domain.WebhookEvent, 1)
	release := make(chan struct{})
	client.SetEventHandler(func(event domain.WebhookEvent) {
		received <- event
		<-release
	})

	done := make(chan struct{})
	go func() {
		client.handleEvent(&events.Message{
			Info: types.MessageInfo{
				MessageSource: types.MessageSource{Sender: types.NewJID("5511999999999", types.DefaultUserServer)},
				ID:            "message-1",
				Timestamp:     time.Now(),
			},
			Message: &waE2E.Message{Conversation: proto.String("oi")},
		})
		close(done)
	}()

	select {
	case event := <-received:
		if event.Event != "message.received" || event.ID != "message-1" {
			t.Fatalf("event = %#v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("event handler was not called")
	}
	select {
	case <-done:
		close(release)
	case <-time.After(time.Second):
		close(release)
		t.Fatal("WhatsApp event loop was blocked by the webhook handler")
	}
}

func TestWhatsAppMapsConnectionEvents(t *testing.T) {
	client, err := NewWhatsApp(context.Background(), filepath.Join(t.TempDir(), "whatsapp.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	client.session = "default"
	statuses := make(chan domain.SessionStatus, 1)
	client.SetStatusHandler(func(_ string, status domain.SessionStatus) { statuses <- status })

	for event, want := range map[any]domain.SessionStatus{
		&events.Connected{}:    domain.SessionWorking,
		&events.Disconnected{}: domain.SessionStarting,
		&events.LoggedOut{}:    domain.SessionFailed,
	} {
		client.handleEvent(event)
		if got := <-statuses; got != want {
			t.Fatalf("status = %s, want %s", got, want)
		}
	}
}

func TestSendError(t *testing.T) {
	for _, tt := range []struct {
		err  error
		want error
	}{
		{whatsmeow.ErrNotConnected, domain.ErrSessionNotWorking},
		{whatsmeow.ErrRecipientADJID, domain.ErrInvalidInput},
		{whatsmeow.ErrMessageTimedOut, domain.ErrSendTimedOut},
		{whatsmeow.ErrServerReturnedError, domain.ErrRemoteFailure},
		{context.DeadlineExceeded, domain.ErrSendTimedOut},
	} {
		if got := sendError(tt.err); !errors.Is(got, tt.want) {
			t.Errorf("sendError(%v) = %v, want %v", tt.err, got, tt.want)
		}
	}
}
