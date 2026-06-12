package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/tui/modal"
	"github.com/drn/argus/internal/tui/terminalpane"
	"github.com/drn/argus/internal/tui/views"
	"github.com/drn/argus/internal/tui/widget"
	"github.com/drn/argus/internal/uxlog"
)

// pluginHelpPage is the tview.Pages name for the plugin-triggered help overlay.
const pluginHelpPage = "pluginhelp"

// Caps on the plugin-pushed hotkey dictionary. A misbehaving plugin could push
// millions of items or megabyte-long strings; clamping at store time bounds the
// memory the mount holds and the CPU both the bottom bar and the `?` overlay
// spend rendering it. Items beyond the count cap are dropped; over-long Key /
// Label strings are truncated (not dropped) so the item still renders.
const (
	maxPluginHotkeys  = 64
	maxHotkeyKeyLen   = 16
	maxHotkeyLabelLen = 64
)

// clampHotkeys bounds a plugin-pushed hotkey dictionary: caps the item count and
// truncates each item's Key and Label to a sane rune length. Truncation is on
// runes (not bytes) so multi-byte glyphs are never split. Returns a fresh slice;
// the caller may store it without aliasing the plugin-controlled input.
func clampHotkeys(items []HotkeyItem) []HotkeyItem {
	if len(items) > maxPluginHotkeys {
		items = items[:maxPluginHotkeys]
	}
	out := make([]HotkeyItem, len(items))
	for i, it := range items {
		out[i] = HotkeyItem{
			Key:   truncateRunes(it.Key, maxHotkeyKeyLen),
			Label: truncateRunes(it.Label, maxHotkeyLabelLen),
			Bar:   it.Bar,
		}
	}
	return out
}

// truncateRunes returns s clamped to at most max runes, counting by rune so a
// multi-byte glyph is never cut mid-encoding.
func truncateRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max])
}

// HotkeyItem is one entry in a plugin's pushed hotkey dictionary. Stage 5
// (bottom bar) and Stage 6 (help overlay) consume the stored slice; this
// stage only decodes and stores it.
type HotkeyItem struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Bar   bool   `json:"bar"`
}

// pluginControlEnvelope is the typed decode for plugin → argus control
// frames. Only Type is needed to dispatch release/help; Items carries the
// hotkey dictionary for the hotkeys envelope. Unknown types and malformed
// JSON decode to a zero/garbage value that dispatchPluginControl ignores.
type pluginControlEnvelope struct {
	Type  string       `json:"type"`
	Items []HotkeyItem `json:"items,omitempty"`
}

// pluginConnector is the minimum surface the App uses from a real
// views.Connector. Abstracted so smoke tests can replace the real WebSocket
// dial with an in-process no-op.
type pluginConnector interface {
	Dial(ctx context.Context) error
	SendResize(cols, rows int) error
	SendFocus() error
	SendBlur() error
	Close() error
	// SetOnClose registers the unexpected-disconnect callback (fired when a
	// pump exits for a reason other than an explicit Close). The App uses it to
	// drive reconnect. Called once right after construction.
	SetOnClose(func(error))
}

// pluginConnectorFactory builds a Connector wired to the given URL and byte
// sinks. The default factory returns a real views.Connector. Tests assign a
// factory that returns a stub so they can observe the lifecycle without
// touching the network.
type pluginConnectorFactory func(url string, onBytes func([]byte), onControl func([]byte), in <-chan []byte) pluginConnector

func defaultPluginConnectorFactory(url string, onBytes func([]byte), onControl func([]byte), in <-chan []byte) pluginConnector {
	return views.NewConnector(url, onBytes, onControl, in)
}

