// Package apiclient is a typed Go HTTP client for the Argus REST API. It
// wraps the surface exposed by internal/api/routes.go so the TUI, scripts,
// and tests can talk to a local or remote argus daemon over HTTP with a
// single transport (no Unix socket, no direct SQLite). Bearer-token auth.
//
// All non-streaming methods accept context.Context so the TUI's main goroutine
// can cancel slow requests on shutdown or view change.
package apiclient

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

// DefaultLocalBaseURL is the local-mode base URL the TUI uses when no
// --remote flag is set. Matches the daemon's default ListenAndServe port.
const DefaultLocalBaseURL = "http://127.0.0.1:7743"

// Client is a typed HTTP client for the argus REST API. Methods are grouped
// by surface area across multiple files in this package; this file holds the
// constructor, the shared Do/DoJSON plumbing, and the error types.
//
// Client is safe for concurrent use — the underlying http.Client is, and
// nothing in this package mutates state after construction.
type Client struct {
	baseURL string
	token   string
	hc      *http.Client
}

// Option configures a Client at construction. Pass via New(baseURL, token, opts...).
type Option func(*Client)

// WithHTTPClient overrides the underlying *http.Client. Useful for tests
// (httptest.NewServer.Client()) and for callers who want a custom Transport
// — e.g. a TLS-skip transport for self-signed dev endpoints. There is no
// dedicated InsecureTLS option because Tailscale MagicDNS issues valid
// certs; if you need to bypass verification, plug a custom Transport here.
func WithHTTPClient(hc *http.Client) Option { return func(c *Client) { c.hc = hc } }

// WithTimeout sets a per-request timeout on the underlying http.Client. SSE
// stream calls bypass this (they install their own Context-based deadline).
func WithTimeout(d time.Duration) Option {
	return func(c *Client) {
		if c.hc == nil {
			c.hc = &http.Client{}
		}
		c.hc.Timeout = d
	}
}

// New constructs a Client. baseURL is the API root (no trailing slash needed
// — it is normalised). token is the bearer token; pass "" for the unused
// dashboard endpoints, but every /api/* endpoint requires one.
//
// Default http.Client has a 30 s timeout and keep-alive enabled. Override
// with WithHTTPClient or WithTimeout for custom behaviour.
func New(baseURL, token string, opts ...Option) *Client {
	c := &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		hc:      &http.Client{Timeout: 30 * time.Second},
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// BaseURL returns the normalised base URL the client was constructed with.
// Useful for diagnostic logs and Settings tab display.
func (c *Client) BaseURL() string { return c.baseURL }

// HTTPClient exposes the underlying http.Client so SSE callers can run their
// own long-lived GETs without going through DoJSON.
func (c *Client) HTTPClient() *http.Client { return c.hc }

// Token returns the bearer token used for Authorization headers. Exposed so
// SSE callers can build their own request with the same auth applied.
func (c *Client) Token() string { return c.token }

// Error is the typed error returned for non-2xx responses. Callers can use
// errors.As to distinguish "task not found" (404) from "auth failed" (401)
// without string-matching the message.
type Error struct {
	Status  int    // HTTP status code
	Method  string // request method
	Path    string // request path
	Message string // server-supplied error message, if any
}

func (e *Error) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("apiclient: %s %s: %d %s: %s", e.Method, e.Path, e.Status, http.StatusText(e.Status), e.Message)
	}
	return fmt.Sprintf("apiclient: %s %s: %d %s", e.Method, e.Path, e.Status, http.StatusText(e.Status))
}

// IsNotFound reports whether err is a 404 from the API. Used by the TUI store
// adapter to translate apiclient errors back into local sentinel errors like
// db.ErrTaskNotFound.
func IsNotFound(err error) bool {
	var e *Error
	return errors.As(err, &e) && e.Status == http.StatusNotFound
}

// IsUnauthorized reports whether err is a 401 from the API. Surfaced in the
// TUI startup path so a bad --token can fail loudly.
func IsUnauthorized(err error) bool {
	var e *Error
	return errors.As(err, &e) && e.Status == http.StatusUnauthorized
}

// errorEnvelope is the {"error":"..."} shape every handler uses on failure.
type errorEnvelope struct {
	Error string `json:"error"`
}

// do issues an HTTP request with auth applied, returning the body Reader on
// 2xx. Caller is responsible for closing the body. Non-2xx responses are
// translated to *Error and the body is consumed.
//
// The url path is joined to baseURL. Query encoding is the caller's job —
// pass a fully formed path string like "/api/tasks?status=in_progress".
func (c *Client) do(ctx context.Context, method, path string, body io.Reader, contentType string) (*http.Response, error) {
	fullURL := c.baseURL + path
	req, err := http.NewRequestWithContext(ctx, method, fullURL, body)
	if err != nil {
		return nil, fmt.Errorf("apiclient: build %s %s: %w", method, path, err)
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("apiclient: do %s %s: %w", method, path, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := readErrorMessage(resp.Body)
		resp.Body.Close()
		return nil, &Error{Status: resp.StatusCode, Method: method, Path: path, Message: msg}
	}
	return resp, nil
}

// doJSON sends an optional JSON body and decodes the response JSON into out.
// Pass nil for out to discard the response body. Pass nil for in for GETs
// and bodyless POSTs (Stop/Resume/Archive/etc).
func (c *Client) doJSON(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	contentType := ""
	if in != nil {
		buf, err := json.Marshal(in)
		if err != nil {
			return fmt.Errorf("apiclient: marshal %s %s body: %w", method, path, err)
		}
		body = bytes.NewReader(buf)
		contentType = "application/json"
	}
	resp, err := c.do(ctx, method, path, body, contentType)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if out == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("apiclient: decode %s %s response: %w", method, path, err)
	}
	return nil
}

// readErrorMessage attempts to extract the {"error":"..."} message from a
// failed response body. Returns "" on parse failure so the *Error caller can
// fall through to a generic "<status> <statusText>" message.
func readErrorMessage(r io.Reader) string {
	var env errorEnvelope
	dec := json.NewDecoder(io.LimitReader(r, 64*1024))
	if err := dec.Decode(&env); err == nil {
		return env.Error
	}
	return ""
}

// query is a small helper for building "?k=v&k=v" tails. Returns "" when
// every value is empty so callers don't paste a trailing "?".
func query(kv ...string) string {
	if len(kv)%2 != 0 {
		panic("apiclient.query: odd number of arguments")
	}
	q := url.Values{}
	// Pair iteration: index by pair so gosec G602 doesn't flag kv[i+1]
	// against the bare loop bound — len(kv) is even by the check above.
	for i := 0; i+1 < len(kv); i += 2 {
		k, v := kv[i], kv[i+1]
		if v == "" {
			continue
		}
		q.Set(k, v)
	}
	if len(q) == 0 {
		return ""
	}
	return "?" + q.Encode()
}
