package db

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/drn/argus/internal/kb"
)

// KBUpsert inserts or replaces a document in the FTS5 knowledge base.
// FTS5 doesn't support UPDATE, so we DELETE then INSERT in a transaction.
func (d *DB) KBUpsert(doc *kb.Document) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	tx, err := d.conn.Begin()
	if err != nil {
		return fmt.Errorf("kb upsert begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	// Delete existing FTS5 row (no-op if absent).
	if _, err := tx.Exec(`DELETE FROM kb_documents WHERE path = ?`, doc.Path); err != nil {
		return fmt.Errorf("kb upsert delete: %w", err)
	}

	// Insert new FTS5 row.
	tagsStr := strings.Join(doc.Tags, " ")
	if _, err := tx.Exec(
		`INSERT INTO kb_documents (path, title, body, tags, tier) VALUES (?, ?, ?, ?, ?)`,
		doc.Path, doc.Title, doc.Body, tagsStr, doc.Tier,
	); err != nil {
		return fmt.Errorf("kb upsert insert: %w", err)
	}

	// Upsert metadata.
	if _, err := tx.Exec(`
		INSERT INTO kb_metadata (path, modified_at, ingested_at, word_count, tier)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(path) DO UPDATE SET
			modified_at = excluded.modified_at,
			ingested_at = excluded.ingested_at,
			word_count  = excluded.word_count,
			tier        = excluded.tier
	`,
		doc.Path,
		doc.ModifiedAt.Unix(),
		doc.IngestedAt.Unix(),
		doc.WordCount,
		doc.Tier,
	); err != nil {
		return fmt.Errorf("kb upsert metadata: %w", err)
	}

	return tx.Commit()
}

// KBDelete removes a document from the knowledge base by its vault-relative path.
func (d *DB) KBDelete(path string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	tx, err := d.conn.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck

	if _, err := tx.Exec(`DELETE FROM kb_documents WHERE path = ?`, path); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM kb_metadata WHERE path = ?`, path); err != nil {
		return err
	}
	return tx.Commit()
}

// KBSearch performs an FTS5 full-text search and returns ranked results.
func (d *DB) KBSearch(query string, limit int) ([]kb.SearchResult, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if limit <= 0 {
		limit = 10
	}

	rows, err := d.conn.Query(`
		SELECT
			kb_documents.path,
			kb_documents.title,
			kb_documents.tier,
			snippet(kb_documents, 2, '[', ']', '...', 20) AS snippet,
			kb_documents.rank,
			COALESCE(km.modified_at, 0),
			COALESCE(km.ingested_at, 0),
			COALESCE(km.word_count, 0)
		FROM kb_documents
		LEFT JOIN kb_metadata km ON km.path = kb_documents.path
		WHERE kb_documents MATCH ?
		ORDER BY rank
		LIMIT ?
	`, query, limit)
	if err != nil {
		return nil, fmt.Errorf("kb search: %w", err)
	}
	defer rows.Close()

	var results []kb.SearchResult
	for rows.Next() {
		var r kb.SearchResult
		var modAt, ingAt int64
		if err := rows.Scan(&r.Path, &r.Title, &r.Tier, &r.Snippet, &r.Rank, &modAt, &ingAt, &r.WordCount); err != nil {
			continue
		}
		r.ModifiedAt = time.Unix(modAt, 0)
		r.IngestedAt = time.Unix(ingAt, 0)
		results = append(results, r)
	}
	return results, nil
}

// KBList returns documents whose path has the given prefix (or all documents if prefix is "").
func (d *DB) KBList(prefix string, limit int) ([]kb.Document, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if limit <= 0 {
		limit = 100
	}

	var rows *sql.Rows
	var err error
	if prefix == "" {
		rows, err = d.conn.Query(`
			SELECT kd.path, kd.title, kd.tier, kd.tags,
			       km.modified_at, km.ingested_at, km.word_count
			FROM kb_documents kd
			LEFT JOIN kb_metadata km ON km.path = kd.path
			ORDER BY kd.path
			LIMIT ?
		`, limit)
	} else {
		rows, err = d.conn.Query(`
			SELECT kd.path, kd.title, kd.tier, kd.tags,
			       km.modified_at, km.ingested_at, km.word_count
			FROM kb_documents kd
			LEFT JOIN kb_metadata km ON km.path = kd.path
			WHERE kd.path LIKE ?
			ORDER BY kd.path
			LIMIT ?
		`, prefix+"%", limit)
	}
	if err != nil {
		return nil, fmt.Errorf("kb list: %w", err)
	}
	defer rows.Close()

	var docs []kb.Document
	for rows.Next() {
		var doc kb.Document
		var tagsStr string
		var modAt, ingAt int64
		if err := rows.Scan(&doc.Path, &doc.Title, &doc.Tier, &tagsStr, &modAt, &ingAt, &doc.WordCount); err != nil {
			continue
		}
		if tagsStr != "" {
			doc.Tags = strings.Fields(tagsStr)
		}
		doc.ModifiedAt = time.Unix(modAt, 0)
		doc.IngestedAt = time.Unix(ingAt, 0)
		docs = append(docs, doc)
	}
	return docs, nil
}

// KBGet retrieves a single document by its vault-relative path.
func (d *DB) KBGet(path string) (*kb.Document, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	var doc kb.Document
	var tagsStr string
	var modAt, ingAt int64
	err := d.conn.QueryRow(`
		SELECT kd.path, kd.title, kd.body, kd.tier, kd.tags,
		       km.modified_at, km.ingested_at, km.word_count
		FROM kb_documents kd
		LEFT JOIN kb_metadata km ON km.path = kd.path
		WHERE kd.path = ?
	`, path).Scan(&doc.Path, &doc.Title, &doc.Body, &doc.Tier, &tagsStr, &modAt, &ingAt, &doc.WordCount)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("document not found: %s", path)
	}
	if err != nil {
		return nil, fmt.Errorf("kb get: %w", err)
	}
	if tagsStr != "" {
		doc.Tags = strings.Fields(tagsStr)
	}
	doc.ModifiedAt = time.Unix(modAt, 0)
	doc.IngestedAt = time.Unix(ingAt, 0)
	return &doc, nil
}

// KBDocumentCount returns the total number of documents in the knowledge base.
func (d *DB) KBDocumentCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()

	var count int
	d.conn.QueryRow(`SELECT COUNT(*) FROM kb_metadata`).Scan(&count) //nolint:errcheck
	return count
}

// KBPendingTask is a task parsed from the Obsidian vault awaiting approval.
type KBPendingTask struct {
	ID         int
	Name       string
	Project    string
	SourceFile string
	CreatedAt  time.Time
}

// KBAddPendingTask inserts a pending task. Ignores duplicates (same source_file + name).
func (d *DB) KBAddPendingTask(name, project, sourceFile string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	_, err := d.conn.Exec(`
		INSERT OR IGNORE INTO kb_pending_tasks (name, project, source_file, created_at)
		VALUES (?, ?, ?, ?)
	`, name, project, sourceFile, time.Now().Format(time.RFC3339Nano))
	return err
}

// KBPendingTasks returns all pending tasks awaiting approval.
func (d *DB) KBPendingTasks() []KBPendingTask {
	d.mu.Lock()
	defer d.mu.Unlock()

	rows, err := d.conn.Query(`
		SELECT id, name, project, source_file, created_at
		FROM kb_pending_tasks
		ORDER BY created_at ASC
	`)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var tasks []KBPendingTask
	for rows.Next() {
		var t KBPendingTask
		var createdAt string
		if err := rows.Scan(&t.ID, &t.Name, &t.Project, &t.SourceFile, &createdAt); err != nil {
			continue
		}
		t.CreatedAt = parseTime(createdAt)
		tasks = append(tasks, t)
	}
	return tasks
}

// KBDeletePendingTask removes a pending task by ID (called after approval or rejection).
func (d *DB) KBDeletePendingTask(id int) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	_, err := d.conn.Exec(`DELETE FROM kb_pending_tasks WHERE id = ?`, id)
	return err
}