// pluginViewMount is one registered plugin view mounted as a tview.Page. Each
// mount carries its own bytes pipes; the actual WebSocket connector is
// created lazily on hotkey activation and torn down on Esc.
type pluginViewMount struct {
	view     *views.View
	pane     *terminalpane.TerminalPane
	pageName string
	hotkey   tcell.Key

	bytesIn chan []byte // ANSI from plugin → terminalpane source
	keysOut chan []byte // keystrokes from pane → plugin

	conn pluginConnector // nil when the view is not active

	// Resize-envelope reconciliation state. All four fields are owned by the
	// tview goroutine (set in activate/deactivate, the dial-complete
	// QueueUpdateDraw closure, and the afterDraw reconciler) — no locking.
	//
	// connReady flips true once the WebSocket dial completed; envelopes are
	// never sent before it. laidOut flips true the first time the pane's page
	// is the front page in afterDraw — i.e. the pane has a real post-layout
	// rect; before that, GetRect() returns the tview Box default (15x10) and
	// must not feed an envelope. lastSentCols/Rows record the last resize
	// envelope delivered on the current connection so the reconciler re-sends
	// exactly when the computed viewport drifts from what the plugin believes.
	connReady    bool
	laidOut      bool
	focusSent    bool
	lastSentCols int
	lastSentRows int
	// sendFailLogged gates failure logging to the first failure of a streak —
	// the reconciler retries every draw, and a wedged connection would
	// otherwise flood ux.log with one line per draw. Cleared on success.
	sendFailLogged bool

	// Reconnect state. All owned by the tview goroutine (set in the disconnect
	// handler, the redial-success closure, and deactivate) — no locking, same
	// as the reconciliation fields above.
	//
	// reconnecting is true while the WebSocket has dropped unexpectedly and the
	// redial loop is running with the overlay up. reconnectAttempt counts dial
	// failures on the current outage (resets to 0 on a fresh connection).
	// reconnectStart marks when the outage began (via a.nowFn) so the overlay
	// can flip to a "still trying…" message after the grace period.
	// reconnectCancel stops the redial goroutine; called in deactivate and on a
	// successful resume. reconnectModal is the overlay widget, kept so the loop
	// can update its message live.
	reconnecting     bool
	reconnectAttempt int
	reconnectStart   time.Time
	reconnectCancel  context.CancelFunc
	reconnectModal   *modal.ReconnectModal

	// hotkeys is the latest dictionary the plugin pushed via a hotkeys
	// control frame. Stage 5 renders the bar:true subset in the bottom bar;
	// Stage 6 renders the full set in the help overlay. Set on dispatch, only
	// read on the tview goroutine. Cleared implicitly when the mount is torn
	// down (a fresh activation starts with the prior dictionary, so Stage 5
	// will reset it on deactivate to avoid bleed between plugins).
	hotkeys []HotkeyItem
}

// loadPluginViews reads the registry and mounts every registered view as a
// tview.Page. Idempotent — calling twice is safe; later calls rebuild the
// list and replace any prior mounts. Called from buildUI; the app's tick
// loop does not refresh dynamically because plugin registrations are rare
// and a new view requires a TUI restart to surface today.
func (a *App) loadPluginViews() {
	if a.pluginConnFactory == nil {
		a.pluginConnFactory = defaultPluginConnectorFactory
	}
	// Remote-TUI mode is a.db = *apistore.Store, which the views.Registry
	// cannot use directly. The remote-TUI flow will mount plugin views over
	// the REST API in a follow-up; for now, no-op cleanly.
	local, ok := a.db.(*db.DB)
	if !ok {
		return
	}

	reg := views.New(local)
	all := reg.List()

	// Tear down any previous mounts before rebuilding (defensive — buildUI
	// only calls this once but the test harness rebuilds the App in place).
	for _, m := range a.pluginMounts {
		a.pages.RemovePage(m.pageName)
		close(m.bytesIn)
		close(m.keysOut)
	}
	a.pluginMounts = a.pluginMounts[:0]
	a.pluginHotkeys = make(map[tcell.Key]*pluginViewMount)

	for _, v := range all {
		key, ok := views.ParseHotkey(v.Hotkey)
		if !ok {
			uxlog.Log("[plugin-view] skipped %q: invalid hotkey %q", v.Title, v.Hotkey)
			continue
		}
		bytesIn := make(chan []byte, 64)
		keysOut := make(chan []byte, 64)
		pane := terminalpane.New(bytesIn)
		pane.SetTitle(v.Title)
		pane.SetInputBack(keysOut)
		pane.OnNeedRedraw = func() {
			if a.tapp != nil {
				a.tapp.QueueUpdateDraw(func() {})
			}
		}
		pageName := fmt.Sprintf("plugin-view:%d", v.ID)
		a.pages.AddPage(pageName, pane, true, false)

		m := &pluginViewMount{
			view:     v,
			pane:     pane,
			pageName: pageName,
			hotkey:   tcell.Key(key),
			bytesIn:  bytesIn,
			keysOut:  keysOut,
		}
		a.pluginMounts = append(a.pluginMounts, m)
		a.pluginHotkeys[tcell.Key(key)] = m
	}
}

