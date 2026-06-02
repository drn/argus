package main

import (
	"crypto/rand"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"

	"github.com/coder/websocket"
)

// fakePlugin is a localhost HTTP+WS server the harness uses to play the
// "plugin side" of every callback argus makes. Each phase wires its own
// handler against PathHandlers; inbound requests are gated by an auth
// header so a stray request from anywhere other than our daemon is rejected.
//
// The mint-time secret prevents a substrate bug from letting one plugin
// hijack another plugin's callback URL: argus has to send the exact
// auth header the plugin gave it at registration time.
type fakePlugin struct {
	server     *httptest.Server
	authHeader string

	mu        sync.Mutex
	recorded  []recordedRequest
	wsHandler http.Handler
}

// recordedRequest captures one inbound HTTP request to the fake plugin so
// later phases can assert on what argus sent.
type recordedRequest struct {
	Method string
	Path   string
	Header http.Header
	Body   []byte
}

func newFakePlugin() *fakePlugin {
	p := &fakePlugin{
		authHeader: "Bearer " + randHex(16),
	}
	p.server = httptest.NewServer(http.HandlerFunc(p.dispatch))
	return p
}

// URL returns the http://host:port base URL of the fake plugin.
func (p *fakePlugin) URL() string {
	return p.server.URL
}

// WSURL returns the ws://host:port base URL for stream-section endpoints.
func (p *fakePlugin) WSURL() string {
	return "ws" + p.server.URL[len("http"):]
}

// AuthHeader is the value argus must send on every callback. The harness
// registers this with argus at the same time it registers the plugin
// surface (MCP tool, settings section, plugin view).
func (p *fakePlugin) AuthHeader() string {
	return p.authHeader
}

// Stop shuts down the server.
func (p *fakePlugin) Stop() {
	p.server.Close()
}

// Recorded returns a copy of every inbound request observed so far.
func (p *fakePlugin) Recorded() []recordedRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]recordedRequest, len(p.recorded))
	copy(out, p.recorded)
	return out
}

// SetWSHandler installs a handler for WebSocket upgrade paths. Phase 8
// (stream sections) wires its own; until then the default is to reject.
func (p *fakePlugin) SetWSHandler(h http.Handler) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.wsHandler = h
}

func (p *fakePlugin) dispatch(w http.ResponseWriter, r *http.Request) {
	// WebSocket upgrade requests skip the auth header — argus connects
	// without it, and the WS handler (if any) is expected to enforce its
	// own protocol-level guards. The current substrate does not pass the
	// auth_header on WS upgrade, so requiring it here would block every
	// stream-section test.
	if isWSUpgrade(r) {
		p.mu.Lock()
		h := p.wsHandler
		p.mu.Unlock()
		if h == nil {
			http.Error(w, "no ws handler registered", http.StatusBadRequest)
			return
		}
		h.ServeHTTP(w, r)
		return
	}

	if r.Header.Get("Authorization") != p.authHeader {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	body, _ := io.ReadAll(r.Body)
	rec := recordedRequest{
		Method: r.Method,
		Path:   r.URL.Path,
		Header: r.Header.Clone(),
		Body:   body,
	}
	p.mu.Lock()
	p.recorded = append(p.recorded, rec)
	p.mu.Unlock()

	// Default response shape: empty 200 OK. Each phase swaps its own
	// content in via a path-keyed map (added when needed).
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"ok":true}`))
}

func isWSUpgrade(r *http.Request) bool {
	return r.Header.Get("Upgrade") == "websocket"
}

// acceptWS upgrades an HTTP request into a coder/websocket Conn. The
// InsecureSkipVerify flag disables Origin checks, which we don't want
// for a loopback test harness — argus's connection has no Origin header.
func acceptWS(w http.ResponseWriter, r *http.Request) (*websocket.Conn, error) {
	return websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
}

func randHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic("randHex: " + err.Error())
	}
	return hex.EncodeToString(b)
}
