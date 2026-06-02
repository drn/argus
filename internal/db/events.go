package db

import (
	"errors"
	"fmt"
	"time"

	"github.com/drn/argus/internal/model"
)

// MaxEventsRetained caps the events table. InsertEvent trims the oldest rows
// past this threshold so the table never grows without bound. Plugins that
// reconnect with a `since` cursor older than the oldest surviving row receive
// a synthetic `resync` event so they know to resnapshot — see the SSE
// handler in internal/api.
const MaxEventsRetained = 10000

// eventsCapForTest is the row cap consulted by InsertEvent. Tests override it
// via SetEventsCapForTest to exercise eviction without inserting
// MaxEventsRetained+1 rows. Production code never writes to this — it stays
// at MaxEventsRetained.
var eventsCapForTest = MaxEventsRetained

// SetEventsCapForTest is a test seam — it overrides the events ring cap and
// returns the prior value. Used by api package tests that need to exercise
// the SSE resync path without inserting 10k rows. Production code MUST NOT
// call this.
func SetEventsCapForTest(cap int) int {
	prev := eventsCapForTest
	eventsCapForTest = cap
	return prev
}

// ErrEventTypeRequired is returned by InsertEvent when the caller passed an
// empty Type. The events ring is shared across plugins; rejecting empty types
// early prevents an entire family of "type:" SSE prefix glitches downstream.
var ErrEventTypeRequired = errors.New("event type is required")

// InsertEvent appends a new event row, stamping ID and At on the supplied
// Event before returning it. Oldest rows are evicted to keep the table within
// eventsCapForTest. Returns ErrEventTypeRequired on empty Type.
//
// The returned pointer is the same as the input — the caller's local Event
// observes the new ID/At too. Tests can rely on this aliasing.
func (d *DB) InsertEvent(ev *model.Event) (*model.Event, error) {
	if ev.Type == "" {
		return nil, ErrEventTypeRequired
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	now := time.Now()
	if ev.At.IsZero() {
		ev.At = now
	}
	payload := string(ev.Payload)

	res, err := d.conn.Exec(
		`INSERT INTO events (type, at, task_id, payload_json) VALUES (?, ?, ?, ?)`,
		ev.Type, formatTime(ev.At), ev.TaskID, payload,
	)
	if err != nil {
		return nil, fmt.Errorf("insert event: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("event last insert id: %w", err)
	}
	ev.ID = id

	// Evict rows past the cap. Using `id NOT IN (top N)` keeps the AUTOINCREMENT
	// counter intact so cursors stay monotonic across evictions.
	if eventsCapForTest > 0 {
		if _, err := d.conn.Exec(
			`DELETE FROM events WHERE id NOT IN (SELECT id FROM events ORDER BY id DESC LIMIT ?)`,
			eventsCapForTest,
		); err != nil {
			return nil, fmt.Errorf("event eviction: %w", err)
		}
	}

	return ev, nil
}

// EventsSince returns events with id > cursorID, ordered ascending by id, up
// to limit rows. limit <= 0 means "no per-call cap" — the result is still
// bounded by the table cap (MaxEventsRetained). cursorID of 0 returns the
// entire retained ring.
func (d *DB) EventsSince(cursorID int64, limit int) ([]*model.Event, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	query := `SELECT id, type, at, task_id, payload_json FROM events WHERE id > ? ORDER BY id ASC`
	args := []any{cursorID}
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}

	rows, err := d.conn.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("query events: %w", err)
	}
	defer rows.Close()

	var out []*model.Event
	for rows.Next() {
		ev := &model.Event{}
		var at, payload string
		if err := rows.Scan(&ev.ID, &ev.Type, &at, &ev.TaskID, &payload); err != nil {
			return nil, fmt.Errorf("scan event: %w", err)
		}
		ev.At = parseTime(at)
		if payload != "" {
			ev.Payload = []byte(payload)
		}
		out = append(out, ev)
	}
	return out, nil
}

// OldestEventID returns the smallest id currently in the events table, or 0
// when the table is empty. Used by the SSE handler to detect when a client's
// `since` cursor predates the retained ring (the gap triggers a synthetic
// resync event).
func (d *DB) OldestEventID() (int64, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	var id int64
	if err := d.conn.QueryRow(`SELECT COALESCE(MIN(id), 0) FROM events`).Scan(&id); err != nil {
		return 0, fmt.Errorf("oldest event id: %w", err)
	}
	return id, nil
}

// LatestEventID returns the largest id currently in the events table, or 0
// when the table is empty. Used by the SSE handler to fence the replay range
// against incoming live events.
func (d *DB) LatestEventID() (int64, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	var id int64
	if err := d.conn.QueryRow(`SELECT COALESCE(MAX(id), 0) FROM events`).Scan(&id); err != nil {
		return 0, fmt.Errorf("latest event id: %w", err)
	}
	return id, nil
}
