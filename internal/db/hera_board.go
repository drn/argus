package db

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/drn/argus/internal/uxlog"
)

var (
	ErrHeraBoardInvalid  = errors.New("board: invalid item or coordinator")
	ErrHeraBoardNotFound = errors.New("board: open item not found for coordinator")
)

// HeraBoardItem is a curated human attention item, owned by a durable role.
type HeraBoardItem struct {
	ID             int64     `json:"id"`
	RoleID         int64     `json:"role_id"`
	Kind           string    `json:"kind"`
	Subject        string    `json:"subject"`
	Body           string    `json:"body"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
	Read           bool      `json:"read"`
	Resolved       bool      `json:"resolved"`
	ResolvedBy     string    `json:"resolved_by"`
	ResolutionNote string    `json:"resolution_note"`
}

const boardColumns = `id, role_id, kind, subject, body, created_at, updated_at, is_read, resolved, resolved_by, resolution_note`

func validBoardContent(kind, subject, body string) bool {
	return (kind == "decision" || kind == "fyi") && strings.TrimSpace(subject) != "" && len(subject) <= 240 && !strings.ContainsFunc(subject, unicode.IsControl) && len(body) <= 64*1024
}

func scanBoard(s rowScanner) (*HeraBoardItem, error) {
	var i HeraBoardItem
	var created, updated string
	err := s.Scan(&i.ID, &i.RoleID, &i.Kind, &i.Subject, &i.Body, &created, &updated, &i.Read, &i.Resolved, &i.ResolvedBy, &i.ResolutionNote)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrHeraBoardNotFound
	}
	if err != nil {
		return nil, err
	}
	i.CreatedAt = parseTime(created)
	i.UpdatedAt = parseTime(updated)
	return &i, nil
}

func (d *DB) PostHeraBoardItem(roleID int64, kind, subject, body string) (*HeraBoardItem, error) {
	if !validBoardContent(kind, subject, body) {
		return nil, ErrHeraBoardInvalid
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	now := formatTime(time.Now())
	res, err := d.conn.Exec(`INSERT INTO hera_board_items(role_id,kind,subject,body,created_at,updated_at)
 SELECT id,?,?,?,?,? FROM hera_roles WHERE id=? AND kind='coordinator' AND archived_at IS NULL`, kind, subject, body, now, now, roleID)
	if err != nil {
		return nil, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, ErrHeraBoardInvalid
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	uxlog.Log("[board] posted role=%d item=%d kind=%s", roleID, id, kind)
	return scanBoard(d.conn.QueryRow(`SELECT `+boardColumns+` FROM hera_board_items WHERE id=?`, id))
}

func (d *DB) UpdateHeraBoardItem(roleID, id int64, kind, subject, body string) (*HeraBoardItem, error) {
	if !validBoardContent(kind, subject, body) {
		return nil, ErrHeraBoardInvalid
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	res, err := d.conn.Exec(`UPDATE hera_board_items SET
 is_read=CASE WHEN kind<>? OR subject<>? OR body<>? THEN 0 ELSE is_read END,
 updated_at=CASE WHEN kind<>? OR subject<>? OR body<>? THEN ? ELSE updated_at END,
 kind=?,subject=?,body=? WHERE role_id=? AND id=? AND resolved=0`, kind, subject, body, kind, subject, body, formatTime(time.Now()), kind, subject, body, roleID, id)
	if err != nil {
		return nil, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, ErrHeraBoardNotFound
	}
	uxlog.Log("[board] updated role=%d item=%d", roleID, id)
	return scanBoard(d.conn.QueryRow(`SELECT `+boardColumns+` FROM hera_board_items WHERE role_id=? AND id=?`, roleID, id))
}

func (d *DB) ListHeraBoardItems(roleID int64, includeResolved bool) ([]*HeraBoardItem, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	rows, err := d.conn.Query(`SELECT `+boardColumns+` FROM hera_board_items WHERE role_id=? AND (? OR resolved=0) ORDER BY id`, roleID, includeResolved)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]*HeraBoardItem, 0)
	for rows.Next() {
		i, err := scanBoard(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

func (d *DB) ReadHeraBoardItem(roleID, id int64) error {
	return d.readHeraBoardItem(roleID, id, "")
}

// ReadHeraBoardItemAt acknowledges only the revision the human expanded. A
// material edit racing the async acknowledgment must remain unread.
func (d *DB) ReadHeraBoardItemAt(roleID, id int64, updatedAt time.Time) error {
	return d.readHeraBoardItem(roleID, id, formatTime(updatedAt))
}

func (d *DB) readHeraBoardItem(roleID, id int64, revision string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	res, err := d.conn.Exec(`UPDATE hera_board_items SET is_read=1 WHERE role_id=? AND id=? AND resolved=0 AND (?='' OR updated_at=?)`, roleID, id, revision, revision)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		if revision != "" {
			uxlog.Log("[board] read skipped stale revision role=%d item=%d", roleID, id)
			return nil
		}
		return ErrHeraBoardNotFound
	}
	uxlog.Log("[board] read role=%d item=%d", roleID, id)
	return nil
}

// ResolveHeraBoardItem atomically records human decision dismissal in the
// coordinator inbox. This system notice bypasses agent self-send/rate caps;
// it is durable even when no coordinator session is currently bound.
func (d *DB) ResolveHeraBoardItem(roleID, id int64, by, note string) error {
	if (by != "agent" && by != "human") || len(note) > 64*1024 {
		return ErrHeraBoardInvalid
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	tx, err := d.conn.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	i, err := scanBoard(tx.QueryRow(`SELECT `+boardColumns+` FROM hera_board_items WHERE role_id=? AND id=?`, roleID, id))
	if err != nil {
		return err
	}
	if i.Resolved {
		return nil
	}
	now := formatTime(time.Now())
	_, err = tx.Exec(`UPDATE hera_board_items SET resolved=1,resolved_by=?,resolution_note=?,updated_at=? WHERE id=?`, by, note, now, id)
	if err != nil {
		return err
	}
	if by == "human" && i.Kind == "decision" {
		body := fmt.Sprintf("Aaron dropped board decision #%d: %s. Do not act on a stale assumption. Follow up on the board if needed.", id, i.Subject)
		if note != "" {
			body += "\n\n" + note
		}
		_, err = tx.Exec(`INSERT INTO hera_messages(from_role_id,to_role_id,body,tldr,sent_at,delivery_mode) VALUES(?,?,?,?,?,?)`, roleID, roleID, body, fmt.Sprintf("Aaron dropped board decision #%d", id), now, HeraDeliveryPending)
		if err != nil {
			return err
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	uxlog.Log("[board] resolved role=%d item=%d by=%s", roleID, id, by)
	return nil
}

// PendingHeraBoardNotices is the durable board outbox. Leave unbound notices
// pending until a binding exists, and never deliver already-read mail.
func (d *DB) PendingHeraBoardNotices() ([]*HeraMessage, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	rows, err := d.conn.Query(`SELECT id, from_role_id, to_role_id, body, tldr, in_reply_to, sent_at, read_at, delivery_mode, delivered_at
 FROM hera_messages WHERE from_role_id=to_role_id AND read_at IS NULL
 AND delivery_mode IN ('pending','queued_no_binding')
 AND EXISTS (SELECT 1 FROM hera_bindings WHERE role_id=to_role_id AND ended_at IS NULL)
 ORDER BY id`)
	if err != nil {
		return nil, err
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

// HeraBoardUnread returns a read-only band summary. Some lightweight callers
// have partial schemas predating the board; treat only a missing table as empty.
func (d *DB) HeraBoardUnread(roleID int64) (int, string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	var count int
	var subject string
	err := d.conn.QueryRow(`SELECT COUNT(*), COALESCE((SELECT subject FROM hera_board_items
 WHERE role_id=? AND resolved=0 AND is_read=0 ORDER BY updated_at DESC,id DESC LIMIT 1),'')
 FROM hera_board_items WHERE role_id=? AND resolved=0 AND is_read=0`, roleID, roleID).Scan(&count, &subject)
	if err != nil && strings.Contains(err.Error(), "no such table: hera_board_items") {
		uxlog.Log("[board] band summary skipped: board table absent")
		return 0, "", nil
	}
	return count, subject, err
}
