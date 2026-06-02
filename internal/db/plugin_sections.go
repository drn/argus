package db

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/drn/argus/internal/tui/settings"
)

// PluginSection is the persisted form of a plugin-registered settings
// section. Mirrors [settings.Section] one-to-one — separate types exist only
// so callers don't have to import internal/tui/settings just to ask the DB
// what's persisted. Conversion goes through ToSection / FromSection so the
// JSON round-trip is exercised in one place.
type PluginSection struct {
	ID          int64
	Scope       string
	Title       string
	Type        string
	SpecJSON    string
	CallbackURL string
	// AuthHeader is the verbatim Authorization-header value the callback proxy
	// MUST set when POSTing form submissions to CallbackURL. Empty means no
	// header; plugins that gate /mcp/* or their own endpoints on a static
	// header use this to authenticate the daemon's callback.
	AuthHeader string
	CreatedAt  time.Time
}

// ErrPluginSectionInvalid rejects writes with empty scope, title, or
// callback URL. Returned by UpsertPluginSection; the HTTP handler maps it
// to a 400.
var ErrPluginSectionInvalid = errors.New("plugin_settings: scope, title, and callback_url must all be non-empty")

// UpsertPluginSection writes (or replaces) the section identified by
// (scope, title). Plugins re-register with the same key to update fields;
// the unique constraint + ON CONFLICT clause keep this idempotent. Returns
// the section's row id so callers can log it for audit purposes.
func (d *DB) UpsertPluginSection(s PluginSection) (int64, error) {
	if s.Scope == "" || s.Title == "" || s.CallbackURL == "" {
		return 0, ErrPluginSectionInvalid
	}
	if s.Type == "" {
		s.Type = string(settings.TypeForm)
	}
	now := formatTime(time.Now())
	d.mu.Lock()
	defer d.mu.Unlock()
	// SQLite's RETURNING needs to come AFTER the ON CONFLICT clause so the
	// upsert path also yields the id; tested via TestUpsertPluginSection_Replaces.
	var id int64
	err := d.conn.QueryRow(
		`INSERT INTO plugin_settings (scope, title, type, spec_json, callback_url, auth_header, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(scope, title) DO UPDATE SET
		   type=excluded.type,
		   spec_json=excluded.spec_json,
		   callback_url=excluded.callback_url,
		   auth_header=excluded.auth_header
		 RETURNING id`,
		s.Scope, s.Title, s.Type, s.SpecJSON, s.CallbackURL, s.AuthHeader, now,
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("upsert plugin section: %w", err)
	}
	return id, nil
}

// DeletePluginSection removes (scope, title) and returns whether a row was
// removed. False is informational; HTTP handlers use it to distinguish
// 404 from 200.
func (d *DB) DeletePluginSection(scope, title string) (bool, error) {
	if scope == "" || title == "" {
		return false, ErrPluginSectionInvalid
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	res, err := d.conn.Exec(`DELETE FROM plugin_settings WHERE scope=? AND title=?`, scope, title)
	if err != nil {
		return false, fmt.Errorf("delete plugin section: %w", err)
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// DeletePluginSectionsByScope removes every section owned by scope and
// returns the row count. Called by the token revocation path so a revoked
// plugin's rail entries disappear immediately.
func (d *DB) DeletePluginSectionsByScope(scope string) (int, error) {
	if scope == "" {
		return 0, nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	res, err := d.conn.Exec(`DELETE FROM plugin_settings WHERE scope=?`, scope)
	if err != nil {
		return 0, fmt.Errorf("delete plugin sections by scope: %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// ListPluginSections returns every persisted section sorted by (title,
// scope) — the same order the TUI uses when rendering the "Plugins" rail
// header, so consumers can iterate without resorting.
func (d *DB) ListPluginSections() ([]PluginSection, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	rows, err := d.conn.Query(
		`SELECT id, scope, title, type, spec_json, callback_url, auth_header, created_at
		 FROM plugin_settings
		 ORDER BY title ASC, scope ASC`,
	)
	if err != nil {
		return nil, fmt.Errorf("list plugin sections: %w", err)
	}
	defer rows.Close() //nolint:errcheck
	var out []PluginSection
	for rows.Next() {
		var p PluginSection
		var created string
		if err := rows.Scan(&p.ID, &p.Scope, &p.Title, &p.Type, &p.SpecJSON, &p.CallbackURL, &p.AuthHeader, &created); err != nil {
			return nil, fmt.Errorf("scan plugin section: %w", err)
		}
		p.CreatedAt = parseTime(created)
		out = append(out, p)
	}
	return out, nil
}

// PluginSections returns every persisted section, decoded into typed
// [settings.Section] values. Corrupt rows (unparseable spec_json) are
// silently dropped so one bad row cannot starve the TUI's "Plugins" header
// — the daemon's rehydrate path takes the same posture and logs the skip.
// The caller doesn't see the corruption, but operators can find it in the
// daemon log; this is the same trade-off `Replace` makes.
func (d *DB) PluginSections() ([]settings.Section, error) {
	rows, err := d.ListPluginSections()
	if err != nil {
		return nil, err
	}
	out := make([]settings.Section, 0, len(rows))
	for _, row := range rows {
		sec, perr := row.ToSection()
		if perr != nil {
			continue
		}
		out = append(out, sec)
	}
	return out, nil
}

// ToSection decodes the stored spec_json into a [settings.Section]. Returns
// an error when the row is corrupt (unparseable JSON) — callers (the
// daemon's boot rehydrate path) treat that as a skip-and-log rather than
// fatal so a single bad row can't take the registry offline.
func (p PluginSection) ToSection() (settings.Section, error) {
	sec := settings.Section{
		Scope:       p.Scope,
		Title:       p.Title,
		Type:        settings.SectionType(p.Type),
		CallbackURL: p.CallbackURL,
		AuthHeader:  p.AuthHeader,
	}
	if p.SpecJSON == "" {
		return sec, nil
	}
	var spec settings.FormSpec
	if err := json.Unmarshal([]byte(p.SpecJSON), &spec); err != nil {
		return sec, fmt.Errorf("plugin section %q (scope %q): parse spec: %w", p.Title, p.Scope, err)
	}
	sec.Spec = &spec
	return sec, nil
}

// FromSection encodes a [settings.Section] into the persisted form. Returns
// an error only on JSON-encode failure, which in practice means the caller
// passed a Section with an unencodable Default value (e.g., a func) — the
// validator at ParseSection rejects those upstream, so the error path here
// is defensive.
func FromSection(s settings.Section) (PluginSection, error) {
	p := PluginSection{
		Scope:       s.Scope,
		Title:       s.Title,
		Type:        string(s.Type),
		CallbackURL: s.CallbackURL,
		AuthHeader:  s.AuthHeader,
	}
	if s.Spec != nil {
		raw, err := json.Marshal(s.Spec)
		if err != nil {
			return p, fmt.Errorf("encode spec: %w", err)
		}
		p.SpecJSON = string(raw)
	}
	return p, nil
}
