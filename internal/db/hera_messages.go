package db

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/drn/argus/internal/model"
)

// Sentinel errors for the hera_messages store.
var (
	// ErrHeraMessageBodyTooLarge fires when body exceeds the 64 KiB cap.
	ErrHeraMessageBodyTooLarge = errors.New("hera message body exceeds 64 KiB cap")
	// ErrHeraMessageTldrRequired fires when tldr is empty.
	ErrHeraMessageTldrRequired = errors.New("hera message tldr is required")
	// ErrHeraMessageTldrTooLong fires when tldr exceeds 120 characters.
	ErrHeraMessageTldrTooLong = errors.New("hera message tldr exceeds 120-char cap")
	// ErrHeraMessageTldrMultiline fires when tldr contains a newline.
	ErrHeraMessageTldrMultiline = errors.New("hera message tldr must be a single line")
	// ErrHeraMessageSelfSend fires when fromRoleID == toRoleID.
	ErrHeraMessageSelfSend = errors.New("hera message: cannot send to self")
	// ErrHeraMessageInboxFull fires when the recipient already holds >=
	// HeraMaxUnreadPerRole unread messages.
	ErrHeraMessageInboxFull = errors.New("hera message: recipient inbox full (500 unread cap)")
	// ErrHeraMessageRateLimited fires when the sender has emitted >=
	// HeraMaxSendsPerMinute messages in the past 60 s.
	ErrHeraMessageRateLimited = errors.New("hera message: sender rate limit exceeded (50/min)")
	// ErrHeraMessageRecipientInvalid fires when the recipient role does not
	// exist or is archived (FK would catch non-existence but not archived).
	ErrHeraMessageRecipientInvalid = errors.New("hera message: recipient role is missing or archived")
	// ErrHeraMessageInvalidReplyTo fires when in_reply_to names a message that
	// does not exist.
	ErrHeraMessageInvalidReplyTo = errors.New("hera message: in_reply_to references unknown message")
)

// Caps for the hera message store. Kept in sync with the task_messages
// equivalents (model.MaxMessageBodyBytes / MaxUnreadPerRecipient / MaxSendsPerMinute).
const (
	HeraMaxUnreadPerRole  = 500
	HeraMaxSendsPerMinute = 50
	HeraMaxTldrLen        = 120
	heraRateLimitWindow   = time.Minute
)

// Delivery mode constants for hera_messages.delivery_mode.
const (
	HeraDeliveryPending         = "pending"
	HeraDeliveryIdleSubmit      = "idle_submit"
	HeraDeliveryQueuedNoBinding = "queued_no_binding"
)

// HeraMessage is one message on the hera role-addressed bus.
type HeraMessage struct {
	ID           int64
	FromRoleID   int64
	ToRoleID     int64
	Body         string
	Tldr         string
	InReplyTo    *int64
	SentAt       time.Time
	ReadAt       *time.Time
	DeliveryMode string
	DeliveredAt  *time.Time
}