// activatePluginView opens a plugin view: switches Pages, dials the WS,
// emits resize+focus envelopes. Idempotent — re-activating an already-active
// view re-sends the resize envelope so a stale plugin can recover.
func (a *App) activatePluginView(m *pluginViewMount) {
	if a.activePlugin != nil && a.activePlugin == m {
		// Recovery hint: zero the last-sent size so the reconciler re-sends
		// the current viewport even though it hasn't changed.
		m.lastSentCols, m.lastSentRows = 0, 0
		a.reconcilePluginViewSize()
		return
	}
	if a.activePlugin != nil {
		a.deactivatePluginView()
	}

	a.activePlugin = m
	a.mode = modePluginView
	// Reset the failsafe timestamp so a stale Ctrl+Q from a prior view can't
	// trip the double-tap failsafe on the first press in this one.
	a.lastCtrlQ = time.Time{}
	uxlog.Log("[plugin-view] surrender: %q has the keyboard (full surrender, ^Q^Q failsafe)", m.view.Title)
	a.pages.SwitchToPage(m.pageName)
	a.tapp.SetFocus(m.pane)

	// Context-sensitive bottom bar: surface the plugin's bar:true hotkeys (if
	// any were pushed before activation) plus the reserved exit hint.
	a.statusbar.SetPluginMode(true, m.view.Title, barHints(m.hotkeys))

	conn := a.newPluginConn(m)
	m.conn = conn
	// Fresh connection: reset the reconciliation state so the first envelope
	// on this conn is sent from a real post-layout rect, followed by focus.
	m.connReady = false
	m.laidOut = false
	m.focusSent = false
	m.lastSentCols, m.lastSentRows = 0, 0
	m.sendFailLogged = false

	go func(c pluginConnector) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := c.Dial(ctx); err != nil {
			uxlog.Log("[plugin-view] dial %q failed: %v", m.view.Title, err)
			return
		}
		// Never compute the viewport on this goroutine: the pane rect is owned
		// by the tview goroutine and may predate the first layout pass (the
		// tview Box default is 15x10 → a garbage 13x8 envelope). Mark the
		// connection ready and kick a draw; the afterDraw reconciler sends the
		// first resize envelope from the real post-layout rect, then focus.
		if a.tapp != nil {
			a.tapp.QueueUpdateDraw(func() {
				if a.activePlugin != m || m.conn != c {
					return // released or replaced while dialing
				}
				m.connReady = true
			})
		}
	}(conn)
}

// newPluginConn builds a connector for the mount wired to its persistent byte
// sinks and the unexpected-disconnect handler. Shared by activatePluginView
// (initial dial) and the reconnect loop (re-dial), so both connections behave
// identically — same byte plumbing, same onClose → reconnect trigger.
func (a *App) newPluginConn(m *pluginViewMount) pluginConnector {
	conn := a.pluginConnFactory(m.view.CallbackURL, func(b []byte) {
		// Forward plugin → streampane source. Non-blocking — drop on
		// backpressure to match the rest of argus's PTY plumbing.
		select {
		case m.bytesIn <- b:
		default:
		}
	}, func(b []byte) {
		// onControl runs on the connector's read-pump goroutine — it must NOT
		// touch tview or App state directly. dispatchPluginControl decodes
		// defensively and routes every tview interaction through
		// QueueUpdateDraw.
		a.dispatchPluginControl(m, b)
	}, m.keysOut)
	conn.SetOnClose(func(err error) {
		// Runs on a pump goroutine — onPluginDisconnect hops to the tview
		// goroutine. `conn` is captured so a stale drop from a replaced
		// connection is ignored (m.conn != conn under the queued closure).
		a.onPluginDisconnect(m, conn, err)
	})
	return conn
}

// Reconnect tunables. The dial timeout bounds a single attempt; the backoff
// schedule caps at 2s (a daemon bounce is seconds); the grace period is how
// long the overlay stays on the optimistic "Reconnecting…" message before it
// flips to the "still trying…" exit hint.
const (
	pluginReconnectPage        = "pluginreconnect"
	pluginReconnectDialTimeout = 3 * time.Second
	pluginReconnectGrace       = 2 * time.Minute
)

