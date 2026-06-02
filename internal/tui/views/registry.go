// Package views holds the plugin-registered top-level view registry.
//
// Each registered view is a full-screen UI surface owned by a plugin. The
// plugin streams ANSI bytes over a WebSocket; the TUI pipes those bytes into
// a streampane and forwards keystrokes back. The registry persists
// registrations to the plugin_views table so they survive restarts.
package views

import (
	"errors"
	"strings"
	"time"

	"github.com/drn/argus/internal/db"
)

// Sentinel errors returned by Registry. Use errors.Is for matching.
var (
	// ErrTitleRequired fires when a title is empty or whitespace-only.
	ErrTitleRequired = errors.New("view title is required")
	// ErrCallbackURLRequired fires when callback_url is empty.
	ErrCallbackURLRequired = errors.New("view callback_url is required")
	// ErrViewExists fires when a (scope, title) pair is already registered.
	ErrViewExists = errors.New("view already registered for this scope/title")
	// ErrViewNotFound fires when Unregister is called on a missing pair.
	ErrViewNotFound = errors.New("view not found")
)

// View is one plugin-registered top-level UI surface.
type View struct {
	ID          int64
	Scope       string
	Title       string
	Hotkey      string
	CallbackURL string
	CreatedAt   time.Time
}

// Registry persists plugin views via *db.DB. All methods are safe for
// concurrent use — the underlying DB serializes writes.
type Registry struct {
	db *db.DB
}

// New constructs a Registry backed by database.
func New(database *db.DB) *Registry {
	return &Registry{db: database}
}

// Register stores a new view. Returns the persisted View on success. Empty
// title or callback_url is rejected up front; (scope, title) collisions
// surface as ErrViewExists.
func (r *Registry) Register(scope, title, hotkey, callbackURL string) (*View, error) {
	if strings.TrimSpace(title) == "" {
		return nil, ErrTitleRequired
	}
	if strings.TrimSpace(callbackURL) == "" {
		return nil, ErrCallbackURLRequired
	}

	if existing, _ := r.db.GetPluginView(scope, title); existing != nil {
		return nil, ErrViewExists
	}

	row, err := r.db.AddPluginView(scope, title, hotkey, callbackURL)
	if err != nil {
		return nil, err
	}
	return toView(*row), nil
}

// Get returns the view at (scope, title) and whether it was found.
func (r *Registry) Get(scope, title string) (*View, bool) {
	row, err := r.db.GetPluginView(scope, title)
	if err != nil || row == nil {
		return nil, false
	}
	return toView(*row), true
}

// List returns every registered view ordered by insertion order. Nil on
// underlying DB error (callers can render that as an empty list).
func (r *Registry) List() []*View {
	rows, err := r.db.PluginViews()
	if err != nil {
		return nil
	}
	out := make([]*View, 0, len(rows))
	for _, row := range rows {
		out = append(out, toView(row))
	}
	return out
}

// Unregister removes the view at (scope, title). Returns ErrViewNotFound if
// no matching row existed.
func (r *Registry) Unregister(scope, title string) error {
	ok, err := r.db.DeletePluginView(scope, title)
	if err != nil {
		return err
	}
	if !ok {
		return ErrViewNotFound
	}
	return nil
}

// RevokeScope cascade-deletes every view owned by scope. Safe no-op when no
// rows match — this is the hook a future scope-token revocation handler
// calls to clean up the views the now-revoked plugin had registered.
func (r *Registry) RevokeScope(scope string) error {
	_, err := r.db.DeletePluginViewsByScope(scope)
	return err
}

// ParseHotkey turns a stored hotkey string (eg "ctrl+l") into a tcell.Key
// constant. Only "ctrl+<letter>" is supported today, matching the substrate's
// minimum-viable shape. Returns (key, true) on hit; (0, false) on miss.
//
// Letters are case-insensitive. "ctrl+l" and "Ctrl+L" both map to
// tcell.KeyCtrlL.
func ParseHotkey(s string) (tcellKey, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	if !strings.HasPrefix(s, "ctrl+") {
		return 0, false
	}
	rest := s[len("ctrl+"):]
	if len(rest) != 1 {
		return 0, false
	}
	c := rest[0]
	if c < 'a' || c > 'z' {
		return 0, false
	}
	// tcell encodes Ctrl+letter keys with the uppercase-letter ASCII value:
	// KeyCtrlA == 'A' == 65, KeyCtrlB == 'B' == 66, ..., KeyCtrlZ == 'Z' == 90.
	// NOT the C0 control codes (1..26) — those are the byte values the TTY
	// emits, but tcell's Key constants are a different encoding. Confirmed
	// by running `tcell.NewEventKey(tcell.KeyCtrlL, 0, 0).Key()` → 76.
	return tcellKey(c - 'a' + 'A'), true
}

// tcellKey aliases the tcell.Key int so the views package doesn't pull tcell
// into its public API. Callers compare against tcell.KeyCtrlA etc by raw int.
type tcellKey int16

func toView(row db.PluginView) *View {
	return &View{
		ID:          row.ID,
		Scope:       row.Scope,
		Title:       row.Title,
		Hotkey:      row.Hotkey,
		CallbackURL: row.CallbackURL,
		CreatedAt:   row.CreatedAt,
	}
}
