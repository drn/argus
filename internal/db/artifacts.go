package db

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/drn/argus/internal/model"
)

// UpsertArtifact inserts or replaces the manifest row for (task_id, filename).
// Re-registering the same filename overwrites name/type/size/created_at (last
// write wins) so a skill that regenerates a report does not accumulate
// duplicate rows. Returns the canonical stored row (the row's stable id is
// re-read on conflict, since ON CONFLICT keeps the original id).
func (d *DB) UpsertArtifact(a *model.Artifact) (*model.Artifact, error) {
	if a.TaskID == "" {
		return nil, errors.New("task_id is required")
	}
	if a.Filename == "" {
		return nil, errors.New("filename is required")
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	if a.ID == "" {
		a.ID = generateID()
	}
	if a.CreatedAt.IsZero() {
		a.CreatedAt = time.Now()
	}
	if a.Name == "" {
		a.Name = a.Filename
	}
	if a.Type == "" {
		a.Type = model.ArtifactText
	}

	_, err := d.conn.Exec(
		`INSERT INTO artifacts (id, task_id, name, filename, type, size, created_at, folder)
		 VALUES (?,?,?,?,?,?,?,?)
		 ON CONFLICT(task_id, filename) DO UPDATE SET
		   name=excluded.name, type=excluded.type, size=excluded.size, created_at=excluded.created_at, folder=excluded.folder`,
		a.ID, a.TaskID, a.Name, a.Filename, string(a.Type), a.Size, formatTime(a.CreatedAt), a.Folder,
	)
	if err != nil {
		return nil, fmt.Errorf("upsert artifact: %w", err)
	}

	// Re-read so the returned id is the canonical stored value: on a conflict
	// the row keeps its original id and the freshly-generated one above is
	// discarded by SQLite.
	got, err := d.getArtifactLocked(a.TaskID, a.Filename)
	if err != nil {
		return nil, err
	}
	return got, nil
}

// Artifacts returns the registered artifacts for a task, newest first.
func (d *DB) Artifacts(taskID string) ([]*model.Artifact, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	rows, err := d.conn.Query(
		`SELECT id, task_id, name, filename, type, size, created_at, folder FROM artifacts WHERE task_id=? ORDER BY created_at DESC, id DESC`,
		taskID,
	)
	if err != nil {
		return nil, fmt.Errorf("query artifacts: %w", err)
	}
	defer rows.Close()

	var out []*model.Artifact
	for rows.Next() {
		a, err := scanArtifact(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, nil
}

// GetArtifact returns the manifest row for (taskID, filename) — where filename
// is the on-disk basename, NOT the display Name — or (nil, nil) when no such
// artifact is registered. The serving path uses this as the allowlist: no row
// → 404, regardless of what is physically on disk.
func (d *DB) GetArtifact(taskID, filename string) (*model.Artifact, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.getArtifactLocked(taskID, filename)
}

// getArtifactLocked is the lock-free body shared by GetArtifact and
// UpsertArtifact. Caller MUST hold d.mu.
func (d *DB) getArtifactLocked(taskID, filename string) (*model.Artifact, error) {
	row := d.conn.QueryRow(
		`SELECT id, task_id, name, filename, type, size, created_at, folder FROM artifacts WHERE task_id=? AND filename=?`,
		taskID, filename,
	)
	a, err := scanArtifact(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return a, nil
}

// PruneFolderArtifacts deletes the manifest rows of a folder whose filename is
// not in keep, returning the removed filenames so the caller can delete their
// bytes. Used when a folder is re-registered so files dropped from the source
// directory do not linger.
func (d *DB) PruneFolderArtifacts(taskID, folder string, keep []string) ([]string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	keepSet := make(map[string]bool, len(keep))
	for _, k := range keep {
		keepSet[k] = true
	}
	rows, err := d.conn.Query(`SELECT filename FROM artifacts WHERE task_id=? AND folder=?`, taskID, folder)
	if err != nil {
		return nil, fmt.Errorf("query folder artifacts: %w", err)
	}
	var stale []string
	for rows.Next() {
		var fn string
		if err := rows.Scan(&fn); err != nil {
			rows.Close()
			return nil, err
		}
		if !keepSet[fn] {
			stale = append(stale, fn)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("query folder artifacts: %w", err)
	}
	for _, fn := range stale {
		if _, err := d.conn.Exec(`DELETE FROM artifacts WHERE task_id=? AND filename=?`, taskID, fn); err != nil {
			return nil, fmt.Errorf("prune folder artifact: %w", err)
		}
	}
	return stale, nil
}

// DeleteArtifactsForTask removes every manifest row for taskID. Called when a
// task is deleted so stale rows don't linger after the on-disk artifact dir is
// removed. Returns the row count for logging.
func (d *DB) DeleteArtifactsForTask(taskID string) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	res, err := d.conn.Exec(`DELETE FROM artifacts WHERE task_id=?`, taskID)
	if err != nil {
		return 0, fmt.Errorf("delete artifacts for task: %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// scanArtifact reads an Artifact from a row using the canonical column order
// shared by Artifacts and getArtifactLocked.
func scanArtifact(row scanner) (*model.Artifact, error) {
	a := &model.Artifact{}
	var typ, createdAt string
	if err := row.Scan(&a.ID, &a.TaskID, &a.Name, &a.Filename, &typ, &a.Size, &createdAt, &a.Folder); err != nil {
		return nil, err
	}
	a.Type = model.ArtifactType(typ)
	a.CreatedAt = parseTime(createdAt)
	return a, nil
}