// reconnectBackoff returns the sleep before the next dial attempt: 250ms, 500ms,
// 1s, then a steady 2s. Pure function of the (zero-based) failed-attempt count
// so tests are deterministic without a clock.
func reconnectBackoff(attempt int) time.Duration {
	switch {
	case attempt <= 0:
		return 250 * time.Millisecond
	case attempt == 1:
		return 500 * time.Millisecond
	case attempt == 2:
		return time.Second
	default:
		return 2 * time.Second
	}
}

// reconnectMessage is the overlay body for the current outage state. Before the
// grace period it is optimistic; after, it surfaces that the wait is unusual.
func reconnectMessage(elapsed time.Duration, attempt int) string {
	if elapsed >= pluginReconnectGrace {
		return fmt.Sprintf("Still trying to reconnect… (attempt %d)", attempt+1)
	}
	return fmt.Sprintf("Reconnecting… (attempt %d)", attempt+1)
}

// onPluginDisconnect is the connector's onClose sink. It runs on a pump
// goroutine, so it hops to the tview goroutine, re-checks that the dropped
// connection is still the active one, and starts a reconnect if so. Idempotent
// against an already-reconnecting mount.
func (a *App) onPluginDisconnect(m *pluginViewMount, dropped pluginConnector, err error) {
	uxlog.Log("[plugin-view] disconnect on %q: %v", m.view.Title, err)
	if a.tapp == nil {
		return
	}
	a.tapp.QueueUpdateDraw(func() {
		if a.activePlugin != m || m.conn != dropped {
			uxlog.Log("[plugin-view] disconnect ignored: mount inactive or stale conn")
			return
		}
		if m.reconnecting {
			return // a reconnect is already in flight
		}
		a.startPluginReconnect(m)
	})
}

// startPluginReconnect shows the reconnect overlay and launches the redial
// loop. Runs on the tview goroutine.
func (a *App) startPluginReconnect(m *pluginViewMount) {
	m.reconnecting = true
	m.reconnectAttempt = 0
	m.reconnectStart = a.nowFn()
	// Pause the resize reconciler while the connection is down — its target
	// (m.conn) is the dropped connector. finishPluginReconnect flips connReady
	// back on for the fresh connection.
	m.connReady = false
	m.reconnectModal = modal.NewReconnectModal(m.view.Title, reconnectMessage(0, 0))
	a.pages.AddPage(pluginReconnectPage, m.reconnectModal, true, true)
	a.pages.SwitchToPage(pluginReconnectPage)
	a.tapp.SetFocus(m.reconnectModal)
	a.statusbar.SetPluginMode(true, m.view.Title, nil)
	uxlog.Log("[plugin-view] reconnecting %q (overlay shown)", m.view.Title)

	// G118 false positive: cancel intentionally outlives this function — it is
	// stored on m.reconnectCancel and invoked by stopPluginReconnect (teardown)
	// and finishPluginReconnect (successful resume). gosec can't see the
	// cross-function call through the struct field.
	ctx, cancel := context.WithCancel(context.Background()) //nolint:gosec // G118: cancel stored on m.reconnectCancel, called in stopPluginReconnect/finishPluginReconnect
	m.reconnectCancel = cancel
	go a.pluginReconnectLoop(ctx, m)
}

// pluginReconnectLoop re-dials the view's callback URL with capped backoff until
// it succeeds or the context is cancelled (deactivate / Esc / failsafe). Runs on
// its own goroutine; every tview/App touch goes through QueueUpdateDraw.
func (a *App) pluginReconnectLoop(ctx context.Context, m *pluginViewMount) {
	attempt := 0
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		conn := a.newPluginConn(m)
		dialCtx, cancel := context.WithTimeout(ctx, pluginReconnectDialTimeout)
		err := conn.Dial(dialCtx)
		cancel()
		if err == nil {
			a.finishPluginReconnect(m, conn)
			return
		}
		// Dial failed — discard this connector and back off.
		_ = conn.Close()
		uxlog.Log("[plugin-view] reconnect dial %q attempt %d failed: %v", m.view.Title, attempt+1, err)
		if a.tapp != nil {
			a.tapp.QueueUpdateDraw(func() {
				if a.activePlugin != m || !m.reconnecting {
					return
				}
				m.reconnectAttempt = attempt + 1
				if m.reconnectModal != nil {
					m.reconnectModal.SetMessage(reconnectMessage(a.nowFn().Sub(m.reconnectStart), m.reconnectAttempt))
				}
			})
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(reconnectBackoff(attempt)):
		}
		attempt++
	}
}

