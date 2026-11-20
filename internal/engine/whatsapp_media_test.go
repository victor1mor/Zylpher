package engine

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.mau.fi/whatsmeow"
	waE2E "go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"

	"github.com/victor1mor/Zylpher/internal/domain"
	"github.com/victor1mor/Zylpher/internal/media"
)

func mediaEvent(id string, message *waE2E.Message) *events.Message {
	return &events.Message{
		Info: types.MessageInfo{
			MessageSource: types.MessageSource{Sender: types.NewJID("5511999999999", types.DefaultUserServer)},
			ID:            types.MessageID(id),
			Timestamp:     time.Date(2026, time.October, 27, 12, 0, 0, 0, time.UTC),
		},
		Message: message,
	}
}

func TestMediaPartFindsEachKind(t *testing.T) {
	cases := []struct {
		message *waE2E.Message
		want    domain.ReceivedMedia
		caption string
	}{
		{&waE2E.Message{ImageMessage: &waE2E.ImageMessage{Mimetype: proto.String("image/jpeg"), Caption: proto.String("olha")}},
			domain.ReceivedMedia{Kind: domain.MediaImage, MimeType: "image/jpeg"}, "olha"},
		{&waE2E.Message{AudioMessage: &waE2E.AudioMessage{Mimetype: proto.String("audio/ogg; codecs=opus"), PTT: proto.Bool(true)}},
			domain.ReceivedMedia{Kind: domain.MediaAudio, MimeType: "audio/ogg; codecs=opus", Voice: true}, ""},
		{&waE2E.Message{VideoMessage: &waE2E.VideoMessage{Mimetype: proto.String("video/mp4")}},
			domain.ReceivedMedia{Kind: domain.MediaVideo, MimeType: "video/mp4"}, ""},
		{&waE2E.Message{DocumentMessage: &waE2E.DocumentMessage{Mimetype: proto.String("application/pdf"), FileName: proto.String("nota.pdf")}},
			domain.ReceivedMedia{Kind: domain.MediaDocument, MimeType: "application/pdf", FileName: "nota.pdf"}, ""},
	}
	for _, c := range cases {
		incoming, ok := mediaPart(c.message)
		if !ok || incoming.info != c.want || incoming.caption != c.caption {
			t.Fatalf("mediaPart = %#v, %t; want %#v", incoming.info, ok, c.want)
		}
	}
	if _, ok := mediaPart(&waE2E.Message{Conversation: proto.String("oi")}); ok {
		t.Fatal("text is not media")
	}
}

func TestReceivedMediaStoresAndReports(t *testing.T) {
	store := media.NewStore(t.TempDir(), 16)
	recipient := types.NewJID("5511888888888", types.DefaultUserServer)
	message := mediaEvent("3EB0FOTO", &waE2E.Message{ImageMessage: &waE2E.ImageMessage{
		Mimetype: proto.String("image/jpeg"), Caption: proto.String("olha"), FileLength: proto.Uint64(4),
	}})
	incoming, _ := mediaPart(message.Message)
	download := func(context.Context, whatsmeow.DownloadableMessage) ([]byte, error) { return []byte("jpeg"), nil }

	received := receivedMedia(context.Background(), store, download, "default", recipient, message, incoming)
	if received.Text != "olha" || received.MediaError != "" || received.Media == nil ||
		received.Media.ID != "3EB0FOTO" || received.Media.Size != 4 || received.From == "" {
		t.Fatalf("received = %#v / %#v", received, received.Media)
	}
	if _, data, err := store.Open("default", "3EB0FOTO"); err != nil || string(data) != "jpeg" {
		t.Fatalf("stored = %q, %v", data, err)
	}
}

func TestReceivedMediaLimitsHTTPBody(t *testing.T) {
	const limit = 64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Flush headers before writing so the response has no Content-Length.
		w.(http.Flusher).Flush()
		_, _ = io.WriteString(w, strings.Repeat("x", limit+1))
	}))
	defer server.Close()
	client := server.Client()
	client.Transport = mediaDownloadTransport{client.Transport}
	message := mediaEvent("foto", &waE2E.Message{ImageMessage: &waE2E.ImageMessage{}})
	incoming, _ := mediaPart(message.Message)
	store := media.NewStore(t.TempDir(), limit)
	download := func(ctx context.Context, _ whatsmeow.DownloadableMessage) ([]byte, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL, nil)
		if err != nil {
			return nil, err
		}
		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		data, err := io.ReadAll(resp.Body)
		if len(data) != limit {
			t.Fatalf("read %d bytes, want %d", len(data), limit)
		}
		return data, err
	}
	received := receivedMedia(context.Background(), store, download, "default", types.EmptyJID, message, incoming)
	if received.MediaError != MediaErrorTooLarge || received.Media != nil {
		t.Fatalf("received = %#v, want too_large", received)
	}
	if _, _, err := store.Open("default", "foto"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("oversized attachment was retained: %v", err)
	}
}

