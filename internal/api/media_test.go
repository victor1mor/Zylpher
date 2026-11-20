package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/victor1mor/Zylpher/internal/domain"
	"github.com/victor1mor/Zylpher/internal/engine"
	"github.com/victor1mor/Zylpher/internal/media"
	"github.com/victor1mor/Zylpher/internal/session"
)

// recordingEngine keeps the last media request, to check what reached the engine.
type recordingEngine struct {
	*engine.Fake
	last domain.MediaMessageRequest
}

func (e *recordingEngine) SendMedia(ctx context.Context, s domain.Session, request domain.MediaMessageRequest) (domain.MessageResult, error) {
	e.last = request
	return e.Fake.SendMedia(ctx, s, request)
}

func newMediaRouter(t *testing.T, maxBytes int64) (http.Handler, *media.Store, *recordingEngine) {
	t.Helper()
	store := media.NewStore(t.TempDir(), maxBytes)
	eng := &recordingEngine{Fake: engine.NewFake()}
	router := NewRouterWithMedia(slog.New(slog.NewTextHandler(io.Discard, nil)), session.NewManager(), eng, nil, store)
	doJSON[domain.Session](t, router, http.MethodPost, "/sessions", `{"name":"default"}`, http.StatusCreated)
	doJSON[domain.Session](t, router, http.MethodPost, "/sessions/default/start", "", http.StatusOK)
	return router, store, eng
}

func sendMedia(t *testing.T, router http.Handler, query, contentType string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/sessions/default/messages/media?"+query, bytes.NewReader(body))
	req.Header.Set("Content-Type", contentType)
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)
	return res
}

func TestSendMediaFlow(t *testing.T) {
	router, _, eng := newMediaRouter(t, 1024)
	res := sendMedia(t, router, "to=%2B5511999999999&kind=audio&voice=true", "audio/ogg; codecs=opus", []byte("OggS..."))
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", res.Code, res.Body)
	}
	if eng.last.To != "5511999999999" || eng.last.Kind != domain.MediaAudio || !eng.last.Voice ||
		eng.last.MimeType != "audio/ogg; codecs=opus" || string(eng.last.Data) != "OggS..." {
		t.Fatalf("engine got %#v", eng.last)
	}

	res = sendMedia(t, router, "to=5511999999999&kind=document&file_name=nota.pdf&caption=segue", "application/pdf", []byte("%PDF"))
	if res.Code != http.StatusOK || eng.last.FileName != "nota.pdf" || eng.last.Caption != "segue" {
		t.Fatalf("document: status %d, engine %#v", res.Code, eng.last)
	}
}

func TestReplyDestinationForTextAndMedia(t *testing.T) {
	for _, tc := range []struct {
		from, want string
	}{
		{"5511999999999@s.whatsapp.net", "5511999999999@s.whatsapp.net"},
		{"12345678901234567@lid", "12345678901234567@lid"},
		{"12345678901234567:3@lid", "12345678901234567@lid"},
		{"5511999999999@g.us", ""},
		{"not-a-contact@lid", ""},
	} {
		t.Run(tc.from, func(t *testing.T) {
			router, _, eng := newMediaRouter(t, 1024)
			body, err := json.Marshal(domain.TextMessageRequest{To: tc.from, Text: "oi"})
			if err != nil {
				t.Fatal(err)
			}
			const textPath = "/sessions/default/messages/text"
			if tc.want == "" {
				doJSON[domain.ErrorResponse](t, router, http.MethodPost, textPath, string(body), http.StatusBadRequest)
			} else {
				response := doJSON[domain.MessageResult](t, router, http.MethodPost, textPath, string(body), http.StatusOK)
				if response.To != tc.want {
					t.Fatalf("text target = %q, want %q", response.To, tc.want)
				}
			}
			res := sendMedia(t, router, "to="+url.QueryEscape(tc.from)+"&kind=image", "image/jpeg", []byte("jpeg"))
			wantStatus := http.StatusOK
			if tc.want == "" {
				wantStatus = http.StatusBadRequest
			}
			if res.Code != wantStatus || eng.last.To != tc.want {
				t.Fatalf("media status = %d, target = %q, want %d and %q", res.Code, eng.last.To, wantStatus, tc.want)
			}
		})
	}
}