// finishPluginReconnect adopts a freshly dialed connection: it resets the
// resize/focus handshake state so the reconciler re-sends resize→focus exactly
// like a new connection, dismisses the overlay, and restores the plugin pane.
// Runs the state mutation on the tview goroutine. If the view was torn down
// while dialing, the new connection is closed and discarded.
func (a *App) finishPluginReconnect(m *pluginViewMount, conn pluginConnector) {
	if a.tapp == nil {
		_ = conn.Close()
		return
	}
	a.tapp.QueueUpdateDraw(func() {
		if a.activePlugin != m || !m.reconnecting {
			// Raced with deactivate — discard the orphan connection.
			_ = conn.Close()
			return
		}
		m.conn = conn
		m.reconnecting = false
		m.reconnectAttempt = 0
		m.reconnectCancel = nil
		// Reset the handshake state so the first envelope on the new connection
		// is sent even though the viewport size hasn't changed. laidOut stays
		// true: the pane never un-laid-out, so the post-layout rect is
		// immediately authoritative (the resume envelope is the real size, not
		// the 13x8 pre-layout default).
		m.connReady = true
		m.focusSent = false
		m.lastSentCols, m.lastSentRows = 0, 0
		m.sendFailLogged = false

		a.pages.RemovePage(pluginReconnectPage)
		m.reconnectModal = nil
		a.pages.SwitchToPage(m.pageName)
		a.tapp.SetFocus(m.pane)
		a.statusbar.SetPluginMode(true, m.view.Title, barHints(m.hotkeys))
		uxlog.Log("[plugin-view] reconnected %q — resuming (resize+focus handshake)", m.view.Title)
		// Drive the resume handshake now (also re-runs every afterDraw).
		a.reconcilePluginViewSize()
	})
}

// stopPluginReconnect cancels an in-flight reconnect: stops the redial loop,
// removes the overlay, and clears all reconnect state. Safe to call when not
// reconnecting. Runs on the tview goroutine; called from deactivatePluginView.
func (a *App) stopPluginReconnect(m *pluginViewMount) {
	if m.reconnectCancel != nil {
		m.reconnectCancel()
		m.reconnectCancel = nil
	}
	if m.reconnectModal != nil {
		a.pages.RemovePage(pluginReconnectPage)
		m.reconnectModal = nil
	}
	m.reconnecting = false
	m.reconnectAttempt = 0
	m.reconnectStart = time.Time{}
}

// deactivatePluginView closes the active plugin view: sends blur, closes the
// WS, switches back to the tasks page.
func (a *App) deactivatePluginView() {
	if a.activePlugin == nil {
		return
	}
	m := a.activePlugin
	a.activePlugin = nil
	uxlog.Log("[plugin-view] release: %q gave back the keyboard", m.view.Title)

	// Cancel any in-flight reconnect FIRST: stops the redial loop and removes
	// the overlay. Done before clearing a.activePlugin's conn so a racing
	// finishPluginReconnect closure sees reconnecting=false and discards its
	// orphan connection.
	a.stopPluginReconnect(m)

	if m.conn != nil {
		_ = m.conn.SendBlur()
		_ = m.conn.Close()
		m.conn = nil
	}
	// Reset reconciliation state — mounts are reused across activations, and
	// stale last-sent values must not suppress the next connection's envelope.
	m.connReady = false
	m.laidOut = false
	m.focusSent = false
	m.lastSentCols, m.lastSentRows = 0, 0
	m.sendFailLogged = false

	// Tear down a lingering help overlay before clearing state — otherwise the
	// page would survive into the next plugin. RemovePage directly (not
	// dismissPluginHelp, which would try to refocus the now-cleared activePlugin).
	if a.pluginHelpVisible {
		a.pages.RemovePage(pluginHelpPage)
	}
	a.pluginHelpVisible = false

	// Clear all plugin-view bar state so nothing bleeds into the next plugin:
	// drop the bottom-bar plugin mode (argus's own tab hints return — the bar
	// already tracks the active tab via SetTab), forget the pushed dictionary,
	// and reset the help-requested seam.
	a.statusbar.SetPluginMode(false, "", nil)
	m.hotkeys = nil
	a.pluginHelpRequested = false

	a.mode = modeTaskList
	a.pages.SwitchToPage("tasks")
	a.tapp.SetFocus(a.tasklist)
}

