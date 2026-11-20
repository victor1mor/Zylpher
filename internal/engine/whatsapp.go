package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.mau.fi/whatsmeow"
	waE2E "go.mau.fi/whatsmeow/proto/waE2E"
	waStore "go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"
	_ "modernc.org/sqlite"

	"github.com/victor1mor/Zylpher/internal/domain"
	"github.com/victor1mor/Zylpher/internal/media"
)

type WhatsApp struct {
	mu         sync.Mutex
	client     *whatsmeow.Client
	container  *sqlstore.Container
	session    string
	events     domain.EventHandler
	statuses   StatusHandler
	qrCode     string
	pairCancel context.CancelFunc
	media      *media.Store
}

const connectionTimeout = 30 * time.Second

var configureWhatsApp sync.Once

func NewWhatsApp(ctx context.Context, database string) (*WhatsApp, error) {
	configureWhatsApp.Do(func() { waStore.SetOSInfo("Zylpher", [3]uint32{0, 1, 0}) })
	if strings.TrimSpace(database) == "" {
		return nil, domain.ErrInvalidInput
	}
	if err := os.MkdirAll(filepath.Dir(database), 0o700); err != nil {
		return nil, fmt.Errorf("create credential directory: %w", err)
	}
	if err := os.Chmod(filepath.Dir(database), 0o700); err != nil {
		return nil, fmt.Errorf("restrict credential directory: %w", err)
	}

	container, err := sqlstore.New(ctx, "sqlite", databaseURI(database), nil)
	if err != nil {
		return nil, fmt.Errorf("open credential store: %w", err)
	}
	if err := os.Chmod(database, 0o600); err != nil {
		_ = container.Close()
		return nil, fmt.Errorf("restrict credential database: %w", err)
	}
	device, err := container.GetFirstDevice(ctx)
	if err != nil {
		_ = container.Close()
		return nil, fmt.Errorf("load device: %w", err)
	}
	whatsapp := &WhatsApp{client: whatsmeow.NewClient(device, nil), container: container}
	whatsapp.client.SetMediaHTTPClient(&http.Client{Transport: mediaDownloadTransport{http.DefaultTransport}})
	whatsapp.client.AddEventHandler(whatsapp.handleEvent)
	return whatsapp, nil
}

func (w *WhatsApp) NeedsPairing() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.client.Store.ID == nil
}

func (w *WhatsApp) Connect(ctx context.Context, session domain.Session) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	w.mu.Lock()
	w.session = session.Name
	if w.client.Store.ID == nil {
		err := w.startPairingLocked()
		w.mu.Unlock()
		return err
	}
	w.mu.Unlock()
	if err := w.client.Connect(); err != nil {
		return fmt.Errorf("connect WhatsApp: %w", err)
	}
	if !w.client.WaitForConnection(connectionTimeout) {
		return errors.New("WhatsApp connection timeout")
	}
	return nil
}

func (w *WhatsApp) SetEventHandler(handler domain.EventHandler) {
	w.mu.Lock()
	w.events = handler
	w.mu.Unlock()
}

func (w *WhatsApp) SetStatusHandler(handler StatusHandler) {
	w.mu.Lock()
	w.statuses = handler
	w.mu.Unlock()
}

func (w *WhatsApp) Disconnect(ctx context.Context, _ domain.Session) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	w.mu.Lock()
	if w.pairCancel != nil {
		w.pairCancel()
		w.pairCancel = nil
	}
	w.qrCode = ""
	w.client.Disconnect()
	w.mu.Unlock()
	return nil
}

func (w *WhatsApp) Status(ctx context.Context, _ domain.Session) (domain.SessionStatus, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.client.IsLoggedIn() {
		return domain.SessionWorking, nil
	}
	if w.qrCode != "" || w.pairCancel != nil || w.client.IsConnected() {
		return domain.SessionStarting, nil
	}
	return domain.SessionStopped, nil
}