// SendHeraMessage validates and persists a new hera message. Caps applied:
//   - body ≤ 64 KiB (model.MaxMessageBodyBytes)
//   - tldr required, ≤ 120 chars, single line
//   - self-send rejected
//   - recipient role must exist AND be active (not archived)
//   - recipient unread cap: HeraMaxUnreadPerRole (500)
//   - sender rolling 60-second rate cap: HeraMaxSendsPerMinute (50)
//   - in_reply_to, if non-nil, must reference an existing message
//
// All checks run inside the DB lock so concurrent senders can't both squeak
// past the caps. Returns the inserted message on success.
func (d *DB) SendHeraMessage(fromRoleID, toRoleID int64, body, tldr string, inReplyTo *int64) (*HeraMessage, error) {
	// Validate before acquiring the lock — cheap, no DB needed.
	if fromRoleID == toRoleID {
		return nil, ErrHeraMessageSelfSend
	}
	if len(body) > model.MaxMessageBodyBytes {
		return nil, ErrHeraMessageBodyTooLarge
	}
	if tldr == "" {
		return nil, ErrHeraMessageTldrRequired
	}
	if len(tldr) > HeraMaxTldrLen {
		return nil, ErrHeraMessageTldrTooLong
	}
	if strings.ContainsAny(tldr, "\n\r") {
		return nil, ErrHeraMessageTldrMultiline
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	// Recipient must exist and be active. The FK prevents non-existent IDs but
	// not archived roles (archived_at IS NOT NULL). We must check explicitly.
	var recipientArchivedAt sql.NullString
	err := d.conn.QueryRow(
		`SELECT archived_at FROM hera_roles WHERE id=?`, toRoleID,
	).Scan(&recipientArchivedAt)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && recipientArchivedAt.Valid) {
		return nil, ErrHeraMessageRecipientInvalid
	}
	if err != nil {
		return nil, fmt.Errorf("check recipient role: %w", err)
	}

	// Inbox-full check (per-recipient unread count).
	var unread int
	if err := d.conn.QueryRow(
		`SELECT COUNT(*) FROM hera_messages WHERE to_role_id=? AND read_at IS NULL`, toRoleID,
	).Scan(&unread); err != nil {
		return nil, fmt.Errorf("count hera unread: %w", err)
	}
	if unread >= HeraMaxUnreadPerRole {
		return nil, ErrHeraMessageInboxFull
	}

	// Rate-limit check: rolling 60-second window on the sender.
	since := time.Now().Add(-heraRateLimitWindow)
	var recent int
	if err := d.conn.QueryRow(
		`SELECT COUNT(*) FROM hera_messages WHERE from_role_id=? AND sent_at>=?`,
		fromRoleID, formatTime(since),
	).Scan(&recent); err != nil {
		return nil, fmt.Errorf("count hera recent sends: %w", err)
	}
	if recent >= HeraMaxSendsPerMinute {
		return nil, ErrHeraMessageRateLimited
	}

	// Validate in_reply_to if provided.
	if inReplyTo != nil {
		var exists int
		if err := d.conn.QueryRow(
			`SELECT COUNT(*) FROM hera_messages WHERE id=?`, *inReplyTo,
		).Scan(&exists); err != nil {
			return nil, fmt.Errorf("check in_reply_to: %w", err)
		}
		if exists == 0 {
			return nil, ErrHeraMessageInvalidReplyTo
		}
	}

	now := formatTime(time.Now())
	var replyToArg any
	if inReplyTo != nil {
		replyToArg = *inReplyTo
	}
	res, err := d.conn.Exec(
		`INSERT INTO hera_messages (from_role_id, to_role_id, body, tldr, in_reply_to, sent_at, delivery_mode)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		fromRoleID, toRoleID, body, tldr, replyToArg, now, HeraDeliveryPending,
	)
	if err != nil {
		return nil, fmt.Errorf("insert hera message: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("insert hera message: last insert id: %w", err)
	}
	return &HeraMessage{
		ID:           id,
		FromRoleID:   fromRoleID,
		ToRoleID:     toRoleID,
		Body:         body,
		Tldr:         tldr,
		InReplyTo:    inReplyTo,
		SentAt:       parseTime(now),
		DeliveryMode: HeraDeliveryPending,
	}, nil
}

// HeraInbox returns unread messages addressed to roleID, oldest first.
func (d *DB) HeraInbox(roleID int64) ([]*HeraMessage, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	rows, err := d.conn.Query(
		`SELECT id, from_role_id, to_role_id, body, tldr, in_reply_to, sent_at, read_at, delivery_mode, delivered_at
		 FROM hera_messages
		 WHERE to_role_id=? AND read_at IS NULL
		 ORDER BY sent_at ASC, id ASC`,
		roleID,
	)
	if err != nil {
		return nil, fmt.Errorf("hera inbox: %w", err)
	}
	defer rows.Close()

	var out []*HeraMessage
	for rows.Next() {
		m, err := scanHeraMessage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// MarkHeraMessagesRead stamps read_at=now on the given message IDs, but only
// when they are addressed to roleID. IDs addressed to other roles (or that
// don't exist) are silently skipped — keeps the call idempotent for partially-
// overlapping batches. Returns the count of rows actually flipped.
func (d *DB) MarkHeraMessagesRead(roleID int64, ids []int64) (int, error) {
	if len(ids) == 0 {
		return 0, nil
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	now := formatTime(time.Now())
	placeholders := make([]string, len(ids))
	args := make([]any, 0, len(ids)+2)
	args = append(args, now, roleID)
	for i, id := range ids {
		placeholders[i] = "?"
		args = append(args, id)
	}

	//nolint:gosec // G202: placeholders are a fixed list of `?` literals; IDs are bound parameters.
	q := `UPDATE hera_messages SET read_at=? WHERE to_role_id=? AND read_at IS NULL AND id IN (` +
		strings.Join(placeholders, ",") + `)`
	res, err := d.conn.Exec(q, args...)
	if err != nil {
		return 0, fmt.Errorf("mark hera messages read: %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// HeraMessagesByIDs loads messages by their IDs in an arbitrary order. Missing
// IDs are silently skipped.
func (d *DB) HeraMessagesByIDs(ids []int64) ([]*HeraMessage, error) {
	if len(ids) == 0 {
		return nil, nil
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	placeholders := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args[i] = id
	}

	//nolint:gosec // G202: placeholders are a fixed list of `?` literals; IDs are bound parameters.
	q := `SELECT id, from_role_id, to_role_id, body, tldr, in_reply_to, sent_at, read_at, delivery_mode, delivered_at
	      FROM hera_messages WHERE id IN (` + strings.Join(placeholders, ",") + `)`
	rows, err := d.conn.Query(q, args...)
	if err != nil {
		return nil, fmt.Errorf("hera messages by ids: %w", err)
	}
	defer rows.Close()

	var out []*HeraMessage
	for rows.Next() {
		m, err := scanHeraMessage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// MarkHeraMessageDelivered stamps delivered_at and delivery_mode on a message.
// Idempotent — re-stamping is harmless.
func (d *DB) MarkHeraMessageDelivered(id int64, mode string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	_, err := d.conn.Exec(
		`UPDATE hera_messages SET delivery_mode=?, delivered_at=? WHERE id=?`,
		mode, formatTime(time.Now()), id,
	)
	if err != nil {
		return fmt.Errorf("mark hera message delivered: %w", err)
	}
	return nil
}

// scanHeraMessage reads one HeraMessage from a row using the canonical column
// order: id, from_role_id, to_role_id, body, tldr, in_reply_to, sent_at,
// read_at, delivery_mode, delivered_at.
func scanHeraMessage(s rowScanner) (*HeraMessage, error) {
	var m HeraMessage
	var inReplyTo sql.NullInt64
	var sentAt, deliveryMode string
	var readAt, deliveredAt sql.NullString
	if err := s.Scan(
		&m.ID, &m.FromRoleID, &m.ToRoleID, &m.Body, &m.Tldr, &inReplyTo,
		&sentAt, &readAt, &deliveryMode, &deliveredAt,
	); err != nil {
		return nil, fmt.Errorf("scan hera message: %w", err)
	}
	if inReplyTo.Valid {
		v := inReplyTo.Int64
		m.InReplyTo = &v
	}
	m.SentAt = parseTime(sentAt)
	m.ReadAt = nullTimePtr(readAt)
	m.DeliveryMode = deliveryMode
	m.DeliveredAt = nullTimePtr(deliveredAt)
	return &m, nil
}
