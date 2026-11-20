package domain

type WebhookEvent struct {
	ID        string `json:"id"`
	Event     string `json:"event"`
	Session   string `json:"session"`
	Timestamp int64  `json:"timestamp"`
	Payload   any    `json:"payload"`
}

type EventHandler func(WebhookEvent)

func MessageReceivedEvent(message ReceivedMessage) WebhookEvent {
	return WebhookEvent{
		ID:        message.ID,
		Event:     "message.received",
		Session:   message.Session,
		Timestamp: message.Timestamp.UnixMilli(),
		Payload:   message,
	}
}

func SessionStatusEvent(session Session) WebhookEvent {
	return WebhookEvent{
		ID:        session.Name,
		Event:     "session.status",
		Session:   session.Name,
		Timestamp: session.UpdatedAt.UnixMilli(),
		Payload:   session,
	}
}
