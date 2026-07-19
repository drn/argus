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
			model       TEXT NOT NULL DEFAULT '',
			worktree    TEXT NOT NULL DEFAULT '',
			agent_pid   INTEGER NOT NULL DEFAULT 0,
			session_id  TEXT NOT NULL DEFAULT '',
			sandboxed   INTEGER NOT NULL DEFAULT 0,
			archived    INTEGER NOT NULL DEFAULT 0,
			pinned      INTEGER NOT NULL DEFAULT 0,
			base_branch TEXT NOT NULL DEFAULT '',
			result      TEXT NOT NULL DEFAULT '',
			archetype   TEXT NOT NULL DEFAULT '',
			profile     TEXT NOT NULL DEFAULT '',
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
			sandbox_allow_apple_events  TEXT NOT NULL DEFAULT '',
			profile                     TEXT NOT NULL DEFAULT ''
		);
		CREATE TABLE IF NOT EXISTS backends (
			name           TEXT PRIMARY KEY,
			command        TEXT NOT NULL,
			prompt_flag    TEXT NOT NULL DEFAULT '',
			resume_command TEXT NOT NULL DEFAULT '',
			model          TEXT NOT NULL DEFAULT '',
			-- env_vars: JSON object mapping a TARGET env-var name to a SOURCE
			-- descriptor, consulted by agent.BuildCmd. Holds the MAPPING ONLY,
			-- never a secret value (resolved at spawn time). '' / 'null' both
			-- decode to an empty mapping.
			env_vars       TEXT NOT NULL DEFAULT ''
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
		// profile (add-diligence-profiles): the project→profile-NAME binding
		// (never the profile body). Empty resolves to the "default" profile.
		"profile                    TEXT NOT NULL DEFAULT ''",
	} {
		d.conn.Exec(`ALTER TABLE projects ADD COLUMN ` + def) //nolint:errcheck
	}

	// Add archived column to existing tasks tables.
	d.conn.Exec(`ALTER TABLE tasks ADD COLUMN archived INTEGER NOT NULL DEFAULT 0`) //nolint:errcheck

	// Add pinned column to existing tasks tables.
	d.conn.Exec(`ALTER TABLE tasks ADD COLUMN pinned INTEGER NOT NULL DEFAULT 0`) //nolint:errcheck

	// Add sandboxed column to existing tasks tables.
	d.conn.Exec(`ALTER TABLE tasks ADD COLUMN sandboxed INTEGER NOT NULL DEFAULT 0`) //nolint:errcheck

	// Orchestration columns: base_branch records the worktree's start point so
	// its history can be inspected without re-deriving it (stacked-PR workflows);
	// result holds an opaque JSON blob the agent writes via task_set_result for a
	// coordinator to read. Both are idempotent ADDs.
	d.conn.Exec(`ALTER TABLE tasks ADD COLUMN base_branch TEXT NOT NULL DEFAULT ''`) //nolint:errcheck
	d.conn.Exec(`ALTER TABLE tasks ADD COLUMN result      TEXT NOT NULL DEFAULT ''`) //nolint:errcheck

	// archetype (add-diligence-profiles): the authoritative model-resolution key
	// read by agent.ResolveModel. Idempotent ADD for databases predating it;
	// existing rows read empty (no archetype → no profile consulted).
	d.conn.Exec(`ALTER TABLE tasks ADD COLUMN archetype TEXT NOT NULL DEFAULT ''`) //nolint:errcheck

	// profile (add-diligence-profiles): the per-spawn profile override. Non-empty
	// means the operator selected a specific profile for this one spawn, overriding
	// the project's bound profile during model resolution. Empty = use project binding.
	d.conn.Exec(`ALTER TABLE tasks ADD COLUMN profile TEXT NOT NULL DEFAULT ''`) //nolint:errcheck

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

	// Retired with the legacy depends_on DAG (Hera orchestration replaced it):
	// drop the two columns from existing databases. Idempotent and safe on fresh
	// DBs (the columns were never created there).
	d.conn.Exec(`ALTER TABLE tasks DROP COLUMN depends_on`) //nolint:errcheck
	d.conn.Exec(`ALTER TABLE tasks DROP COLUMN plan_slug`)  //nolint:errcheck

	// Add resume_command column to existing backends tables.
	d.conn.Exec(`ALTER TABLE backends ADD COLUMN resume_command TEXT NOT NULL DEFAULT ''`) //nolint:errcheck

	// Per-backend credential env mapping (add-foreign-backend-envmap). JSON
	// object: TARGET env var -> SOURCE descriptor; mapping only, never a value.
	// Idempotent ADD for databases predating the column.
	d.conn.Exec(`ALTER TABLE backends ADD COLUMN env_vars TEXT NOT NULL DEFAULT ''`) //nolint:errcheck

	// Per-backend default model and per-task model override.
	d.conn.Exec(`ALTER TABLE backends ADD COLUMN model TEXT NOT NULL DEFAULT ''`) //nolint:errcheck
	d.conn.Exec(`ALTER TABLE tasks ADD COLUMN model TEXT NOT NULL DEFAULT ''`)    //nolint:errcheck

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
	// scope: empty for device tokens (the original use case); non-empty for
	// plugin-scoped tokens. The auth middleware tags scoped tokens as
	// `scope:<name>` instead of `device`, and downstream plugin endpoints gate
	// on that tag.
	if _, err := d.conn.Exec(`
		CREATE TABLE IF NOT EXISTS api_tokens (
			id          INTEGER PRIMARY KEY AUTOINCREMENT,
			label       TEXT NOT NULL DEFAULT '',
			hash        TEXT NOT NULL UNIQUE,
			last4       TEXT NOT NULL DEFAULT '',
			scope       TEXT NOT NULL DEFAULT '',
			created_at  INTEGER NOT NULL,
			last_used   INTEGER NOT NULL DEFAULT 0,
			revoked_at  INTEGER NOT NULL DEFAULT 0
		)
	`); err != nil {
		return fmt.Errorf("creating api_tokens table: %w", err)
	}

	// Idempotent ALTER for databases created before the scope column was added.
	d.conn.Exec(`ALTER TABLE api_tokens ADD COLUMN scope TEXT NOT NULL DEFAULT ''`) //nolint:errcheck

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
			model        TEXT NOT NULL DEFAULT '',
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

	// Per-schedule model override column. Idempotent.
	d.conn.Exec(`ALTER TABLE scheduled_tasks ADD COLUMN model TEXT NOT NULL DEFAULT ''`) //nolint:errcheck

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

	// Session artifacts: files an agent/skill produced (HTML reports, PDFs,
	// rendered markdown, images) and registered for viewing in Argus Web. The
	// bytes live at ~/.argus/artifacts/<task-id>/<filename>; this table is the
	// manifest that SCOPES serving — a row must exist for a file to be served,
	// so a user-supplied name only selects a registered row and never builds a
	// filesystem path directly. One row per (task_id, filename); re-registering
	// the same filename overwrites (last write wins).
	if _, err := d.conn.Exec(`
		CREATE TABLE IF NOT EXISTS artifacts (
			id          TEXT PRIMARY KEY,
			task_id     TEXT NOT NULL,
			name        TEXT NOT NULL,
			filename    TEXT NOT NULL,
			type        TEXT NOT NULL DEFAULT 'text',
			size        INTEGER NOT NULL DEFAULT 0,
			created_at  TEXT NOT NULL,
			UNIQUE(task_id, filename)
		)
	`); err != nil {
		return fmt.Errorf("creating artifacts table: %w", err)
	}
	d.conn.Exec(`CREATE INDEX IF NOT EXISTS idx_artifacts_task ON artifacts(task_id, created_at)`) //nolint:errcheck

	// Per-task sidecar metadata. Composite PK (task_id, namespace, key) keeps
	// each plugin's keys isolated under its own namespace prefix; ON
	// CONFLICT(...) DO UPDATE in SetMeta upserts so a write never has to
	// branch on existence. Cascades wired through Delete / SetArchived rather
	// than via FK so the soft-archive flow can scope cleanup without forcing
	// an ON DELETE CASCADE that wouldn't fire on the soft path.
	if _, err := d.conn.Exec(`
		CREATE TABLE IF NOT EXISTS task_meta (
			task_id     TEXT NOT NULL,
			namespace   TEXT NOT NULL,
			key         TEXT NOT NULL,
			value       TEXT NOT NULL DEFAULT '',
			updated_at  TEXT NOT NULL,
			PRIMARY KEY (task_id, namespace, key)
		)
	`); err != nil {
		return fmt.Errorf("creating task_meta table: %w", err)
	}
	d.conn.Exec(`CREATE INDEX IF NOT EXISTS idx_task_meta_namespace ON task_meta(task_id, namespace)`) //nolint:errcheck

	// Events ring (PR 2 of the plugin substrate). Bounded — InsertEvent
	// evicts the oldest rows once the row count exceeds MaxEventsRetained.
	// id is INTEGER PRIMARY KEY AUTOINCREMENT so the cursor is monotonic
	// even when rows are evicted (otherwise SQLite would recycle rowids and
	// a since=<old> client could miss events whose ids landed below their
	// cursor). type/at/task_id are indexed-eligible if downstream consumers
	// need filtered replay, but the ring is small enough today that linear
	// scans are fine.
	if _, err := d.conn.Exec(`
		CREATE TABLE IF NOT EXISTS events (
			id           INTEGER PRIMARY KEY AUTOINCREMENT,
			type         TEXT NOT NULL,
			at           TEXT NOT NULL,
			task_id      TEXT NOT NULL DEFAULT '',
			payload_json TEXT NOT NULL DEFAULT ''
		)
	`); err != nil {
		return fmt.Errorf("creating events table: %w", err)
	}

	// Runtime-registered MCP tools (PR 4 of the plugin substrate). Each row is
	// a single proxied tool registered by a plugin via POST /api/mcp/tools.
	// The MCP server consults this table alongside the built-in tool list at
	// tools/list and dispatches tools/call invocations by HTTP-POSTing to
	// callback_url. Persistence here is what makes registrations survive a
	// daemon restart per the contract — without it, every restart would
	// silently drop every plugin tool until the plugin re-registered.
	if _, err := d.conn.Exec(`
		CREATE TABLE IF NOT EXISTS plugin_mcp_tools (
			name          TEXT PRIMARY KEY,
			scope         TEXT NOT NULL,
			description   TEXT NOT NULL DEFAULT '',
			input_schema  TEXT NOT NULL DEFAULT '{}',
			callback_url  TEXT NOT NULL,
			auth_header   TEXT NOT NULL DEFAULT '',
			registered_at INTEGER NOT NULL,
			last_seen_at  INTEGER NOT NULL DEFAULT 0
		)
	`); err != nil {
		return fmt.Errorf("creating plugin_mcp_tools table: %w", err)
	}
	// Scope index for cascade-on-revoke and the per-scope sweep operations;
	// last_seen_at index for the idle sweeper's range scan.
	d.conn.Exec(`CREATE INDEX IF NOT EXISTS idx_plugin_mcp_tools_scope     ON plugin_mcp_tools(scope)`)        //nolint:errcheck
	d.conn.Exec(`CREATE INDEX IF NOT EXISTS idx_plugin_mcp_tools_last_seen ON plugin_mcp_tools(last_seen_at)`) //nolint:errcheck

	// Plugin-registered settings sections (PR 7 of the plugin substrate).
	// Composite UNIQUE(scope, title) lets a plugin upsert by re-registering
	// the same key — the substrate plan caps a plugin at one section, and
	// the (scope, title) uniqueness leaves room for future plans that allow
	// many. spec_json holds the encoded [settings.FormSpec]; we deserialize
	// on read rather than splitting fields across rows so the JSON shape
	// stays the single source of truth.
	if _, err := d.conn.Exec(`
		CREATE TABLE IF NOT EXISTS plugin_settings (
			id            INTEGER PRIMARY KEY AUTOINCREMENT,
			scope         TEXT NOT NULL,
			title         TEXT NOT NULL,
			type          TEXT NOT NULL DEFAULT 'form',
			spec_json     TEXT NOT NULL DEFAULT '',
			callback_url  TEXT NOT NULL,
			auth_header   TEXT NOT NULL DEFAULT '',
			created_at    TEXT NOT NULL,
			UNIQUE(scope, title)
		)
	`); err != nil {
		return fmt.Errorf("creating plugin_settings table: %w", err)
	}
	// Idempotent add for databases created before auth_header existed. The
	// callback proxy forwards this as the Authorization header on form-submit
	// POSTs so plugins can require auth on their callback endpoint.
	d.conn.Exec(`ALTER TABLE plugin_settings ADD COLUMN auth_header TEXT NOT NULL DEFAULT ''`) //nolint:errcheck

	// Plugin-registered top-level views (PR 9 of the plugin substrate). Each
	// row is one full-screen UI surface owned by a plugin: the TUI opens a
	// WebSocket to callback_url when the hotkey fires, streams ANSI bytes
	// from the plugin into a streampane, and forwards keystrokes back. scope
	// is reserved for the post-PR-1 scope-token gating swap; today every row
	// is registered under the master token.
	if _, err := d.conn.Exec(`
		CREATE TABLE IF NOT EXISTS plugin_views (
			id           INTEGER PRIMARY KEY AUTOINCREMENT,
			scope        TEXT NOT NULL DEFAULT '',
			title        TEXT NOT NULL,
			hotkey       TEXT NOT NULL DEFAULT '',
			callback_url TEXT NOT NULL,
			created_at   TEXT NOT NULL,
			UNIQUE(scope, title)
		)
	`); err != nil {
		return fmt.Errorf("creating plugin_views table: %w", err)
	}

	if err := d.createHeraTables(); err != nil {
		return err
	}

	return nil
}

