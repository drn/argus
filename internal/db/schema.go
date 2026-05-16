package db

import "fmt"

func (d *DB) createTables() error {
	ddl := `
		CREATE TABLE IF NOT EXISTS schema_version (
			version INTEGER NOT NULL
		);
		CREATE TABLE IF NOT EXISTS tasks (
			id          TEXT PRIMARY KEY,
			name        TEXT NOT NULL,
			status      TEXT NOT NULL DEFAULT 'pending',
			project     TEXT NOT NULL DEFAULT '',
			branch      TEXT NOT NULL DEFAULT '',
			prompt      TEXT NOT NULL DEFAULT '',
			backend     TEXT NOT NULL DEFAULT '',
			worktree    TEXT NOT NULL DEFAULT '',
			agent_pid   INTEGER NOT NULL DEFAULT 0,
			session_id  TEXT NOT NULL DEFAULT '',
			sandboxed   INTEGER NOT NULL DEFAULT 0,
			archived    INTEGER NOT NULL DEFAULT 0,
			pinned      INTEGER NOT NULL DEFAULT 0,
			base_branch TEXT NOT NULL DEFAULT '',
			depends_on  TEXT NOT NULL DEFAULT '',
			result      TEXT NOT NULL DEFAULT '',
			plan_slug   TEXT NOT NULL DEFAULT '',
			created_at  TEXT NOT NULL,
			started_at  TEXT NOT NULL DEFAULT '',
			ended_at    TEXT NOT NULL DEFAULT ''
		);
		CREATE TABLE IF NOT EXISTS projects (
			name                        TEXT PRIMARY KEY,
			path                        TEXT NOT NULL,
			branch                      TEXT NOT NULL DEFAULT '',
			backend                     TEXT NOT NULL DEFAULT '',
			sandbox_enabled             TEXT NOT NULL DEFAULT '',
			sandbox_deny_read           TEXT NOT NULL DEFAULT '',
			sandbox_extra_write         TEXT NOT NULL DEFAULT '',
			sandbox_allow_apple_events  TEXT NOT NULL DEFAULT ''
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

	// Idempotent ALTER TABLE migrations below. All are safe to call multiple
	// times (errors for already-existing columns are silently ignored), so
	// ordering within this block does not matter — new columns can be appended
	// anywhere. Add per-project sandbox columns to existing databases.
	for _, def := range []string{
		"sandbox_enabled            TEXT NOT NULL DEFAULT ''",
		"sandbox_deny_read          TEXT NOT NULL DEFAULT ''",
		"sandbox_extra_write        TEXT NOT NULL DEFAULT ''",
		"sandbox_allow_apple_events TEXT NOT NULL DEFAULT ''",
	} {
		d.conn.Exec(`ALTER TABLE projects ADD COLUMN ` + def) //nolint:errcheck
	}

	// Add archived column to existing tasks tables.
	d.conn.Exec(`ALTER TABLE tasks ADD COLUMN archived INTEGER NOT NULL DEFAULT 0`) //nolint:errcheck

	// Add pinned column to existing tasks tables.
	d.conn.Exec(`ALTER TABLE tasks ADD COLUMN pinned INTEGER NOT NULL DEFAULT 0`) //nolint:errcheck

	// Add sandboxed column to existing tasks tables.
	d.conn.Exec(`ALTER TABLE tasks ADD COLUMN sandboxed INTEGER NOT NULL DEFAULT 0`) //nolint:errcheck

	// Orchestration columns for stacked-PR / DAG workflows: base_branch
	// records the start point so the worktree's history can be inspected
	// without re-deriving it; depends_on holds a JSON array of task IDs that
	// must reach status=complete before this task's session is started; result
	// holds an opaque JSON blob the agent writes via task_set_result for the
	// orchestrator to read. All three are idempotent ADDs.
	d.conn.Exec(`ALTER TABLE tasks ADD COLUMN base_branch TEXT NOT NULL DEFAULT ''`) //nolint:errcheck
	d.conn.Exec(`ALTER TABLE tasks ADD COLUMN depends_on  TEXT NOT NULL DEFAULT ''`) //nolint:errcheck
	d.conn.Exec(`ALTER TABLE tasks ADD COLUMN result      TEXT NOT NULL DEFAULT ''`) //nolint:errcheck

	// plan_slug groups tasks belonging to the same orchestrator stack. Like
	// result, it's opaque to the daemon: the orchestrator sets it on every
	// sub-task it creates, and the DAG view uses it as a filter so multiple
	// stacks within a project render as separate graphs rather than one big
	// disconnected blob.
	d.conn.Exec(`ALTER TABLE tasks ADD COLUMN plan_slug TEXT NOT NULL DEFAULT ''`) //nolint:errcheck

	// Index for FindByNameProject (task_create idempotency check inside
	// createMu). The query filters by all three columns; SQLite uses a
	// partial-prefix on (name, project) and tests archived in-memory if
	// the prefix is selective enough. At Argus's scale a full scan would
	// be fine, but the comment in mcp.TaskStore.FindByNameProject claims
	// "indexed SQL query" — this is what makes that claim true.
	d.conn.Exec(`CREATE INDEX IF NOT EXISTS idx_tasks_name_project ON tasks(name, project, archived)`) //nolint:errcheck

	// Drop legacy columns and config from removed features. SQLite supports
	// DROP COLUMN since 3.35; the statements are idempotent and safe on fresh
	// databases (where the columns/rows never existed).
	d.conn.Exec(`DROP INDEX IF EXISTS idx_tasks_todo_path`)              //nolint:errcheck
	d.conn.Exec(`ALTER TABLE tasks DROP COLUMN todo_path`)               //nolint:errcheck
	d.conn.Exec(`ALTER TABLE tasks DROP COLUMN pr_url`)                  //nolint:errcheck
	d.conn.Exec(`ALTER TABLE tasks DROP COLUMN waiting_review`)          //nolint:errcheck
	d.conn.Exec(`DELETE FROM config WHERE key='defaults.review_prompt'`) //nolint:errcheck

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

	// Push subscriptions for Web Push notifications. One row per registered
	// device. Stored as JSON because the W3C subscription shape is opaque.
	if _, err := d.conn.Exec(`
		CREATE TABLE IF NOT EXISTS push_subscriptions (
			id          INTEGER PRIMARY KEY AUTOINCREMENT,
			label       TEXT NOT NULL DEFAULT '',
			endpoint    TEXT NOT NULL UNIQUE,
			p256dh      TEXT NOT NULL,
			auth_key    TEXT NOT NULL,
			created_at  INTEGER NOT NULL
		)
	`); err != nil {
		return fmt.Errorf("creating push_subscriptions table: %w", err)
	}

	// Per-device API tokens (Phase 6). Master token in ~/.argus/api-token still
	// works as admin and is the only credential that can mint new tokens.
	if _, err := d.conn.Exec(`
		CREATE TABLE IF NOT EXISTS api_tokens (
			id          INTEGER PRIMARY KEY AUTOINCREMENT,
			label       TEXT NOT NULL DEFAULT '',
			hash        TEXT NOT NULL UNIQUE,
			last4       TEXT NOT NULL DEFAULT '',
			created_at  INTEGER NOT NULL,
			last_used   INTEGER NOT NULL DEFAULT 0,
			revoked_at  INTEGER NOT NULL DEFAULT 0
		)
	`); err != nil {
		return fmt.Errorf("creating api_tokens table: %w", err)
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

	// Scheduled tasks: cron-like definitions that fire a fresh task at each
	// match. last_run_at, next_run_at, last_task_id, last_error are populated
	// by the scheduler service in internal/scheduler.
	if _, err := d.conn.Exec(`
		CREATE TABLE IF NOT EXISTS scheduled_tasks (
			id           TEXT PRIMARY KEY,
			name         TEXT NOT NULL,
			project      TEXT NOT NULL,
			prompt       TEXT NOT NULL,
			backend      TEXT NOT NULL DEFAULT '',
			schedule     TEXT NOT NULL DEFAULT '',
			run_once_at  TEXT NOT NULL DEFAULT '',
			enabled      INTEGER NOT NULL DEFAULT 1,
			created_at   TEXT NOT NULL,
			last_run_at  TEXT NOT NULL DEFAULT '',
			next_run_at  TEXT NOT NULL DEFAULT '',
			last_task_id TEXT NOT NULL DEFAULT '',
			last_error   TEXT NOT NULL DEFAULT ''
		)
	`); err != nil {
		return fmt.Errorf("creating scheduled_tasks table: %w", err)
	}

	// Add run_once_at column to existing scheduled_tasks tables. Idempotent.
	d.conn.Exec(`ALTER TABLE scheduled_tasks ADD COLUMN run_once_at TEXT NOT NULL DEFAULT ''`) //nolint:errcheck

	// Inter-task messaging. One row per peer-to-peer message; read state is
	// per-recipient via read_at. kind is documentation for receiving agents
	// and the task_ask polling loop — the daemon does not enforce
	// conversation semantics. See internal/db/messages.go.
	if _, err := d.conn.Exec(`
		CREATE TABLE IF NOT EXISTS task_messages (
			id            TEXT PRIMARY KEY,
			from_task_id  TEXT NOT NULL,
			to_task_id    TEXT NOT NULL,
			kind          TEXT NOT NULL DEFAULT 'note',
			body          TEXT NOT NULL,
			in_reply_to   TEXT NOT NULL DEFAULT '',
			created_at    TEXT NOT NULL,
			read_at       TEXT NOT NULL DEFAULT ''
		)
	`); err != nil {
		return fmt.Errorf("creating task_messages table: %w", err)
	}
	d.conn.Exec(`CREATE INDEX IF NOT EXISTS idx_msg_to_unread   ON task_messages(to_task_id, read_at)`)       //nolint:errcheck
	d.conn.Exec(`CREATE INDEX IF NOT EXISTS idx_msg_in_reply_to ON task_messages(in_reply_to)`)               //nolint:errcheck
	d.conn.Exec(`CREATE INDEX IF NOT EXISTS idx_msg_from_created ON task_messages(from_task_id, created_at)`) //nolint:errcheck

	return nil
}
