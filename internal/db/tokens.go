package db

import (
	"database/sql"
	"errors"
	"time"
)

// APIToken is a labeled per-device or plugin-scoped token. Plaintext is never
// stored — only the SHA-256 hex hash. The original plaintext is shown to the
// user once at mint time and then forgotten. Scope is empty for device tokens
// and non-empty for plugin tokens.
type APIToken struct {
	ID        int64
	Label     string
	Hash      string
	Last4     string
	Scope     string
	CreatedAt time.Time
	LastUsed  time.Time
	Revoked   bool
}

// AddAPIToken stores a hashed device token (empty scope); returns the new row
// id. Thin wrapper around AddAPITokenWithScope for the existing call sites.
func (d *DB) AddAPIToken(label, hash, last4 string) (int64, error) {
	return d.AddAPITokenWithScope(label, "", hash, last4)
}

// AddAPITokenWithScope stores a hashed token under the given scope; returns
// the new row id. A blank scope marks a device token (the original use case);
// any non-empty scope marks a plugin token.
func (d *DB) AddAPITokenWithScope(label, scope, hash, last4 string) (int64, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	res, err := d.conn.Exec(
		`INSERT INTO api_tokens (label, scope, hash, last4, created_at) VALUES (?, ?, ?, ?, ?)`,
		label, scope, hash, last4, time.Now().Unix(),
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// APITokens returns all tokens (revoked or not). Caller filters.
func (d *DB) APITokens() ([]APIToken, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	rows, err := d.conn.Query(
		`SELECT id, label, hash, last4, scope, created_at, last_used, revoked_at FROM api_tokens ORDER BY id`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []APIToken
	for rows.Next() {
		var t APIToken
		var created, lastUsed, revoked int64
		if err := rows.Scan(&t.ID, &t.Label, &t.Hash, &t.Last4, &t.Scope, &created, &lastUsed, &revoked); err != nil {
			continue
		}
		t.CreatedAt = time.Unix(created, 0)
		if lastUsed > 0 {
			t.LastUsed = time.Unix(lastUsed, 0)
		}
		t.Revoked = revoked > 0
		out = append(out, t)
	}
	return out, nil
}

// FindAPITokenByHash looks up a token by its SHA-256 hex hash. Returns nil if
// not found or revoked. Updates last_used on hit.
func (d *DB) FindAPITokenByHash(hash string) (*APIToken, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	var t APIToken
	var created, lastUsed, revoked int64
	err := d.conn.QueryRow(
		`SELECT id, label, hash, last4, scope, created_at, last_used, revoked_at
		 FROM api_tokens WHERE hash = ?`,
		hash,
	).Scan(&t.ID, &t.Label, &t.Hash, &t.Last4, &t.Scope, &created, &lastUsed, &revoked)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if revoked > 0 {
		return nil, nil
	}
	t.CreatedAt = time.Unix(created, 0)
	if lastUsed > 0 {
		t.LastUsed = time.Unix(lastUsed, 0)
	}
	// Best-effort touch.
	_, _ = d.conn.Exec(`UPDATE api_tokens SET last_used = ? WHERE id = ?`, time.Now().Unix(), t.ID)
	return &t, nil
}

// FindAPITokenByID returns a token by primary-key id, including revoked rows.
// Returns (nil, nil) when no row matches. Used by the revoke handler to read
// the scope BEFORE the row is marked revoked — the cascade-to-plugin-MCP
// sweep needs the scope and FindAPITokenByHash hides revoked rows.
func (d *DB) FindAPITokenByID(id int64) (*APIToken, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	var t APIToken
	var created, lastUsed, revoked int64
	err := d.conn.QueryRow(
		`SELECT id, label, hash, last4, scope, created_at, last_used, revoked_at
		 FROM api_tokens WHERE id = ?`,
		id,
	).Scan(&t.ID, &t.Label, &t.Hash, &t.Last4, &t.Scope, &created, &lastUsed, &revoked)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	t.CreatedAt = time.Unix(created, 0)
	if lastUsed > 0 {
		t.LastUsed = time.Unix(lastUsed, 0)
	}
	t.Revoked = revoked > 0
	return &t, nil
}

// RevokeAPIToken marks a token as revoked. Idempotent.
func (d *DB) RevokeAPIToken(id int64) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	res, err := d.conn.Exec(`UPDATE api_tokens SET revoked_at = ? WHERE id = ?`, time.Now().Unix(), id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return errors.New("token not found")
	}
	return nil
}
