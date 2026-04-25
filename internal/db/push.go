package db

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// PushSubscription represents a registered Web Push subscription for a device.
type PushSubscription struct {
	ID        int64
	Label     string
	Endpoint  string
	P256dh    string
	Auth      string
	CreatedAt time.Time
}

// AddPushSubscription inserts (or replaces by endpoint) a subscription.
func (d *DB) AddPushSubscription(s PushSubscription) (int64, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	// On endpoint conflict, refresh the cryptographic keys and label.
	// Browsers may rotate p256dh/auth on re-subscribe; keeping the stale
	// keys would cause every subsequent push to fail VAPID encryption.
	res, err := d.conn.Exec(
		`INSERT INTO push_subscriptions (label, endpoint, p256dh, auth_key, created_at)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(endpoint) DO UPDATE SET
		   label=excluded.label,
		   p256dh=excluded.p256dh,
		   auth_key=excluded.auth_key`,
		s.Label, s.Endpoint, s.P256dh, s.Auth, time.Now().Unix(),
	)
	if err != nil {
		return 0, fmt.Errorf("insert push sub: %w", err)
	}
	return res.LastInsertId()
}

// PushSubscriptions returns every registered subscription.
func (d *DB) PushSubscriptions() ([]PushSubscription, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	rows, err := d.conn.Query(
		`SELECT id, label, endpoint, p256dh, auth_key, created_at FROM push_subscriptions ORDER BY id`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PushSubscription
	for rows.Next() {
		var s PushSubscription
		var ts int64
		if err := rows.Scan(&s.ID, &s.Label, &s.Endpoint, &s.P256dh, &s.Auth, &ts); err != nil {
			continue
		}
		s.CreatedAt = time.Unix(ts, 0)
		out = append(out, s)
	}
	return out, nil
}

// DeletePushSubscription removes a subscription by id.
func (d *DB) DeletePushSubscription(id int64) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	res, err := d.conn.Exec(`DELETE FROM push_subscriptions WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return errors.New("subscription not found")
	}
	return nil
}

// DeletePushSubscriptionByEndpoint is used by the push fan-out to drop
// subscriptions that the push service has reported as expired (HTTP 410).
func (d *DB) DeletePushSubscriptionByEndpoint(endpoint string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, err := d.conn.Exec(`DELETE FROM push_subscriptions WHERE endpoint = ?`, endpoint)
	return err
}

// GetConfigValue reads a single config kv. Returns "" (no error) if missing.
func (d *DB) GetConfigValue(key string) (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	var v string
	err := d.conn.QueryRow(`SELECT value FROM config WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return v, nil
}
