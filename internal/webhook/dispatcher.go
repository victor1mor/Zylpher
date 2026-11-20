package webhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/victor1mor/Zylpher/internal/domain"
)

type Dispatcher struct {
	url    string
	secret []byte
	client *http.Client
	outbox *outbox
}

func NewDispatcher(url string, secret string, client *http.Client) *Dispatcher {
	if client == nil {
		client = http.DefaultClient
	}
	return &Dispatcher{url: url, secret: []byte(secret), client: client}
}

func (d *Dispatcher) Dispatch(ctx context.Context, event domain.WebhookEvent) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if d == nil || d.url == "" {
		return nil
	}
	if len(d.secret) == 0 {
		return errors.New("webhook secret is required")
	}

	body, err := json.Marshal(event)
	if err != nil {
		return err
	}
	if d.outbox != nil {
		return d.outbox.enqueue(ctx, body)
	}
	return d.send(ctx, body)
}

func (d *Dispatcher) send(ctx context.Context, body []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Zylpher")
	signature := hmac.New(sha256.New, d.secret)
	_, _ = signature.Write(body)
	req.Header.Set("X-Zylpher-Signature", fmt.Sprintf("sha256=%x", signature.Sum(nil)))

	res, err := d.client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode < http.StatusOK || res.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("webhook returned status %d", res.StatusCode)
	}
	return nil
}
