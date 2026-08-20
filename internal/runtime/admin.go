package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Sentinel errors returned by AdminClient methods. They are wrapped by
// concrete errors; callers should branch with errors.Is.
var (
	// ErrAdminUnreachable is returned when the Admin API cannot be reached:
	// connection refused, DNS failure, or any transport-level error that is
	// neither a timeout nor a cancellation.
	ErrAdminUnreachable = errors.New("admin API unreachable")
	// ErrAdminTimeout is returned when an Admin API request exceeds the
	// client timeout or the caller's context deadline.
	ErrAdminTimeout = errors.New("admin API timeout")
	// ErrAdminNotFound is returned when the Admin API responds with 404:
	// the endpoint is not exposed by this Caddy build and the caller
	// should fall back to the static view.
	ErrAdminNotFound = errors.New("admin API not found")
	// ErrAdminRejected is returned when the Admin API responds with a
	// non-2xx status: Caddy refused the posted configuration or the
	// request was otherwise invalid.
	ErrAdminRejected = errors.New("admin API rejected the configuration")
)

// AdminClient is a minimal read/write HTTP client for Caddy's local Admin
// API. It is deliberately low-level: orchestration (adapt, capability
// gating) lives in higher layers.
//
// AdminClient is safe for concurrent use: it holds no per-request state.
type AdminClient struct {
	baseURL string
	hc      *http.Client
}

// NewAdminClient returns a client for the given base URL (e.g.
// "http://localhost:2019"; a trailing slash is trimmed) with the given
// per-request timeout. A non-positive timeout defaults to 30 seconds.
func NewAdminClient(baseURL string, timeout time.Duration) *AdminClient {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &AdminClient{
		baseURL: strings.TrimSuffix(baseURL, "/"),
		hc:      &http.Client{Timeout: timeout},
	}
}

// LoadJSON POSTs config (adapted Caddy JSON) to /load and blocks until the
// reload completes or fails. It sets Content-Type: application/json and
// Cache-Control: must-revalidate (the latter forces Caddy to actually
// reload even when the config is identical to the running one).
//
// Error mapping:
//   - non-2xx response -> ErrAdminRejected wrapped, message includes the
//     body's {"error":"..."} field when present
//   - context deadline exceeded (client timeout or ctx deadline) -> ErrAdminTimeout wrapped
//   - context canceled -> context.Canceled returned as-is (NOT a sentinel)
//   - any other transport/network error -> ErrAdminUnreachable wrapped
func (c *AdminClient) LoadJSON(ctx context.Context, config []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/load", bytes.NewReader(config))
	if err != nil {
		return mapAdminErr(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Cache-Control", "must-revalidate")

	resp, err := c.hc.Do(req)
	if err != nil {
		return mapAdminErr(err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return mapAdminErr(err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%w: %s", ErrAdminRejected, adminErrorBody(resp.StatusCode, body))
	}
	return nil
}

// Config GETs /config/ (read-only inspection of the loaded configuration)
// and returns the response body. Errors map exactly like LoadJSON.
func (c *AdminClient) Config(ctx context.Context) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/config/", nil)
	if err != nil {
		return nil, mapAdminErr(err)
	}

	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, mapAdminErr(err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, mapAdminErr(err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%w: %s", ErrAdminRejected, adminErrorBody(resp.StatusCode, body))
	}
	return body, nil
}

// Upstreams GETs /reverse_proxy/upstreams (live upstream health) and
// returns the response body. When the Caddy build does not expose that
// endpoint (404) it returns ErrAdminNotFound so the caller can fall
// back to the static view; other non-2xx responses remain
// ErrAdminRejected. Errors otherwise map exactly like Config.
func (c *AdminClient) Upstreams(ctx context.Context) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/reverse_proxy/upstreams", nil)
	if err != nil {
		return nil, mapAdminErr(err)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, mapAdminErr(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, mapAdminErr(err)
	}
	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("%w: %s", ErrAdminNotFound, adminErrorBody(resp.StatusCode, body))
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%w: %s", ErrAdminRejected, adminErrorBody(resp.StatusCode, body))
	}
	return body, nil
}

// mapAdminErr converts a transport error from the underlying http.Client
// into the AdminClient error contract: cancellation passes through
// untouched, timeouts wrap ErrAdminTimeout, and everything else wraps
// ErrAdminUnreachable.
func mapAdminErr(err error) error {
	if errors.Is(err, context.Canceled) {
		return err
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("%w: %v", ErrAdminTimeout, err)
	}
	return fmt.Errorf("%w: %v", ErrAdminUnreachable, err)
}

// adminErrorBody builds a readable failure message from a non-2xx response.
// It prefers the JSON {"error":"..."} field Caddy emits for rejected
// configurations, tolerates malformed or empty bodies by falling back to
// the raw body text, and finally to the HTTP status text.
func adminErrorBody(status int, body []byte) string {
	var payload struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(body, &payload); err == nil && payload.Error != "" {
		return payload.Error
	}
	if msg := strings.TrimSpace(string(body)); msg != "" {
		return msg
	}
	return http.StatusText(status)
}
