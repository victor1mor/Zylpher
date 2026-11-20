package engine

import (
	"context"
	"crypto/aes"
	"errors"
	"net/http"
	"strings"
	"time"

	"go.mau.fi/whatsmeow"
	waE2E "go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"

	"github.com/victor1mor/Zylpher/internal/domain"
	"github.com/victor1mor/Zylpher/internal/media"
)

// downloadTimeout bounds one attachment download, outside the whatsmeow event loop.
const downloadTimeout = 60 * time.Second

// ponytail: two downloads across all sessions, with no queue. If bursts cause loss,
// add a bounded queue before raising the memory budget.
var mediaDownloadSlots = make(chan struct{}, 2)

type mediaDownloadLimitKey struct{}

type mediaDownloadTransport struct{ http.RoundTripper }

func (t mediaDownloadTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.RoundTripper.RoundTrip(req)
	if limit, ok := req.Context().Value(mediaDownloadLimitKey{}).(int64); ok && err == nil && resp.StatusCode == http.StatusOK {
		resp.Body = http.MaxBytesReader(nil, resp.Body, limit)
	}
	return resp, err
}

// Why an attachment was not stored. The message is still delivered, with its caption.
const (
	MediaErrorTooLarge       = "too_large"
	MediaErrorDownloadFailed = "download_failed"
	MediaErrorNoStore        = "no_store"
)

// incomingMedia is the attachment part of a received message, before download.
type incomingMedia struct {
	part     whatsmeow.DownloadableMessage
	info     domain.ReceivedMedia
	caption  string
	declared uint64
}

// mediaPart finds the attachment in a message. Stickers, contacts, locations and the rest
// are not attachments for v1.1 and are ignored, as before.
func mediaPart(message *waE2E.Message) (incomingMedia, bool) {
	switch {
	case message.GetImageMessage() != nil:
		image := message.GetImageMessage()
		return incomingMedia{
			part:     image,
			info:     domain.ReceivedMedia{Kind: domain.MediaImage, MimeType: image.GetMimetype()},
			caption:  image.GetCaption(),
			declared: image.GetFileLength(),
		}, true
	case message.GetAudioMessage() != nil:
		audio := message.GetAudioMessage()
		return incomingMedia{
			part:     audio,
			info:     domain.ReceivedMedia{Kind: domain.MediaAudio, MimeType: audio.GetMimetype(), Voice: audio.GetPTT()},
			declared: audio.GetFileLength(),
		}, true
	case message.GetVideoMessage() != nil:
		video := message.GetVideoMessage()
		return incomingMedia{
			part:     video,
			info:     domain.ReceivedMedia{Kind: domain.MediaVideo, MimeType: video.GetMimetype()},
			caption:  video.GetCaption(),
			declared: video.GetFileLength(),
		}, true
	case message.GetDocumentMessage() != nil:
		document := message.GetDocumentMessage()
		return incomingMedia{
			part:     document,
			info:     domain.ReceivedMedia{Kind: domain.MediaDocument, MimeType: document.GetMimetype(), FileName: document.GetFileName()},
			caption:  document.GetCaption(),
			declared: document.GetFileLength(),
		}, true
	}
	return incomingMedia{}, false
}

// downloader is the part of the WhatsApp client used to fetch attachments (fakeable in tests).
type downloader func(ctx context.Context, part whatsmeow.DownloadableMessage) ([]byte, error)

// receivedMedia downloads and stores the attachment, then builds the webhook message. Failures
// do not drop the message: it goes out with MediaError set, so the consumer can answer.
func receivedMedia(ctx context.Context, store *media.Store, download downloader, session string, recipient types.JID, message *events.Message, incoming incomingMedia) domain.ReceivedMessage {
	received := domain.ReceivedMessage{
		ID:        string(message.Info.ID),
		Session:   session,
		From:      message.Info.Sender.String(),
		To:        recipient.String(),
		Text:      incoming.caption,
		Timestamp: message.Info.Timestamp.UTC(),
	}
	switch {
	case store == nil:
		received.MediaError = MediaErrorNoStore
	case incoming.declared > uint64(store.MaxBytes()):
		received.MediaError = MediaErrorTooLarge
	default:
		select {
		case mediaDownloadSlots <- struct{}{}:
			defer func() { <-mediaDownloadSlots }()
		default:
			received.MediaError = MediaErrorDownloadFailed
			return received
		}
		limit := store.MaxBytes()
		if incoming.part.GetFileEncSHA256() != nil {
			limit += aes.BlockSize + 10 // Maximum CBC padding plus the truncated WhatsApp HMAC.
		}
		data, err := download(context.WithValue(ctx, mediaDownloadLimitKey{}, limit), incoming.part)
		var tooLarge *http.MaxBytesError
		if err != nil {
			if errors.As(err, &tooLarge) {
				received.MediaError = MediaErrorTooLarge
			} else {
				received.MediaError = MediaErrorDownloadFailed
			}
			break
		}
		info := incoming.info
		info.ID = mediaID(received.ID)
		saved, err := store.Save(session, info, data)
		switch {
		case errors.Is(err, domain.ErrTooLarge):
			received.MediaError = MediaErrorTooLarge
		case err != nil:
			received.MediaError = MediaErrorDownloadFailed
		default:
			received.Media = &saved
		}
	}
	return received
}

