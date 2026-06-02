package db

import (
	"database/sql"
	"errors"
	"time"
)

// PluginView is one plugin-registered top-level UI surface. Stored in the
// plugin_views table; see schema.go for the column shape.
type PluginView struct {
	ID          int64
	Scope       string
	Title       string
	Hotkey      string
	CallbackURL string
	CreatedAt   time.Time
}

// AddPluginView inserts a new plugin view, stamps its ID + CreatedAt, and
// returns the persisted row. Caller is responsible for uniqueness — SQLite
// enforces UNIQUE(scope, title); the resulting error is surfaced as-is so
// the higher-level registry can map it to a sentinel.
func (d *DB) AddPluginView(scope, title, hotkey, callbackURL string) (*PluginView, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	now := time.Now().UTC()
	res, err := d.conn.Exec(
		`INSERT INTO plugin_views (scope, title, hotkey, callback_url, created_at) VALUES (?, ?, ?, ?, ?)`,
		scope, title, hotkey, callbackURL, now.Format(time.RFC3339Nano),
	)
	if err != nil {
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	return &PluginView{
		ID:          id,
		Scope:       scope,
		Title:       title,
		Hotkey:      hotkey,
		CallbackURL: callbackURL,
		CreatedAt:   now,
	}, nil
}

// GetPluginView returns the row matching (scope, title) or (nil, nil) if no
// match. The scope+title pair is unique per the schema.
func (d *DB) GetPluginView(scope, title string) (*PluginView, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	var v PluginView
	var createdAt string
	err := d.conn.QueryRow(
		`SELECT id, scope, title, hotkey, callback_url, created_at FROM plugin_views WHERE scope = ? AND title = ?`,
		scope, title,
	).Scan(&v.ID, &v.Scope, &v.Title, &v.Hotkey, &v.CallbackURL, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	v.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	return &v, nil
}

// PluginViews returns every registered view ordered by insertion order.
func (d *DB) PluginViews() ([]PluginView, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	rows, err := d.conn.Query(
		`SELECT id, scope, title, hotkey, callback_url, created_at FROM plugin_views ORDER BY id`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PluginView
	for rows.Next() {
		var v PluginView
		var createdAt string
		if err := rows.Scan(&v.ID, &v.Scope, &v.Title, &v.Hotkey, &v.CallbackURL, &createdAt); err != nil {
			continue
		}
		v.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
		out = append(out, v)
	}
	return out, nil
}

// DeletePluginView removes the (scope, title) row. Returns true if a row was
// actually deleted, false if no match existed.
func (d *DB) DeletePluginView(scope, title string) (bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	res, err := d.conn.Exec(`DELETE FROM plugin_views WHERE scope = ? AND title = ?`, scope, title)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// DeletePluginViewsByScope removes every row matching scope. Returns the
// count deleted.
func (d *DB) DeletePluginViewsByScope(scope string) (int64, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	res, err := d.conn.Exec(`DELETE FROM plugin_views WHERE scope = ?`, scope)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}
