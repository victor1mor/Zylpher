// Code generated from openapi.json by genclient. DO NOT EDIT.

package client

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

type GetHealthResponse struct {
	Status string `json:"status"`
}

type Session struct {
	CreatedAt time.Time `json:"created_at"`
	Name      string    `json:"name"`
	Status    string    `json:"status"`
	UpdatedAt time.Time `json:"updated_at"`
}

type CreateSessionRequest struct {
	Name string `json:"name"`
}

type SendMediaParams struct {
	To       string
	Kind     string
	Caption  string
	FileName string
	Voice    bool
}

type MessageResult struct {
	ID        string    `json:"id"`
	Session   string    `json:"session"`
	Status    string    `json:"status"`
	Timestamp time.Time `json:"timestamp"`
	To        string    `json:"to"`
}

type SendTextRequest struct {
	Text string `json:"text"`
	To   string `json:"to"`
}

type GetSessionQRResponse struct {
	Code    string `json:"code"`
	Session string `json:"session"`
}

// GetHealth Verificar se o servidor está ativo.
func (c *Client) GetHealth(ctx context.Context) (GetHealthResponse, error) {
	return readJSON[GetHealthResponse](c.request(ctx, "GET", "/health", 200, requestOptions{}))
}

// GetMetrics Consultar as métricas da fila de webhooks.
func (c *Client) GetMetrics(ctx context.Context) (string, error) {
	return readText(c.request(ctx, "GET", "/metrics", 200, requestOptions{}))
}

// ListSessions Listar sessões.
func (c *Client) ListSessions(ctx context.Context) ([]Session, error) {
	return readJSON[[]Session](c.request(ctx, "GET", "/sessions", 200, requestOptions{}))
}

// CreateSession Criar uma sessão em STOPPED.
func (c *Client) CreateSession(ctx context.Context, body CreateSessionRequest) (Session, error) {
	return writeJSON[Session](ctx, c, "POST", "/sessions", 201, body)
}

// GetSession Consultar a sessão e atualizar seu estado real.
func (c *Client) GetSession(ctx context.Context, name string) (Session, error) {
	return readJSON[Session](c.request(ctx, "GET", fmt.Sprintf("/sessions/%s", url.PathEscape(name)), 200, requestOptions{}))
}

// LogoutSession Desvincular o aparelho da sessão.
func (c *Client) LogoutSession(ctx context.Context, name string) (Session, error) {
	return readJSON[Session](c.request(ctx, "POST", fmt.Sprintf("/sessions/%s/logout", url.PathEscape(name)), 200, requestOptions{}))
}

// DownloadMedia Baixar um anexo recebido. Feche o corpo da resposta após a leitura.
func (c *Client) DownloadMedia(ctx context.Context, name string, id string) (*http.Response, error) {
	return c.request(ctx, "GET", fmt.Sprintf("/sessions/%s/media/%s", url.PathEscape(name), url.PathEscape(id)), 200, requestOptions{})
}

// DeleteMedia Excluir um anexo recebido.
func (c *Client) DeleteMedia(ctx context.Context, name string, id string) error {
	return closeResponse(c.request(ctx, "DELETE", fmt.Sprintf("/sessions/%s/media/%s", url.PathEscape(name), url.PathEscape(id)), 204, requestOptions{}))
}

// SendMedia Enviar um arquivo em uma sessão WORKING.
func (c *Client) SendMedia(ctx context.Context, name string, params SendMediaParams, contentType string, body io.Reader) (MessageResult, error) {
	query := url.Values{}
	query.Set("to", params.To)
	query.Set("kind", params.Kind)
	if params.Caption != "" {
		query.Set("caption", params.Caption)
	}
	if params.FileName != "" {
		query.Set("file_name", params.FileName)
	}
	if params.Voice {
		query.Set("voice", fmt.Sprint(params.Voice))
	}
	return readJSON[MessageResult](c.request(ctx, "POST", fmt.Sprintf("/sessions/%s/messages/media", url.PathEscape(name)), 200, requestOptions{query: query, contentType: contentType, body: body}))
}

// SendText Enviar texto em uma sessão WORKING.
func (c *Client) SendText(ctx context.Context, name string, body SendTextRequest) (MessageResult, error) {
	return writeJSON[MessageResult](ctx, c, "POST", fmt.Sprintf("/sessions/%s/messages/text", url.PathEscape(name)), 200, body)
}

// GetSessionQR Consultar o QR textual do pareamento.
func (c *Client) GetSessionQR(ctx context.Context, name string) (GetSessionQRResponse, error) {
	return readJSON[GetSessionQRResponse](c.request(ctx, "GET", fmt.Sprintf("/sessions/%s/qr", url.PathEscape(name)), 200, requestOptions{}))
}

// StartSession Iniciar a sessão.
func (c *Client) StartSession(ctx context.Context, name string) (Session, error) {
	return readJSON[Session](c.request(ctx, "POST", fmt.Sprintf("/sessions/%s/start", url.PathEscape(name)), 200, requestOptions{}))
}

// StopSession Parar a sessão.
func (c *Client) StopSession(ctx context.Context, name string) (Session, error) {
	return readJSON[Session](c.request(ctx, "POST", fmt.Sprintf("/sessions/%s/stop", url.PathEscape(name)), 200, requestOptions{}))
}
