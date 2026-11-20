package domain

import "time"

type TextMessageRequest struct {
	To   string `json:"to"`
	Text string `json:"text"`
}

type MessageResult struct {
	ID        string    `json:"id"`
	Session   string    `json:"session"`
	To        string    `json:"to"`
	Status    string    `json:"status"`
	Timestamp time.Time `json:"timestamp"`
}

type ReceivedMessage struct {
	ID        string    `json:"id"`
	Session   string    `json:"session"`
	From      string    `json:"from"`
	To        string    `json:"to"`
	Text      string    `json:"text"`
	Timestamp time.Time `json:"timestamp"`
	// Media is set when the message carries an attachment. Text then holds the caption, if any.
	// The bytes stay in the local media store until the consumer fetches and deletes them.
	Media *ReceivedMedia `json:"media,omitempty"`
	// MediaError says why an attachment was not stored ("too_large", "download_failed",
	// "no_store"). The message is still delivered so the consumer can answer.
	MediaError string `json:"media_error,omitempty"`
}

// MediaKind is the attachment family. Each one maps to one WhatsApp message type.
type MediaKind string

const (
	MediaImage    MediaKind = "image"
	MediaAudio    MediaKind = "audio"
	MediaVideo    MediaKind = "video"
	MediaDocument MediaKind = "document"
)

func (k MediaKind) Valid() bool {
	switch k {
	case MediaImage, MediaAudio, MediaVideo, MediaDocument:
		return true
	}
	return false
}

// ReceivedMedia describes a stored attachment. Fetch it with GET /sessions/{name}/media/{id}.
type ReceivedMedia struct {
	ID       string    `json:"id"`
	Kind     MediaKind `json:"kind"`
	MimeType string    `json:"mime_type"`
	Size     int64     `json:"size"`
	SHA256   string    `json:"sha256"`
	Voice    bool      `json:"voice,omitempty"`
	FileName string    `json:"file_name,omitempty"`
}

// MediaMessageRequest is an outgoing attachment. Voice sends audio as a voice note (OGG/Opus).
type MediaMessageRequest struct {
	To       string
	Kind     MediaKind
	MimeType string
	Caption  string
	FileName string
	Voice    bool
	Data     []byte
}