// barHints converts a plugin's pushed hotkey dictionary into the widget-local
// PluginHint slice the bottom bar renders, filtering to the bar:true subset.
// Defined on the app side so the widget package never imports tui (cycle).
func barHints(items []HotkeyItem) []widget.PluginHint {
	var out []widget.PluginHint
	for _, it := range items {
		if it.Bar {
			out = append(out, widget.PluginHint{Key: it.Key, Label: it.Label})
		}
	}
	return out
}

// dispatchPluginControl decodes a raw plugin → argus control frame and
// routes it to the matching handler. Runs on the connector's read-pump
// goroutine, so every handler that touches tview or App state defers to
// a.tapp.QueueUpdateDraw. Malformed JSON and unknown types are logged and
// ignored — never panics, never disturbs the binary ANSI stream.
//
// mount is the mount the control frame arrived on. Handlers re-check that it
// is still the active plugin under QueueUpdateDraw, because a release/failsafe
// could have fired between the read and the queued closure running.
func (a *App) dispatchPluginControl(mount *pluginViewMount, raw []byte) {
	uxlog.Log("[plugin-view] control frame received (%d bytes)", len(raw))
	var env pluginControlEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		uxlog.Log("[plugin-view] ignored malformed control frame: %v", err)
		return
	}
	switch env.Type {
	case "release":
		uxlog.Log("[plugin-view] dispatch: release")
		if a.tapp != nil {
			a.tapp.QueueUpdateDraw(func() {
				// Guard against a stale mount: only deactivate if this mount is
				// still the active plugin. A late release from plugin A must not
				// deactivate a freshly-activated plugin B (release/activation
				// race between the read and the queued closure running).
				if a.activePlugin == nil || a.activePlugin != mount {
					uxlog.Log("[plugin-view] release ignored: mount no longer active")
					return
				}
				a.deactivatePluginView()
			})
		}
	case "hotkeys":
		uxlog.Log("[plugin-view] dispatch: hotkeys (%d items)", len(env.Items))
		// Clamp BEFORE storing so both the bottom bar and the `?` overlay read a
		// bounded dictionary — a plugin cannot grow argus's memory/CPU without
		// bound. Truncates over-long Key/Label and drops items past the count cap.
		items := clampHotkeys(env.Items)
		if a.tapp != nil {
			a.tapp.QueueUpdateDraw(func() {
				// Guard against a stale/nil mount: only store if this mount is
				// still the active plugin (a release could have raced in).
				if a.activePlugin == nil || a.activePlugin != mount {
					uxlog.Log("[plugin-view] hotkeys dropped: mount no longer active")
					return
				}
				mount.hotkeys = items
				// Refresh the bottom bar live with the bar:true subset.
				a.statusbar.SetPluginMode(true, mount.view.Title, barHints(items))
			})
		}
	case "help":
		uxlog.Log("[plugin-view] dispatch: help")
		if a.tapp != nil {
			a.tapp.QueueUpdateDraw(func() { a.requestPluginHelp(mount) })
		}
	default:
		uxlog.Log("[plugin-view] ignored unknown control type %q", env.Type)
	}
}

// requestPluginHelp pops the plugin-triggered help overlay, rendering the
// active mount's full hotkey dictionary (every item, the bar flag ignored) in
// argus's help modal styled like argus help. The overlay shows ONLY the
// plugin's hotkeys — never argus's own bindings — because argus has fully
// surrendered the keyboard. The plugin owns `?` and is the authority on when
// help is shown; argus reserves nothing.
//
// Runs on the tview goroutine (called from a QueueUpdateDraw closure in
// dispatchPluginControl). The next key dismisses the overlay (see the
// modePluginView branch of handleGlobalKey) — it does not capture the keyboard
// beyond that single dismissal.
func (a *App) requestPluginHelp(mount *pluginViewMount) {
	if a.activePlugin == nil || a.activePlugin != mount {
		uxlog.Log("[plugin-view] help dropped: mount no longer active")
		return
	}
	a.pluginHelpRequested = true

	overlay := modal.NewHelpModalWith(mount.view.Title, pluginHelpSections(mount.hotkeys))
	// Re-add replaces any prior instance (a second help frame just refreshes).
	a.pages.AddPage(pluginHelpPage, overlay, true, true)
	a.pages.SwitchToPage(pluginHelpPage)
	a.pluginHelpVisible = true
	uxlog.Log("[plugin-view] help overlay shown for %q (%d hotkeys)", mount.view.Title, len(mount.hotkeys))
}