func TestSendMediaRefusesInvalid(t *testing.T) {
	router, _, _ := newMediaRouter(t, 8)
	for _, c := range []struct {
		query, contentType string
		body               []byte
		want               int
	}{
		{"to=5511999999999&kind=sticker", "image/webp", []byte("x"), http.StatusBadRequest},
		{"to=5511999999999&kind=image", "text/html", []byte("x"), http.StatusBadRequest},
		{"to=5511999999999&kind=image&voice=true", "image/png", []byte("x"), http.StatusBadRequest},
		{"to=5511999999999&kind=audio&voice=true", "audio/mpeg", []byte("x"), http.StatusBadRequest},
		{"to=5511999999999&kind=audio&caption=oi", "audio/mpeg", []byte("x"), http.StatusBadRequest},
		{"to=5511999999999&kind=document&file_name=../x", "application/pdf", []byte("x"), http.StatusBadRequest},
		{"to=123&kind=image", "image/png", []byte("x"), http.StatusBadRequest},
		{"to=5511999999999&kind=image", "image/png", nil, http.StatusBadRequest},
		{"to=5511999999999&kind=image", "image/png", []byte("grande demais"), http.StatusRequestEntityTooLarge},
	} {
		if res := sendMedia(t, router, c.query, c.contentType, c.body); res.Code != c.want {
			t.Fatalf("%s (%s): status %d, want %d, body %s", c.query, c.contentType, res.Code, c.want, res.Body)
		}
	}
}

func TestSendMediaNeedsWorkingSession(t *testing.T) {
	router := NewRouterWithMedia(slog.New(slog.NewTextHandler(io.Discard, nil)), session.NewManager(), engine.NewFake(), nil, nil)
	doJSON[domain.Session](t, router, http.MethodPost, "/sessions", `{"name":"default"}`, http.StatusCreated)
	if res := sendMedia(t, router, "to=5511999999999&kind=image", "image/png", []byte("x")); res.Code != http.StatusConflict {
		t.Fatalf("status = %d", res.Code)
	}
}

func TestGetAndDeleteMedia(t *testing.T) {
	router, store, _ := newMediaRouter(t, 1024)
	saved, err := store.Save("default", domain.ReceivedMedia{ID: "3EB0AA", Kind: domain.MediaImage, MimeType: "image/jpeg"}, []byte("jpeg"))
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/sessions/default/media/3EB0AA", nil)
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)
	if res.Code != http.StatusOK || res.Body.String() != "jpeg" {
		t.Fatalf("get = %d %q", res.Code, res.Body)
	}
	for header, want := range map[string]string{
		"Content-Type":           "image/jpeg",
		"X-Content-Type-Options": "nosniff",
		"Content-Disposition":    "attachment",
		"X-Zylpher-SHA256":       saved.SHA256,
	} {
		if got := res.Header().Get(header); got != want {
			t.Fatalf("%s = %q, want %q", header, got, want)
		}
	}

	req = httptest.NewRequest(http.MethodDelete, "/sessions/default/media/3EB0AA", nil)
	res = httptest.NewRecorder()
	router.ServeHTTP(res, req)
	if res.Code != http.StatusNoContent {
		t.Fatalf("delete = %d", res.Code)
	}
	doJSON[domain.ErrorResponse](t, router, http.MethodGet, "/sessions/default/media/3EB0AA", "", http.StatusNotFound)
	doJSON[domain.ErrorResponse](t, router, http.MethodGet, "/sessions/missing/media/3EB0AA", "", http.StatusNotFound)
	doJSON[domain.ErrorResponse](t, router, http.MethodGet, "/sessions/default/media/a.b", "", http.StatusBadRequest)
}

func TestMediaDisabledWithoutStore(t *testing.T) {
	router := newTestRouter()
	response := doJSON[domain.ErrorResponse](t, router, http.MethodGet, "/sessions/default/media/abc", "", http.StatusNotFound)
	if response.Error != "media_disabled" {
		t.Fatalf("error = %q", response.Error)
	}
}
