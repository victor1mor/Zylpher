package domain

import (
	"testing"
	"time"
)

func TestMessageReceivedEvent(t *testing.T) {
	timestamp := time.Date(2026, time.July, 10, 20, 0, 0, 0, time.UTC)
	message := ReceivedMessage{ID: "message-1", Session: "default", Text: "oi", Timestamp: timestamp}

	event := MessageReceivedEvent(message)
	if event.ID != message.ID || event.Event != "message.received" || event.Session != message.Session || event.Timestamp != timestamp.UnixMilli() || event.Payload != message {
		t.Fatalf("event = %#v", event)
	}
}

func TestSessionStatusEvent(t *testing.T) {
	timestamp := time.Date(2026, time.July, 10, 20, 0, 0, 0, time.UTC)
	session := Session{Name: "default", Status: SessionWorking, UpdatedAt: timestamp}

	event := SessionStatusEvent(session)
	if event.ID != session.Name || event.Event != "session.status" || event.Session != session.Name || event.Timestamp != timestamp.UnixMilli() || event.Payload != session {
		t.Fatalf("event = %#v", event)
	}
}
