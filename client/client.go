// Package client provides a Go client for the Zylpher HTTP API.
package client

//go:generate go run ../internal/genclient ../openapi.json client.gen.go

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client sends authenticated requests to a Zylpher instance.
type Client struct {
	baseURL string
	apiKey  string
	http    *http.Client
}

// New creates a client. A nil httpClient uses a 30-second timeout.
// Redirects are returned as errors so the client never resends the API key.
func New(baseURL, apiKey string, httpClient *http.Client) (*Client, error) {
	base, err := url.Parse(baseURL)
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Hostname() == "" ||
		base.User != nil || base.RawQuery != "" || base.ForceQuery || strings.Contains(baseURL, "#") {
		return nil, errors.New("Zylpher base URL must be HTTP(S), without credentials, query or fragment")
	}
	if apiKey == "" {
		return nil, errors.New("Zylpher API key is required")
	}
	transport := http.Client{Timeout: 30 * time.Second}
	if httpClient != nil {
		transport = *httpClient
	}
	transport.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{baseURL: strings.TrimRight(base.String(), "/"), apiKey: apiKey, http: &transport}, nil
}

// APIError is returned when the server responds with an unexpected HTTP status.
type APIError struct {
	StatusCode int    `json:"-"`
	Code       string `json:"error"`
	Message    string `json:"message"`
}

func (e *APIError) Error() string {
	if e.Code == "" {
		return fmt.Sprintf("Zylpher HTTP %d", e.StatusCode)
	}
	return fmt.Sprintf("Zylpher HTTP %d (%s)", e.StatusCode, e.Code)
}

type requestOptions struct {
	query       url.Values
	contentType string
	body        io.Reader
}

func (c *Client) request(ctx context.Context, method, path string, status int, options requestOptions) (*http.Response, error) {
	target := c.baseURL + path
	if len(options.query) != 0 {
		target += "?" + options.query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, target, options.body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	if options.contentType != "" {
		req.Header.Set("Content-Type", options.contentType)
	}
	res, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	if res.StatusCode == status {
		return res, nil
	}
	defer res.Body.Close()
	apiErr := &APIError{StatusCode: res.StatusCode}
	_ = json.NewDecoder(io.LimitReader(res.Body, 64<<10)).Decode(apiErr)
	return nil, apiErr
}

func writeJSON[T any](ctx context.Context, c *Client, method, path string, status int, input any) (T, error) {
	body, err := json.Marshal(input)
	if err != nil {
		var zero T
		return zero, err
	}
	return readJSON[T](c.request(ctx, method, path, status, requestOptions{
		contentType: "application/json",
		body:        bytes.NewReader(body),
	}))
}

func readJSON[T any](res *http.Response, err error) (T, error) {
	var result T
	if err != nil {
		return result, err
	}
	defer res.Body.Close()
	err = json.NewDecoder(res.Body).Decode(&result)
	return result, err
}

func readText(res *http.Response, err error) (string, error) {
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	text, err := io.ReadAll(res.Body)
	return string(text), err
}

func closeResponse(res *http.Response, err error) error {
	if err != nil {
		return err
	}
	return res.Body.Close()
}