// mediaID keeps the WhatsApp ID when it is safe as a file name; otherwise it hashes it.
func mediaID(messageID string) string {
	if media.ValidID(messageID) {
		return messageID
	}
	return "m" + strings.ToLower(hashHex(messageID))
}

func uploadType(kind domain.MediaKind) whatsmeow.MediaType {
	switch kind {
	case domain.MediaImage:
		return whatsmeow.MediaImage
	case domain.MediaAudio:
		return whatsmeow.MediaAudio
	case domain.MediaVideo:
		return whatsmeow.MediaVideo
	default:
		return whatsmeow.MediaDocument
	}
}

// mediaMessage builds the protobuf for an uploaded attachment.
func mediaMessage(request domain.MediaMessageRequest, upload whatsmeow.UploadResponse) *waE2E.Message {
	caption := optional(request.Caption)
	switch request.Kind {
	case domain.MediaImage:
		return &waE2E.Message{ImageMessage: &waE2E.ImageMessage{
			URL: proto.String(upload.URL), DirectPath: proto.String(upload.DirectPath),
			MediaKey: upload.MediaKey, FileEncSHA256: upload.FileEncSHA256, FileSHA256: upload.FileSHA256,
			FileLength: proto.Uint64(upload.FileLength), Mimetype: proto.String(request.MimeType),
			Caption: caption,
		}}
	case domain.MediaAudio:
		return &waE2E.Message{AudioMessage: &waE2E.AudioMessage{
			URL: proto.String(upload.URL), DirectPath: proto.String(upload.DirectPath),
			MediaKey: upload.MediaKey, FileEncSHA256: upload.FileEncSHA256, FileSHA256: upload.FileSHA256,
			FileLength: proto.Uint64(upload.FileLength), Mimetype: proto.String(request.MimeType),
			PTT: proto.Bool(request.Voice),
		}}
	case domain.MediaVideo:
		return &waE2E.Message{VideoMessage: &waE2E.VideoMessage{
			URL: proto.String(upload.URL), DirectPath: proto.String(upload.DirectPath),
			MediaKey: upload.MediaKey, FileEncSHA256: upload.FileEncSHA256, FileSHA256: upload.FileSHA256,
			FileLength: proto.Uint64(upload.FileLength), Mimetype: proto.String(request.MimeType),
			Caption: caption,
		}}
	default:
		return &waE2E.Message{DocumentMessage: &waE2E.DocumentMessage{
			URL: proto.String(upload.URL), DirectPath: proto.String(upload.DirectPath),
			MediaKey: upload.MediaKey, FileEncSHA256: upload.FileEncSHA256, FileSHA256: upload.FileSHA256,
			FileLength: proto.Uint64(upload.FileLength), Mimetype: proto.String(request.MimeType),
			FileName: optional(request.FileName), Title: optional(request.FileName), Caption: caption,
		}}
	}
}

func optional(text string) *string {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	return proto.String(text)
}

// SendMedia uploads the attachment to WhatsApp and sends it.
func (w *WhatsApp) SendMedia(ctx context.Context, session domain.Session, request domain.MediaMessageRequest) (domain.MessageResult, error) {
	if err := ctx.Err(); err != nil {
		return domain.MessageResult{}, err
	}
	recipient, to, err := ParseDestination(request.To)
	if err != nil {
		return domain.MessageResult{}, err
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	upload, err := w.client.Upload(ctx, request.Data, uploadType(request.Kind))
	if err != nil {
		return domain.MessageResult{}, sendError(err)
	}
	response, err := w.client.SendMessage(ctx, recipient, mediaMessage(request, upload))
	if err != nil {
		return domain.MessageResult{}, sendError(err)
	}
	return domain.MessageResult{
		ID:        string(response.ID),
		Session:   session.Name,
		To:        to,
		Status:    "sent",
		Timestamp: response.Timestamp.UTC(),
	}, nil
}

// SetMediaStore enables attachments. Without a store, received attachments are reported with
// MediaError "no_store" and never written to disk.
func (w *WhatsApp) SetMediaStore(store *media.Store) {
	w.mu.Lock()
	w.media = store
	w.mu.Unlock()
}
