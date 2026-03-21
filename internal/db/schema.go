package db

import "fmt"

func (d *DB) createTables() error {
	ddl := `
		CREATE TABLE IF NOT EXISTS schema_version (
			version INTEGER NOT NULL
		);
		CREATE TABLE IF NOT EXISTS tasks (
			id         TEXT PRIMARY KEY,
			name       TEXT NOT NULL,
			status     TEXT NOT NULL DEFAULT 'pending',
			project    TEXT NOT NULL DEFAULT '',
			branch     TEXT NOT NULL DEFAULT '',
			prompt     TEXT NOT NULL DEFAULT '',
			backend    TEXT NOT NULL DEFAULT '',
			worktree   TEXT NOT NULL DEFAULT '',
			agent_pid  INTEGER NOT NULL DEFAULT 0,
			session_id TEXT NOT NULL DEFAULT '',
			pr_url     TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			started_at TEXT NOT NULL DEFAULT '',
			ended_at   TEXT NOT NULL DEFAULT ''
		);
		CREATE TABLE IF NOT EXISTS projects (
			name                TEXT PRIMARY KEY,
			path                TEXT NOT NULL,
			branch              TEXT NOT NULL DEFAULT '',
			backend             TEXT NOT NULL DEFAULT '',
			sandbox_enabled     TEXT NOT NULL DEFAULT '',
			sandbox_deny_read   TEXT NOT NULL DEFAULT '',
			sandbox_extra_write TEXT NOT NULL DEFAULT ''
		);
		CREATE TABLE IF NOT EXISTS backends (
			name           TEXT PRIMARY KEY,
			command        TEXT NOT NULL,
			prompt_flag    TEXT NOT NULL DEFAULT '',
			resume_command TEXT NOT NULL DEFAULT ''
		);
		CREATE TABLE IF NOT EXISTS config (
			key   TEXT PRIMARY KEY,
			value TEXT NOT NULL
		);
	`
	if _, err := d.conn.Exec(ddl); err != nil {
		return err
	}

	// Add per-project sandbox columns to existing databases (safe to call multiple times;
	// errors for already-existing columns are silently ignored).
	for _, def := range []string{
		"sandbox_enabled     TEXT NOT NULL DEFAULT ''",
		"sandbox_deny_read   TEXT NOT NULL DEFAULT ''",
		"sandbox_extra_write TEXT NOT NULL DEFAULT ''",
	} {
		d.conn.Exec(`ALTER TABLE projects ADD COLUMN ` + def) //nolint:errcheck
	}

	// Add pr_url column to existing tasks tables.
	d.conn.Exec(`ALTER TABLE tasks ADD COLUMN pr_url TEXT NOT NULL DEFAULT ''`) //nolint:errcheck

	// Add archived column to existing tasks tables.
	d.conn.Exec(`ALTER TABLE tasks ADD COLUMN archived INTEGER NOT NULL DEFAULT 0`) //nolint:errcheck

	// Add todo_path column to existing tasks tables.
	d.conn.Exec(`ALTER TABLE tasks ADD COLUMN todo_path TEXT NOT NULL DEFAULT ''`) //nolint:errcheck
	// Index for TasksByTodoPath queries (called on every tick).
	d.conn.Exec(`CREATE INDEX IF NOT EXISTS idx_tasks_todo_path ON tasks(todo_path)`) //nolint:errcheck

	// Add resume_command column to existing backends tables.
	d.conn.Exec(`ALTER TABLE backends ADD COLUMN resume_command TEXT NOT NULL DEFAULT ''`) //nolint:errcheck

	// KB FTS5 full-text search table (virtual table — CREATE VIRTUAL TABLE).
	// Note: FTS5 doesn't support UPDATE; use DELETE+INSERT in a transaction.
	if _, err := d.conn.Exec(`
		CREATE VIRTUAL TABLE IF NOT EXISTS kb_documents USING fts5(
			path UNINDEXED,
			title,
			body,
			tags,
			tier UNINDEXED,
			tokenize = 'porter unicode61'
		)
	`); err != nil {
		return fmt.Errorf("creating kb_documents fts5 table: %w", err)
	}

	// KB metadata table for non-text fields not suitable for FTS5.
	if _, err := d.conn.Exec(`
		CREATE TABLE IF NOT EXISTS kb_metadata (
			path        TEXT PRIMARY KEY,
			modified_at INTEGER NOT NULL,
			ingested_at INTEGER NOT NULL,
			word_count  INTEGER NOT NULL DEFAULT 0,
			tier        TEXT NOT NULL DEFAULT 'hot'
		)
	`); err != nil {
		return fmt.Errorf("creating kb_metadata table: %w", err)
	}

	// KB pending tasks table for vault task imports awaiting approval.
	if _, err := d.conn.Exec(`
		CREATE TABLE IF NOT EXISTS kb_pending_tasks (
			id          INTEGER PRIMARY KEY AUTOINCREMENT,
			name        TEXT NOT NULL,
			project     TEXT NOT NULL DEFAULT '',
			source_file TEXT NOT NULL,
			created_at  TEXT NOT NULL,
			UNIQUE(source_file, name)
		)
	`); err != nil {
		return fmt.Errorf("creating kb_pending_tasks table: %w", err)
	}

	return nil
}