// createHeraTables installs the native Hera role/binding/orchestrator model
// (Milestone 1 of merging Hera into Argus — see context/plans/merge-hera-into-argus.md).
// These are the FINAL shape of Hera's 10 versioned migrations collapsed into
// drop-in CREATE TABLE IF NOT EXISTS statements; Argus has no versioned
// migration runner, so the migration history is not replayed.
//
// FK cascade (orchestrator → roles → bindings / role_status) is REAL here —
// it requires PRAGMA foreign_keys=ON, which Open/OpenInMemory now enable on the
// connection. The binding → argus task relationship is deliberately NOT an FK:
// argus_task_id is plain TEXT because tasks are soft-archivable and a hard FK
// would either block archive or cascade-wipe bindings the user expects to
// resume. That cleanup is app-level — see Delete in tasks.go, which ends live
// bindings, while SetArchived leaves them intact (archive is resumable).
func (d *DB) createHeraTables() error {
	// Idempotent ADD COLUMN migration for existing DBs that predate the nuked_at
	// column (BUG-022 two-state EOL). This MUST run BEFORE the DDL block below: the
	// DDL creates `CREATE INDEX ... ON hera_*(nuked_at)`, and on a pre-existing DB
	// the `CREATE TABLE IF NOT EXISTS` is a no-op, so without the column the whole
	// multi-statement Exec aborts with "no such column: nuked_at". On a fresh DB
	// these ALTERs fail (table doesn't exist yet) and are intentionally ignored —
	// the CREATE TABLE below then creates the column inline.
	d.conn.Exec(`ALTER TABLE hera_orchestrators ADD COLUMN nuked_at TEXT`) //nolint:errcheck
	d.conn.Exec(`ALTER TABLE hera_roles ADD COLUMN nuked_at TEXT`)         //nolint:errcheck
	// Idempotent ADD COLUMN migration for the orchestrator-level base_branch
	// (add-hera-plan-base-branch). Same pattern as nuked_at above: additive,
	// nullable, no backfill — existing rows read back NULL and root nodes fall
	// through to the coordinator-branch default. Fails on a fresh DB (table not
	// yet created) and is intentionally ignored; the CREATE TABLE below carries
	// the column inline.
	d.conn.Exec(`ALTER TABLE hera_orchestrators ADD COLUMN base_branch TEXT`) //nolint:errcheck
	// node_kind (add-hera-subcoord-nodes): plan-node discriminator. NULL means
	// leaf worker (default); "subcoord" means the gater materializes as a
	// distinct coordinator agent. Idempotent — ignored when the column already
	// exists (e.g. fresh DBs where the CREATE TABLE below adds it inline).
	d.conn.Exec(`ALTER TABLE hera_roles ADD COLUMN node_kind TEXT`) //nolint:errcheck
	// Idempotent ADD COLUMN migration for the role-level cancelled_at
	// (make-hera-plan-living). Same pattern as nuked_at / base_branch above:
	// additive, nullable TEXT, no backfill — existing rows read back NULL
	// (active / not cancelled). Fails on a fresh DB (table not yet created) and
	// is intentionally ignored; the CREATE TABLE below carries the column inline.
	d.conn.Exec(`ALTER TABLE hera_roles ADD COLUMN cancelled_at TEXT`) //nolint:errcheck
	// archetype (add-diligence-profiles): a planned node's intended archetype,
	// mirrored onto the live role for display. Same additive nullable-TEXT
	// pattern as node_kind / cancelled_at above — no backfill, existing rows
	// read back NULL (no archetype). Fails on a fresh DB (table not yet created)
	// and is intentionally ignored; the CREATE TABLE below carries it inline.
	d.conn.Exec(`ALTER TABLE hera_roles ADD COLUMN archetype TEXT`) //nolint:errcheck
	// Idempotent ADD COLUMN migration for the orchestrator-level kanban_status
	// (add-hera-kanban-status). NOT NULL DEFAULT 'active' is SQLite-safe on
	// ALTER TABLE ADD COLUMN (a constant default backfills every existing row).
	// Deliberately no CHECK constraint: SQLite cannot widen/add a CHECK via
	// ALTER TABLE ADD COLUMN, so one written only in the CREATE TABLE branch
	// below would be inert on every already-existing DB (the exact
	// hera_role_status CHECK-widening footgun make-hera-plan-living hit) —
	// db.HeraKanbanStatus is the only writer. Fails on a fresh DB (table not
	// yet created) and is intentionally ignored; the CREATE TABLE below
	// carries the column inline.
	d.conn.Exec(`ALTER TABLE hera_orchestrators ADD COLUMN kanban_status TEXT NOT NULL DEFAULT 'active'`) //nolint:errcheck

	ddl := `
		CREATE TABLE IF NOT EXISTS hera_orchestrators (
			id          INTEGER PRIMARY KEY,
			name        TEXT NOT NULL,
			created_at  TEXT NOT NULL,
			archived_at TEXT,
			pinned_at   TEXT,
			-- nuked_at (BUG-022 two-state EOL): the Tier-2 "nuked" marker. A nuked
			-- row is REMOVED from the rail entirely (not shown in any archive); its
			-- worktree/branch are reclaimed and its DB row retained for DB-only
			-- recovery. nuked rows always also carry archived_at (so they leave the
			-- active-name index). archived_at-set/nuked_at-NULL is the Tier-1 HIDDEN
			-- state (reversible, nested in the parent's archive expando).
			nuked_at    TEXT,
			-- base_branch (add-hera-plan-base-branch): the explicit base branch a
			-- plan-DAG's ROOT nodes stack on, set optionally at bootstrap. NULL/empty
			-- means root nodes default to the coordinator's branch (then the project
			-- default). Has no effect on blocker-having nodes.
			base_branch TEXT,
			-- kanban_status (add-hera-kanban-status): the independent operator-set
			-- "where does this effort stand" axis for a TOP-LEVEL coordinator —
			-- active/backlog/blocked/done, default active. Wholly separate from
			-- pinned_at/archived_at and from any role's hera_role_status; see
			-- db.HeraKanbanStatus. No CHECK constraint (see the ALTER comment above).
			kanban_status TEXT NOT NULL DEFAULT 'active'
		);
		-- Partial unique on name scoped to active rows: an archived orchestrator
		-- may coexist with a fresh active row of the same name (Hera migration 0003).
		CREATE UNIQUE INDEX IF NOT EXISTS idx_hera_orch_active_name ON hera_orchestrators(name) WHERE archived_at IS NULL;
		CREATE INDEX IF NOT EXISTS idx_hera_orch_archived ON hera_orchestrators(archived_at);
		CREATE INDEX IF NOT EXISTS idx_hera_orch_pinned   ON hera_orchestrators(pinned_at);
		CREATE INDEX IF NOT EXISTS idx_hera_orch_nuked    ON hera_orchestrators(nuked_at);

		CREATE TABLE IF NOT EXISTS hera_roles (
			id              INTEGER PRIMARY KEY,
			orchestrator_id INTEGER NOT NULL REFERENCES hera_orchestrators(id) ON DELETE CASCADE,
			name            TEXT NOT NULL,
			kind            TEXT NOT NULL CHECK (kind IN ('coordinator','worker','freelance')),
			argus_project   TEXT NOT NULL,
			prompt          TEXT NOT NULL DEFAULT '',
			created_at      TEXT NOT NULL,
			archived_at     TEXT,
			pinned_at       TEXT,
			-- nuked_at (BUG-022 two-state EOL): see hera_orchestrators.nuked_at.
			nuked_at        TEXT,
			-- node_kind (add-hera-subcoord-nodes): plan-node discriminator. NULL or
			-- absent means leaf worker (default); "subcoord" means materialize as a
			-- distinct coordinator agent. Only meaningful on planned roles (no binding).
			node_kind       TEXT,
			-- cancelled_at (make-hera-plan-living): a planned node may be cancelled
			-- by the coordinator (plan-mutation verb CancelHeraPlannedNode). A
			-- cancelled node is kept in the DB (not deleted) so the plan view can
			-- show it as cancelled, but it is excluded from ListHeraPlannedNodes so
			-- the gater never materializes it, and it is treated as non-blocking
			-- (satisfied) by the gater so its dependents can still proceed.
			cancelled_at    TEXT,
			-- archetype (add-diligence-profiles): a planned node's intended
			-- diligence archetype, mirrored onto the live role for display. NULL or
			-- empty means no archetype. The gater copies it into CreateAndStart so
			-- the materialized task carries it as the model-resolution key.
			archetype       TEXT
		);
		CREATE INDEX IF NOT EXISTS idx_hera_roles_kind ON hera_roles(orchestrator_id, kind);
		CREATE UNIQUE INDEX IF NOT EXISTS idx_hera_roles_active_name ON hera_roles(orchestrator_id, name) WHERE archived_at IS NULL;
		CREATE INDEX IF NOT EXISTS idx_hera_roles_archived  ON hera_roles(archived_at);
		CREATE INDEX IF NOT EXISTS idx_hera_roles_pinned    ON hera_roles(pinned_at);
		CREATE INDEX IF NOT EXISTS idx_hera_roles_nuked     ON hera_roles(nuked_at);
		CREATE INDEX IF NOT EXISTS idx_hera_roles_cancelled ON hera_roles(cancelled_at);

		CREATE TABLE IF NOT EXISTS hera_bindings (
			id              INTEGER PRIMARY KEY,
			role_id         INTEGER NOT NULL REFERENCES hera_roles(id) ON DELETE CASCADE,
			orchestrator_id INTEGER REFERENCES hera_orchestrators(id) ON DELETE CASCADE,
			argus_task_id   TEXT NOT NULL,
			worktree_path   TEXT NOT NULL,
			started_at      TEXT NOT NULL,
			ended_at        TEXT,
			end_reason      TEXT
		);
		-- THE multi-binding invariant (Hera migration 0004): live-uniqueness is
		-- per-(task, orchestrator), NOT per-task. One argus task may simultaneously
		-- be a worker in orchestrator A and a coordinator in B. orchestrator_id is
		-- denormalized from the role so these partial indexes need no JOIN. Role-side
		-- uniqueness stays one-live-binding-per-role (a role is incarnated at most once).
		CREATE UNIQUE INDEX IF NOT EXISTS idx_hera_bindings_live_role          ON hera_bindings(role_id) WHERE ended_at IS NULL;
		CREATE UNIQUE INDEX IF NOT EXISTS idx_hera_bindings_live_task_orch     ON hera_bindings(argus_task_id, orchestrator_id) WHERE ended_at IS NULL;
		CREATE UNIQUE INDEX IF NOT EXISTS idx_hera_bindings_live_worktree_orch ON hera_bindings(worktree_path, orchestrator_id) WHERE ended_at IS NULL;

		CREATE TABLE IF NOT EXISTS hera_role_status (
			role_id    INTEGER PRIMARY KEY REFERENCES hera_roles(id) ON DELETE CASCADE,
			status     TEXT NOT NULL CHECK (status IN ('idle','working','blocked','done','failed')),
			updated_at TEXT NOT NULL
		);

		-- Role-addressed message bus (Milestone 2). FK cascades on both sender
		-- and recipient role deletes — if a role is hard-deleted, its messages go
		-- too. in_reply_to SET NULL on parent delete so threads don't break.
		-- read_at is real NULL (never '') — the partial inbox index below requires it.
		-- delivery_mode tracks how (or whether) the message reached the recipient PTY.
		CREATE TABLE IF NOT EXISTS hera_messages (
			id             INTEGER PRIMARY KEY AUTOINCREMENT,
			from_role_id   INTEGER NOT NULL REFERENCES hera_roles(id) ON DELETE CASCADE,
			to_role_id     INTEGER NOT NULL REFERENCES hera_roles(id) ON DELETE CASCADE,
			body           TEXT NOT NULL,
			tldr           TEXT NOT NULL DEFAULT '',
			in_reply_to    INTEGER REFERENCES hera_messages(id) ON DELETE SET NULL,
			sent_at        TEXT NOT NULL,
			read_at        TEXT,
			delivery_mode  TEXT NOT NULL DEFAULT 'pending',
			delivered_at   TEXT
		);
		-- Partial inbox index: only indexes unread rows, so cost scales with the
		-- unread count not the full message history. Covers the HeraInbox + unread-
		-- cap queries. sent_at + id provide a stable oldest-first ordering within
		-- the partial scan.
		CREATE INDEX IF NOT EXISTS idx_hera_msg_inbox ON hera_messages(to_role_id, sent_at, id) WHERE read_at IS NULL;
		-- Sender index covers the rate-limit rolling-window COUNT query.
		CREATE INDEX IF NOT EXISTS idx_hera_msg_sent  ON hera_messages(from_role_id, sent_at);

		-- Per-role tree-scan cursor (Milestone 5). A disposable read bookmark — the
		-- id of the last message a role saw via hera_tree_updates' subtree roll-up,
		-- re-seeded on every read. (Hera's event_cursor is NOT ported: the in-process
		-- events ring needs no cross-restart SSE cursor.)
		--
		-- role_id PK + ON DELETE CASCADE is LOAD-BEARING (Hera BUG-034): a bare
		-- REFERENCES with the default NO ACTION would block role/orchestrator delete
		-- with "FOREIGN KEY constraint failed (787)" because the cursor row pins its
		-- parent role. CASCADE lets a role/orchestrator delete clean the cursor too.
		CREATE TABLE IF NOT EXISTS tree_read_cursors (
			role_id    INTEGER PRIMARY KEY REFERENCES hera_roles(id) ON DELETE CASCADE,
			cursor     INTEGER NOT NULL,
			updated_at TEXT NOT NULL
		);

		-- Plan-DAG blocking edges (add-hera-plan-substrate). A directed edge
		-- (blocked_role_id, blocker_role_id) means "blocked waits on blocker to
		-- reach hera role-status done before it materializes". FK CASCADE on both
		-- endpoints so a role delete prunes its edges (mirroring tree_read_cursors
		-- BUG-034 — a bare REFERENCES would PIN the role under PRAGMA foreign_keys=ON
		-- and block role/orchestrator delete with error 787). The composite PK
		-- guards against duplicate edges. The same-orchestrator endpoint constraint
		-- and the cycle check are enforced app-side at insert (AddHeraBlock), not in
		-- the schema, because SQLite cannot express either declaratively.
		CREATE TABLE IF NOT EXISTS hera_blocks (
			blocked_role_id INTEGER NOT NULL REFERENCES hera_roles(id) ON DELETE CASCADE,
			blocker_role_id INTEGER NOT NULL REFERENCES hera_roles(id) ON DELETE CASCADE,
			created_at      TEXT NOT NULL,
			PRIMARY KEY (blocked_role_id, blocker_role_id)
		);
		CREATE INDEX IF NOT EXISTS idx_hera_blocks_blocker ON hera_blocks(blocker_role_id);
	`
	if _, err := d.conn.Exec(ddl); err != nil {
		return fmt.Errorf("creating hera tables: %w", err)
	}
	return nil
}