func TestReceivedMediaFailuresStillDeliver(t *testing.T) {
	recipient := types.NewJID("5511888888888", types.DefaultUserServer)
	big := mediaEvent("grande", &waE2E.Message{VideoMessage: &waE2E.VideoMessage{FileLength: proto.Uint64(1 << 30)}})
	never := func(context.Context, whatsmeow.DownloadableMessage) ([]byte, error) {
		t.Fatal("must not download")
		return nil, nil
	}
	failing := func(context.Context, whatsmeow.DownloadableMessage) ([]byte, error) { return nil, errors.New("rede") }
	small := mediaEvent("pequena", &waE2E.Message{ImageMessage: &waE2E.ImageMessage{FileLength: proto.Uint64(4), Caption: proto.String("olha")}})

	store := media.NewStore(t.TempDir(), 16)
	for _, c := range []struct {
		store    *media.Store
		message  *events.Message
		download downloader
		want     string
	}{
		{store, big, never, MediaErrorTooLarge},
		{nil, small, never, MediaErrorNoStore},
		{store, small, failing, MediaErrorDownloadFailed},
	} {
		incoming, _ := mediaPart(c.message.Message)
		received := receivedMedia(context.Background(), c.store, c.download, "default", recipient, c.message, incoming)
		if received.MediaError != c.want || received.Media != nil {
			t.Fatalf("received = %#v, want error %q", received, c.want)
		}
	}
	for range cap(mediaDownloadSlots) {
		select {
		case mediaDownloadSlots <- struct{}{}:
			defer func() { <-mediaDownloadSlots }()
		default:
			t.Fatal("previous download did not release its slot")
		}
	}
	incoming, _ := mediaPart(small.Message)
	received := receivedMedia(context.Background(), store, never, "second", recipient, small, incoming)
	if received.Text != "olha" || received.MediaError != MediaErrorDownloadFailed || received.Media != nil {
		t.Fatalf("received with no download slot = %#v", received)
	}
}

func TestMediaMessageBuildsVoiceAndDocument(t *testing.T) {
	upload := whatsmeow.UploadResponse{URL: "u", DirectPath: "/d", MediaKey: []byte{1}, FileEncSHA256: []byte{2}, FileSHA256: []byte{3}, FileLength: 9}

	voice := mediaMessage(domain.MediaMessageRequest{Kind: domain.MediaAudio, MimeType: "audio/ogg; codecs=opus", Voice: true}, upload)
	if audio := voice.GetAudioMessage(); audio == nil || !audio.GetPTT() || audio.GetFileLength() != 9 || audio.GetMimetype() != "audio/ogg; codecs=opus" {
		t.Fatalf("voice = %#v", voice)
	}

	document := mediaMessage(domain.MediaMessageRequest{Kind: domain.MediaDocument, MimeType: "application/pdf", FileName: "nota.pdf", Caption: "segue"}, upload)
	if doc := document.GetDocumentMessage(); doc == nil || doc.GetFileName() != "nota.pdf" || doc.GetCaption() != "segue" || doc.GetDirectPath() != "/d" {
		t.Fatalf("document = %#v", document)
	}

	image := mediaMessage(domain.MediaMessageRequest{Kind: domain.MediaImage, MimeType: "image/png"}, upload)
	if img := image.GetImageMessage(); img == nil || img.Caption != nil {
		t.Fatalf("image without caption = %#v", image)
	}
}

func TestMediaIDHashesUnsafeIDs(t *testing.T) {
	if got := mediaID("3EB0ABC"); got != "3EB0ABC" {
		t.Fatalf("safe id changed: %q", got)
	}
	got := mediaID("../../fora")
	if !media.ValidID(got) || got == "../../fora" {
		t.Fatalf("unsafe id kept: %q", got)
	}
}