func (w *WhatsApp) QRCode(ctx context.Context, _ domain.Session) (string, bool, error) {
	if err := ctx.Err(); err != nil {
		return "", false, err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.qrCode, w.qrCode != "", nil
}

func (w *WhatsApp) SendText(ctx context.Context, session domain.Session, request domain.TextMessageRequest) (domain.MessageResult, error) {
	if err := ctx.Err(); err != nil {
		return domain.MessageResult{}, err
	}
	recipient, to, err := ParseDestination(request.To)
	if err != nil {
		return domain.MessageResult{}, err
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	response, err := w.client.SendMessage(ctx, recipient, textMessage(request.Text))
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

// ParseDestination returns an individual WhatsApp address and its canonical API representation.
// Bare phone numbers retain their existing digits-only representation.
func ParseDestination(input string) (types.JID, string, error) {
	input = strings.TrimSpace(input)
	user, server, qualified := strings.Cut(input, "@")
	if !qualified {
		user = strings.TrimPrefix(user, "+")
		server = types.DefaultUserServer
	}
	if server != types.DefaultUserServer && server != types.HiddenUserServer {
		return types.EmptyJID, "", domain.ErrInvalidInput
	}
	jid := types.NewJID(user, server)
	if qualified {
		parsed, err := types.ParseJID(input)
		// ParseJID can truncate out-of-range device and agent numbers.
		if err != nil || parsed.String() != input {
			return types.EmptyJID, "", domain.ErrInvalidInput
		}
		jid = parsed.ToNonAD()
	}
	id, err := strconv.ParseUint(jid.User, 10, 64)
	if err != nil || id == 0 {
		return types.EmptyJID, "", domain.ErrInvalidInput
	}
	if server == types.DefaultUserServer && (len(jid.User) < 8 || len(jid.User) > 15 || jid.User[0] == '0') {
		return types.EmptyJID, "", domain.ErrInvalidInput
	}
	if qualified {
		return jid, jid.String(), nil
	}
	return jid, user, nil
}

func textMessage(text string) *waE2E.Message {
	return &waE2E.Message{Conversation: proto.String(text)}
}

func receivedMessage(session string, recipient types.JID, message *events.Message) (domain.ReceivedMessage, bool) {
	text := message.Message.GetConversation()
	if text == "" {
		text = message.Message.GetExtendedTextMessage().GetText()
	}
	if text == "" {
		return domain.ReceivedMessage{}, false
	}
	return domain.ReceivedMessage{
		ID:        string(message.Info.ID),
		Session:   session,
		From:      message.Info.Sender.String(),
		To:        recipient.String(),
		Text:      text,
		Timestamp: message.Info.Timestamp.UTC(),
	}, true
}

func (w *WhatsApp) handleEvent(event any) {
	switch value := event.(type) {
	case *events.Message:
		w.handleMessage(value)
	case *events.Connected:
		w.emitStatus(domain.SessionWorking)
	case *events.Disconnected:
		w.emitStatus(domain.SessionStarting)
	case events.PermanentDisconnect:
		w.emitStatus(domain.SessionFailed)
	}
}

func (w *WhatsApp) handleMessage(message *events.Message) {
	w.mu.Lock()
	handler := w.events
	session := w.session
	store := w.media
	var recipient types.JID
	if w.client.Store.ID != nil {
		recipient = *w.client.Store.ID
	}
	w.mu.Unlock()

	if handler == nil || session == "" || recipient.IsEmpty() {
		return
	}
	if incoming, ok := mediaPart(message.Message); ok {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), downloadTimeout)
			defer cancel()
			handler(domain.MessageReceivedEvent(receivedMedia(ctx, store, w.client.Download, session, recipient, message, incoming)))
		}()
		return
	}
	received, ok := receivedMessage(session, recipient, message)
	if ok {
		// ponytail: one goroutine per text; add a bounded queue when sustained traffic proves necessary.
		go handler(domain.MessageReceivedEvent(received))
	}
}

func (w *WhatsApp) emitStatus(status domain.SessionStatus) {
	w.mu.Lock()
	handler := w.statuses
	session := w.session
	w.mu.Unlock()
	if handler != nil && session != "" {
		// ponytail: status persistence runs outside the whatsmeow event loop.
		go handler(session, status)
	}
}

func sendError(err error) error {
	switch {
	case errors.Is(err, whatsmeow.ErrNotConnected), errors.Is(err, whatsmeow.ErrNotLoggedIn):
		return domain.ErrSessionNotWorking
	case errors.Is(err, whatsmeow.ErrRecipientADJID), errors.Is(err, whatsmeow.ErrUnknownServer):
		return domain.ErrInvalidInput
	case errors.Is(err, whatsmeow.ErrMessageTimedOut), errors.Is(err, context.DeadlineExceeded):
		return domain.ErrSendTimedOut
	case errors.Is(err, whatsmeow.ErrServerReturnedError):
		return domain.ErrRemoteFailure
	default:
		return fmt.Errorf("send WhatsApp text: %w", err)
	}
}

func (w *WhatsApp) Pair(ctx context.Context, render func(string)) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.client.Store.ID != nil {
		return domain.ErrConflict
	}
	qrChannel, err := w.client.GetQRChannel(ctx)
	if err != nil {
		return fmt.Errorf("create QR channel: %w", err)
	}
	if err := w.client.Connect(); err != nil {
		return fmt.Errorf("connect for pairing: %w", err)
	}
	for item := range qrChannel {
		switch item.Event {
		case whatsmeow.QRChannelEventCode:
			render(item.Code)
		case whatsmeow.QRChannelSuccess.Event:
			return nil
		case whatsmeow.QRChannelEventError:
			if item.Error != nil {
				return fmt.Errorf("pairing: %w", item.Error)
			}
			return errors.New("pairing failed")
		default:
			return fmt.Errorf("pairing: %s", item.Event)
		}
	}
	return errors.New("QR channel closed before pairing")
}

func (w *WhatsApp) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.pairCancel != nil {
		w.pairCancel()
	}
	w.qrCode = ""
	w.client.Disconnect()
	return w.container.Close()
}

func (w *WhatsApp) startPairingLocked() error {
	if w.pairCancel != nil {
		return nil
	}
	pairCtx, cancel := context.WithCancel(context.Background())
	qrChannel, err := w.client.GetQRChannel(pairCtx)
	if err != nil {
		cancel()
		return fmt.Errorf("create QR channel: %w", err)
	}
	if err := w.client.Connect(); err != nil {
		cancel()
		return fmt.Errorf("connect for pairing: %w", err)
	}
	w.pairCancel = cancel
	go w.cacheQRCodes(qrChannel)
	return nil
}

func (w *WhatsApp) cacheQRCodes(qrChannel <-chan whatsmeow.QRChannelItem) {
	for item := range qrChannel {
		w.mu.Lock()
		switch item.Event {
		case whatsmeow.QRChannelEventCode:
			w.qrCode = item.Code
		default:
			w.qrCode = ""
			if w.pairCancel != nil {
				w.pairCancel()
				w.pairCancel = nil
			}
		}
		w.mu.Unlock()
	}
}

func hashHex(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:16])
}

func databaseURI(database string) string {
	path := filepath.ToSlash(database)
	absolute := filepath.IsAbs(database)
	if absolute && !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	prefix := "file:"
	if absolute {
		prefix = "file://"
	}
	return prefix + (&url.URL{Path: path}).EscapedPath() + "?_pragma=foreign_keys%281%29"
}
