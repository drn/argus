package tui

import (
	"context"
	"time"

	"github.com/drn/argus/internal/uxlog"
)

// openStreamSection dials a WebSocket to callbackURL and pumps bytes into
// bytesIn while reading keystrokes from keysOut. Called by SettingsView when
// a stream section gains focus. Reuses pluginConnFactory so smoke tests can
// inject a stub connector — the same factory powers plugin views.
//
// Per-section state is keyed by (scope, title) so re-entering a section
// after a blur opens a fresh connector (the SettingsView's mount cache
// keeps the streampane alive across the cycle so previously-received bytes
// are still rendered when the new connector takes a moment to dial).
func (a *App) openStreamSection(scope, title, callbackURL string, bytesIn chan<- []byte, keysOut <-chan []byte) {
	if a.pluginConnFactory == nil {
		a.pluginConnFactory = defaultPluginConnectorFactory
	}

	onBytes := func(b []byte) {
		select {
		case bytesIn <- b:
		default:
			// Drop on backpressure to match the rest of argus's PTY plumbing.
		}
	}
	// Settings-stream sections do not participate in the plugin-view
	// ball/release model, so they pass a nil control sink — plugin → argus
	// text frames here are dropped by the connector.
	conn := a.pluginConnFactory(callbackURL, onBytes, nil, keysOut)

	a.streamConnsMu.Lock()
	if a.streamConns == nil {
		a.streamConns = make(map[pluginStreamKey]pluginConnector)
	}
	key := pluginStreamKey{scope: scope, title: title}
	// Close any existing connector for this key defensively. The SettingsView
	// should always blur before re-focusing, but a stale open shouldn't keep
	// running if it leaks through.
	if prev, ok := a.streamConns[key]; ok {
		_ = prev.Close()
	}
	a.streamConns[key] = conn
	a.streamConnsMu.Unlock()

	go func(c pluginConnector) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := c.Dial(ctx); err != nil {
			uxlog.Log("[stream-section] dial %q (scope %q) failed: %v", title, scope, err)
			return
		}
		if err := c.SendFocus(); err != nil {
			uxlog.Log("[stream-section] send focus failed: %v", err)
		}
	}(conn)
}

// closeStreamSection tears down the connector for (scope, title) on blur.
// Safe to call when no connector exists — that's the no-op path after a
// section is unregistered with no prior focus.
func (a *App) closeStreamSection(scope, title string) {
	key := pluginStreamKey{scope: scope, title: title}
	a.streamConnsMu.Lock()
	conn, ok := a.streamConns[key]
	if ok {
		delete(a.streamConns, key)
	}
	a.streamConnsMu.Unlock()
	if !ok {
		return
	}
	_ = conn.SendBlur()
	_ = conn.Close()
}