// dismissPluginHelp hides the plugin help overlay and returns focus to the
// active plugin pane, restoring its bottom bar. No-op if the overlay is not
// visible. Runs on the tview goroutine.
func (a *App) dismissPluginHelp() {
	if !a.pluginHelpVisible {
		return
	}
	a.pluginHelpVisible = false
	a.pluginHelpRequested = false
	a.pages.RemovePage(pluginHelpPage)
	uxlog.Log("[plugin-view] help overlay dismissed")
	if m := a.activePlugin; m != nil {
		a.pages.SwitchToPage(m.pageName)
		a.tapp.SetFocus(m.pane)
		a.statusbar.SetPluginMode(true, m.view.Title, barHints(m.hotkeys))
	}
}

// pluginHelpSections converts a plugin's full hotkey dictionary into the single
// HelpSection the overlay renders. Every item is included regardless of its bar
// flag — the help overlay is the complete reference, the bottom bar is the
// subset.
func pluginHelpSections(items []HotkeyItem) []modal.HelpSection {
	bindings := make([]modal.HelpBinding, 0, len(items))
	for _, it := range items {
		bindings = append(bindings, modal.HelpBinding{Key: it.Key, Action: it.Label})
	}
	return []modal.HelpSection{{Title: "Hotkeys", Bindings: bindings}}
}

// pluginViewColOverhead / pluginViewRowOverhead are the fixed chrome around a
// plugin pane when deriving its viewport from the raw screen size: the pane
// fills the pages area (screen minus the 1-row header and 1-row status bar)
// and draws its own 1-cell border on each side. Used only until the pane's
// first real layout pass; after that the post-layout rect is authoritative.
const (
	pluginViewColOverhead = 2 // pane border left+right
	pluginViewRowOverhead = 4 // header + statusbar + pane border top+bottom
)

// pluginViewportSize returns the cols/rows the active plugin view should
// render into. The pane rect is trusted only after its first layout pass
// (m.laidOut) — a never-drawn tview Box reports the 15x10 default, which
// would yield a garbage 13x8 viewport. Before layout, derive from the screen
// size minus fixed chrome. Must be called on the tview goroutine.
func (a *App) pluginViewportSize() (int, int) {
	if m := a.activePlugin; m != nil && m.laidOut {
		_, _, w, h := m.pane.GetRect()
		if w > 2 && h > 2 {
			return w - 2, h - 2 // subtract border
		}
	}
	if a.screen != nil {
		w, h := a.screen.Size()
		return max(w-pluginViewColOverhead, 1), max(h-pluginViewRowOverhead, 1)
	}
	return 80, 24
}

// reconcilePluginViewSize sends a resize envelope to the active plugin view
// when the computed viewport differs from the last envelope delivered on the
// current connection. This is the ONLY path that sends plugin resize
// envelopes; it runs on the tview goroutine after every draw (and directly on
// re-activation as a recovery hint), so the initial envelope, terminal
// resizes, and corrections for a lost/raced/garbage envelope all flow through
// the same dedupe. The focus envelope follows the first successful resize so
// the plugin learns its size before it learns it has the keyboard.
func (a *App) reconcilePluginViewSize() {
	m := a.activePlugin
	if m == nil || m.conn == nil || !m.connReady || !m.laidOut {
		return
	}
	cols, rows := a.pluginViewportSize()
	if cols == m.lastSentCols && rows == m.lastSentRows {
		return
	}
	if err := m.conn.SendResize(cols, rows); err != nil {
		// Leave lastSent unchanged so the envelope is retried on the next draw.
		// Log only the first failure of a streak — retrying per draw would
		// otherwise flood ux.log while a connection is wedged.
		if !m.sendFailLogged {
			m.sendFailLogged = true
			uxlog.Log("[plugin-view] send resize %dx%d failed (retrying per draw): %v", cols, rows, err)
		}
		return
	}
	m.sendFailLogged = false
	uxlog.Log("[plugin-view] resize envelope %dx%d (was %dx%d)", cols, rows, m.lastSentCols, m.lastSentRows)
	m.lastSentCols, m.lastSentRows = cols, rows
	if !m.focusSent {
		m.focusSent = true
		if err := m.conn.SendFocus(); err != nil {
			uxlog.Log("[plugin-view] send focus failed: %v", err)
		}
	}
}
