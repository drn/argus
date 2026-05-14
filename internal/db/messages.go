package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/drn/argus/internal/model"
)

// Sentinel errors returned by the task_messages methods. Callers use
// errors.Is — string-matching silently breaks on any future rename.
var (
	// ErrMessageBodyTooLarge fires when the body exceeds model.MaxMessageBodyBytes.
	ErrMessageBodyTooLarge = errors.New("message body exceeds 64 KiB cap")
	// ErrMessageSelfSend rejects A→A messages so an orchestrator that
	// accidentally wires a task to itself can't fill its own inbox.
	ErrMessageSelfSend = errors.New("cannot send message to self")
	// ErrMessageInboxFull rejects sends when the recipient already has
	// MaxUnreadPerRecipient unread messages. The cap prevents one chatty
	// sender from filling the table while a slow recipient catches up.
	ErrMessageInboxFull = errors.New("recipient inbox full (500 unread cap)")
	// ErrMessageRateLimited fires when the sender has emitted MaxSendsPerMinute
	// messages in the past 60s. Sliding window from time.Now().
	ErrMessageRateLimited = errors.New("sender rate limit exceeded (50/min)")
)

// MaxUnreadPerRecipient caps a single recipient's unread inbox. Once breached
// every subsequent send rejects with ErrMessageInboxFull until the recipient
// catches up via task_message_ack. 500 is two orders of magnitude above any
// reasonable cooperating-task chatter.
const MaxUnreadPerRecipient = 500

// MaxSendsPerMinute throttles a single sender. 50 is generous for an agent
// loop that pings a peer once per build cycle, tight enough to stop a runaway
// orchestrator from filling SQLite in seconds.
const MaxSendsPerMinute = 50

// rateLimitWindow defines the rolling window used by the per-sender rate
// limit. Aligned with MaxSendsPerMinute's name — both are intentionally
// expressed in minutes to keep the math obvious.
const rateLimitWindow = time.Minute

