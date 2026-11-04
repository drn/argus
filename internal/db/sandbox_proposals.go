package db

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Sandbox proposal statuses.
const (
	SandboxProposalPending  = "pending"
	SandboxProposalApproved = "approved"
	SandboxProposalRejected = "rejected"
)

// MaxPendingSandboxProposalsPerTask bounds how many distinct pending
// proposals one task may hold.
const MaxPendingSandboxProposalsPerTask = 10

var (
	ErrSandboxProposalNotFound = errors.New("sandbox proposal not found")
	ErrSandboxProposalDecided  = errors.New("sandbox proposal already decided")
	ErrSandboxProposalCap      = errors.New("too many pending sandbox proposals for this task")
)

// SandboxProposalEntry is one requested path with the agent's reason and any
// sensitivity warning computed at proposal time.
type SandboxProposalEntry struct {
	Path    string `json:"path"`
	Reason  string `json:"reason"`
	Warning string `json:"warning,omitempty"`
}

// SandboxProposal is a pending/decided agent request for extra write paths.
type SandboxProposal struct {
	ID           string                 `json:"id"`
	TaskID       string                 `json:"task_id"`
	Project      string                 `json:"project"`
	Entries      []SandboxProposalEntry `json:"entries"`
	Status       string                 `json:"status"`
	Scope        string                 `json:"scope,omitempty"`
	AppliedPaths []string               `json:"applied_paths,omitempty"`
	CreatedAt    time.Time              `json:"created_at"`
	DecidedAt    *time.Time             `json:"decided_at,omitempty"`
	DecidedBy    string                 `json:"decided_by,omitempty"`
}

func pathKey(entries []SandboxProposalEntry) string {
	ps := make([]string, len(entries))
	for i, e := range entries {
		ps[i] = e.Path
	}
	sort.Strings(ps)
	return strings.Join(ps, "\x00")
}

// AddSandboxProposal records a pending proposal. A pending proposal from the
// same task with an identical path set is returned instead (merged=true).
func (d *DB) AddSandboxProposal(taskID, project string, entries []SandboxProposalEntry) (*SandboxProposal, bool, error) {
	pending, err := d.ListSandboxProposals(SandboxProposalPending)
	if err != nil {
		return nil, false, err
	}
	n := 0
	for _, p := range pending {
		if p.TaskID != taskID {
			continue
		}
		if pathKey(p.Entries) == pathKey(entries) {
			return p, true, nil
		}
		n++
	}
	if n >= MaxPendingSandboxProposalsPerTask {
		return nil, false, ErrSandboxProposalCap
	}
	raw, err := json.Marshal(entries)
	if err != nil {
		return nil, false, err
	}
	p := &SandboxProposal{ID: generateID(), TaskID: taskID, Project: project, Entries: entries, Status: SandboxProposalPending, CreatedAt: time.Now().UTC()}
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, err := d.conn.Exec(`INSERT INTO sandbox_proposals (id, task_id, project, entries, status, created_at) VALUES (?, ?, ?, ?, 'pending', ?)`,
		p.ID, taskID, project, string(raw), p.CreatedAt.Format(time.RFC3339Nano)); err != nil {
		return nil, false, err
	}
	return p, false, nil
}

const sandboxProposalCols = `id, task_id, project, entries, status, scope, applied_paths, created_at, decided_at, decided_by`

func scanSandboxProposal(r rowScanner) (*SandboxProposal, error) {
	var p SandboxProposal
	var entries, applied, created, decided string
	if err := r.Scan(&p.ID, &p.TaskID, &p.Project, &entries, &p.Status, &p.Scope, &applied, &created, &decided, &p.DecidedBy); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(entries), &p.Entries); err != nil {
		return nil, fmt.Errorf("decoding proposal entries: %w", err)
	}
	json.Unmarshal([]byte(applied), &p.AppliedPaths) //nolint:errcheck
	p.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	if decided != "" {
		if t, err := time.Parse(time.RFC3339Nano, decided); err == nil {
			p.DecidedAt = &t
		}
	}
	return &p, nil
}

// ListSandboxProposals returns proposals oldest-first, optionally filtered by status.
func (d *DB) ListSandboxProposals(status string) ([]*SandboxProposal, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	q := `SELECT ` + sandboxProposalCols + ` FROM sandbox_proposals`
	var args []any
	if status != "" {
		q += ` WHERE status = ?`
		args = append(args, status)
	}
	q += ` ORDER BY created_at`
	rows, err := d.conn.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*SandboxProposal{}
	for rows.Next() {
		p, err := scanSandboxProposal(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// GetSandboxProposal returns one proposal or ErrSandboxProposalNotFound.
func (d *DB) GetSandboxProposal(id string) (*SandboxProposal, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	p, err := scanSandboxProposal(d.conn.QueryRow(`SELECT `+sandboxProposalCols+` FROM sandbox_proposals WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSandboxProposalNotFound
	}
	return p, err
}

// DecideSandboxProposal atomically moves a pending proposal to approved or
// rejected (compare-and-set). It does NOT touch sandbox config.
func (d *DB) DecideSandboxProposal(id, status, scope string, applied []string, by string) error {
	raw, _ := json.Marshal(applied)
	if applied == nil {
		raw = []byte("[]")
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	res, err := d.conn.Exec(`UPDATE sandbox_proposals SET status=?, scope=?, applied_paths=?, decided_at=?, decided_by=? WHERE id=? AND status='pending'`,
		status, scope, string(raw), time.Now().UTC().Format(time.RFC3339Nano), by, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		var exists int
		if d.conn.QueryRow(`SELECT 1 FROM sandbox_proposals WHERE id=?`, id).Scan(&exists) != nil {
			return ErrSandboxProposalNotFound
		}
		return ErrSandboxProposalDecided
	}
	return nil
}

// RevertSandboxProposal returns a decided proposal to pending (used when
// applying the approved paths fails after the compare-and-set).
func (d *DB) RevertSandboxProposal(id string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, err := d.conn.Exec(`UPDATE sandbox_proposals SET status='pending', scope='', applied_paths='[]', decided_at='', decided_by='' WHERE id=?`, id)
	return err
}