// InsertMessage persists a new TaskMessage. The caller must populate From, To,
// Kind, and Body; CreatedAt is stamped here, ID is generated when empty.
// Validation runs first (kind/body shape), then the caps (size, self-send,
// inbox, rate limit). All checks happen inside the DB lock so concurrent
// senders can't both squeak past the cap.
//
// Returns the inserted message (ID, CreatedAt filled in) on success.
func (d *DB) InsertMessage(m *model.TaskMessage) (*model.TaskMessage, error) {
	if err := m.Validate(); err != nil {
		return nil, err
	}
	if len(m.Body) > model.MaxMessageBodyBytes {
		return nil, ErrMessageBodyTooLarge
	}
	if m.From == m.To {
		return nil, ErrMessageSelfSend
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	now := time.Now()

	// Inbox-full check (per-recipient unread count).
	var unread int
	if err := d.conn.QueryRow(`SELECT COUNT(*) FROM task_messages WHERE to_task_id=? AND read_at=''`, m.To).Scan(&unread); err != nil {
		return nil, fmt.Errorf("count unread: %w", err)
	}
	if unread >= MaxUnreadPerRecipient {
		return nil, ErrMessageInboxFull
	}

	// Rate-limit check (per-sender, rolling 1-minute window). Note: the
	// count comes from extant rows, so DeleteMessagesForTask (fired when a
	// recipient is archived/destroyed) effectively resets the window for any
	// sender whose recent traffic targeted that recipient. Acceptable under
	// the cooperative single-user threat model — a misbehaving sender would
	// have to coordinate with an archive cascade to exploit this. If we ever
	// need a strict per-sender rate floor, track sends in a separate
	// archive-immune table.
	since := now.Add(-rateLimitWindow)
	var recent int
	if err := d.conn.QueryRow(`SELECT COUNT(*) FROM task_messages WHERE from_task_id=? AND created_at>=?`, m.From, formatTime(since)).Scan(&recent); err != nil {
		return nil, fmt.Errorf("count recent sends: %w", err)
	}
	if recent >= MaxSendsPerMinute {
		return nil, ErrMessageRateLimited
	}

	if m.ID == "" {
		m.ID = generateID()
	}
	if m.CreatedAt.IsZero() {
		m.CreatedAt = now
	}

	_, err := d.conn.Exec(
		`INSERT INTO task_messages (id, from_task_id, to_task_id, kind, body, in_reply_to, created_at, read_at) VALUES (?,?,?,?,?,?,?,?)`,
		m.ID, m.From, m.To, string(m.Kind), m.Body, m.InReplyTo, formatTime(m.CreatedAt), formatTime(m.ReadAt),
	)
	if err != nil {
		return nil, fmt.Errorf("insert message: %w", err)
	}
	return m, nil
}

// InboxFilter narrows the messages returned by Inbox. Zero-value fields are
// "no filter". Sender, when set, restricts to messages sent by that task ID.
// Since, when non-zero, restricts to created_at > Since (strict inequality so
// a caller can pass the timestamp of the last message they saw without
// double-counting it).
type InboxFilter struct {
	UnreadOnly bool
	Sender     string
	Since      time.Time
	Limit      int
}

// MaxInboxLimit caps a single Inbox call. 500 matches MaxUnreadPerRecipient
// so an agent that lets its inbox fill to the cap can still drain it in one
// call. Larger limits are silently clamped.
const MaxInboxLimit = 500

// DefaultInboxLimit is used when InboxFilter.Limit is zero.
const DefaultInboxLimit = 50

// Inbox returns messages addressed to toID, oldest first. The default limit
// is 50; pass InboxFilter.Limit to override (clamped to MaxInboxLimit).
func (d *DB) Inbox(toID string, f InboxFilter) ([]*model.TaskMessage, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = DefaultInboxLimit
	}
	if limit > MaxInboxLimit {
		limit = MaxInboxLimit
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	q := `SELECT id, from_task_id, to_task_id, kind, body, in_reply_to, created_at, read_at FROM task_messages WHERE to_task_id=?`
	args := []any{toID}
	if f.UnreadOnly {
		q += ` AND read_at=''`
	}
	if f.Sender != "" {
		q += ` AND from_task_id=?`
		args = append(args, f.Sender)
	}
	if !f.Since.IsZero() {
		q += ` AND created_at>?`
		args = append(args, formatTime(f.Since))
	}
	q += ` ORDER BY created_at ASC, id ASC LIMIT ?`
	args = append(args, limit)

	rows, err := d.conn.Query(q, args...)
	if err != nil {
		return nil, fmt.Errorf("query inbox: %w", err)
	}
	defer rows.Close()

	var out []*model.TaskMessage
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, nil
}

// AckMessages marks the supplied IDs read_at=now() only when the recipient
// matches toID. IDs that don't belong to toID (or don't exist) are silently
// ignored — keeps the call idempotent for partially-overlapping batches and
// avoids leaking which message IDs are valid via error messages.
//
// Returns the number of rows actually flipped from unread to read.
func (d *DB) AckMessages(toID string, ids []string) (int, error) {
	if len(ids) == 0 {
		return 0, nil
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	now := formatTime(time.Now())

	// Build (?,?,?...) placeholder list. Capped by the caller (MCP limits
	// the batch to 500); SQLite's variable count limit is 999 by default.
	placeholders := make([]string, len(ids))
	args := make([]any, 0, len(ids)+2)
	args = append(args, now, toID)
	for i, id := range ids {
		placeholders[i] = "?"
		args = append(args, id)
	}

	//nolint:gosec // G202: placeholders are a fixed list of `?` literals built from len(ids); IDs themselves are passed as bound parameters, not concatenated.
	q := `UPDATE task_messages SET read_at=? WHERE to_task_id=? AND read_at='' AND id IN (` + strings.Join(placeholders, ",") + `)`
	res, err := d.conn.Exec(q, args...)
	if err != nil {
		return 0, fmt.Errorf("ack messages: %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// UnreadCount returns the number of unread messages addressed to toID.
// Cheap — indexed by idx_msg_to_unread.
func (d *DB) UnreadCount(toID string) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	var n int
	if err := d.conn.QueryRow(`SELECT COUNT(*) FROM task_messages WHERE to_task_id=? AND read_at=''`, toID).Scan(&n); err != nil {
		return 0, fmt.Errorf("unread count: %w", err)
	}
	return n, nil
}

// FindReply returns the first answer to questionID sent by fromID, or nil if
// none exists yet. Used by task_ask's polling loop. The (in_reply_to,
// from_task_id) compound is small enough that the idx_msg_in_reply_to index
// covers it.
func (d *DB) FindReply(questionID, fromID string) (*model.TaskMessage, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	row := d.conn.QueryRow(
		`SELECT id, from_task_id, to_task_id, kind, body, in_reply_to, created_at, read_at FROM task_messages WHERE in_reply_to=? AND from_task_id=? ORDER BY created_at ASC LIMIT 1`,
		questionID, fromID,
	)
	m, err := scanMessage(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return m, nil
}

// WaitForReply polls FindReply until either a reply lands or ctx is cancelled.
// Cadence is 500ms; callers should set a context deadline. Returns (nil, nil)
// on context cancellation so a caller can distinguish "timeout" from "DB error".
func (d *DB) WaitForReply(ctx context.Context, questionID, fromID string) (*model.TaskMessage, error) {
	// Fast-path: maybe the answer already landed.
	if m, err := d.FindReply(questionID, fromID); err != nil {
		return nil, err
	} else if m != nil {
		return m, nil
	}

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil, nil
		case <-ticker.C:
			m, err := d.FindReply(questionID, fromID)
			if err != nil {
				return nil, err
			}
			if m != nil {
				return m, nil
			}
		}
	}
}

// DeleteMessagesForTask removes every message either sent by OR addressed to
// taskID. Called when a task is archived or destroyed so a dead recipient's
// queued inbox doesn't sit forever counting against MaxUnreadPerRecipient
// (and so the sender's history doesn't linger after a destroy). Returns the
// row count for logging.
func (d *DB) DeleteMessagesForTask(taskID string) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	res, err := d.conn.Exec(`DELETE FROM task_messages WHERE from_task_id=? OR to_task_id=?`, taskID, taskID)
	if err != nil {
		return 0, fmt.Errorf("delete messages for task: %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// scanMessage reads a TaskMessage from a row using the canonical column order
// used by both Inbox and FindReply.
func scanMessage(row scanner) (*model.TaskMessage, error) {
	m := &model.TaskMessage{}
	var kind, createdAt, readAt string
	if err := row.Scan(&m.ID, &m.From, &m.To, &kind, &m.Body, &m.InReplyTo, &createdAt, &readAt); err != nil {
		return nil, err
	}
	m.Kind = model.MessageKind(kind)
	m.CreatedAt = parseTime(createdAt)
	m.ReadAt = parseTime(readAt)
	return m, nil
}
