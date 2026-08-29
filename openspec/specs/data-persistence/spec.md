# Data Persistence

## Purpose

The data persistence capability is the durable store for tasks, projects, backends, and scalar configuration. It owns a single SQLite database under the argus data directory, creates and evolves its schema on open, seeds sensible defaults on first run, and exposes thread-safe create/read/update/delete operations. All other capabilities depend on it as the source of truth for persisted state, so its contracts (ID generation, not-found signalling, transactional grouping, and concurrent-write safety) must be stable.
## Requirements
### Requirement: Database open and initialization

The store SHALL open or create a SQLite database at a caller-supplied path, creating any missing parent directories, ensuring all required tables and indexes exist, and applying default seeding and backend fixups before returning a usable handle. If the directory cannot be created or the file is not a valid SQLite database, Open SHALL return an error rather than a partially-initialized handle.

#### Scenario: Opening a fresh database

- **WHEN** Open is called with a path whose parent directory does not yet exist
- **THEN** the parent directory is created, the schema is initialized, defaults are seeded, and a usable database handle is returned

#### Scenario: Reopening an already-initialized database

- **WHEN** Open is called a second time on a path that was already initialized
- **THEN** initialization is idempotent, seeding does not run again, and a usable handle is returned without error

#### Scenario: Opening a corrupted or unreachable path

- **WHEN** Open is called against a file that is not valid SQLite, or a path whose parent directory cannot be created
- **THEN** Open returns an error and does not return a handle

#### Scenario: In-memory store for ephemeral use

- **WHEN** an in-memory database is requested
- **THEN** the schema is created and defaults are seeded without any filesystem migration step, yielding a usable handle

### Requirement: Schema creation is idempotent and self-evolving

The store SHALL create all required tables and indexes if they do not already exist, and SHALL apply column additions and legacy-column removals in an idempotent way so that repeatedly initializing an existing database neither fails nor duplicates structure.

#### Scenario: Re-running schema creation on an existing database

- **WHEN** schema creation runs against a database that already has the tables
- **THEN** existing data is preserved and no error is raised for already-present tables, columns, or indexes

### Requirement: Default seeding on first run

On first initialization the store SHALL seed default backends and default scalar configuration values. Seeding SHALL be safe to run multiple times: it inserts a default backend only when that backend is missing, and inserts default config values only when no configuration rows exist yet. Seeding SHALL replace known placeholder backend commands (such as `echo`, `cat`, or `true`) with the real default command.

#### Scenario: Fresh database gets defaults

- **WHEN** a fresh database is initialized
- **THEN** the default backend (claude) is present and the default scalar config values are readable through the assembled configuration

#### Scenario: Placeholder backend command is corrected

- **WHEN** a backend named like a default exists with a placeholder command such as `echo`
- **THEN** seeding rewrites that backend's command to the real default command

#### Scenario: Seeding does not clobber existing config

- **WHEN** seeding runs against a database that already contains configuration rows
- **THEN** existing config rows are left unchanged

### Requirement: Backend fixup on every open

The store SHALL, on every open, reconcile known-outdated backend configurations toward the shipped defaults: inserting any default backend that is entirely missing, and correcting specific obsolete command forms while preserving unrelated user customizations.

#### Scenario: Missing default backend is reinserted

- **WHEN** a database is opened that lacks a backend present in the shipped defaults
- **THEN** that default backend is inserted

#### Scenario: Outdated command flags are corrected without losing user flags

- **WHEN** a backend command is missing a required default flag but carries extra user-added flags
- **THEN** the required default behavior is applied and the user's extra flags are retained

#### Scenario: Already-correct backend is left untouched

- **WHEN** a backend already matches the expected default form
- **THEN** the fixup pass makes no change to it

### Requirement: Task create and read

The store SHALL persist a task and return it on lookup by ID. On insert, when the task has no ID the store SHALL assign a generated URL-safe identifier, and when it has no creation timestamp the store SHALL stamp the current time. A caller-supplied ID SHALL be preserved.

#### Scenario: Adding a task without an ID

- **WHEN** a task with an empty ID is added
- **THEN** a non-empty generated ID and a non-zero creation timestamp are assigned, and the task is retrievable by that ID

#### Scenario: Adding a task with a caller-supplied ID

- **WHEN** a task is added with an explicit ID
- **THEN** that ID is preserved and used as the lookup key

#### Scenario: Round-tripping all task fields

- **WHEN** a task with status, project, branch, sandboxed/archived/pinned flags, orchestration fields, and timestamps is added and then read back
- **THEN** every persisted field, including the JSON-encoded dependency list and stored times, matches the values written

### Requirement: Not-found signalling for missing tasks

Every task mutation and the single-task read SHALL report a missing target via a recognizable not-found error rather than silently succeeding, so callers can distinguish "no such task" from a successful no-op.

#### Scenario: Getting a non-existent task

- **WHEN** a task is requested by an ID that does not exist
- **THEN** a not-found error is returned

#### Scenario: Updating a non-existent task

- **WHEN** an update targets an ID with no matching row
- **THEN** a not-found error is returned and no row is created

#### Scenario: Partial-update of a non-existent task

- **WHEN** a partial-column write (result, plan slug, dependencies, archived state, or rename) targets a missing ID
- **THEN** a not-found error is returned

### Requirement: Full and partial task updates

The store SHALL support replacing all mutable columns of an existing task, and SHALL also offer targeted partial writes (name, result blob, plan slug, dependency list, archived flag) that update only the named column(s) so a concurrent full-row write cannot be clobbered by a stale read-modify-write.

#### Scenario: Full update replaces mutable fields

- **WHEN** an existing task is updated with changed fields
- **THEN** the stored row reflects all the new values on the next read

#### Scenario: Partial write leaves other fields intact

- **WHEN** only the result blob (or plan slug, or dependency list) is written for a task whose status was concurrently changed
- **THEN** the written column takes the new value and the unrelated status is preserved

#### Scenario: Partial writes are last-write-wins idempotent

- **WHEN** the same partial column is written twice with different values
- **THEN** the most recent value is the one read back

### Requirement: Conditional rename (compare-and-swap)

The store SHALL offer a name update that succeeds only when the row's current name still equals an expected value, returning success/failure to the caller. A name that has drifted since the expected value was observed SHALL be treated as a no-op (no error), while a missing row SHALL be a not-found error. A plain rename SHALL update only the name and leave all other fields untouched.

#### Scenario: Expected name still matches

- **WHEN** a conditional rename is issued and the current name equals the expected value
- **THEN** the name is updated and success is reported

#### Scenario: Name drifted since observation

- **WHEN** a conditional rename is issued but the current name no longer equals the expected value and the row still exists
- **THEN** no change is made and a non-error "not applied" result is reported

#### Scenario: Plain rename preserves other fields

- **WHEN** a task is renamed
- **THEN** only the name changes and fields such as status are preserved

### Requirement: Archive write enforces pinned/archived mutual exclusivity

The store SHALL provide a targeted archive write that, when archiving, also clears the pinned flag so that at most one of pinned/archived is ever set, and when unarchiving leaves the pinned flag untouched. Archiving a task SHALL also remove its queued messages and sidecar metadata in the same transaction.

#### Scenario: Archiving a pinned task clears pinned

- **WHEN** a pinned task is archived
- **THEN** the row reads back archived and not pinned, and unrelated fields such as status are preserved

#### Scenario: Unarchiving leaves pinned alone

- **WHEN** a task is unarchived
- **THEN** the pinned flag is left unchanged

### Requirement: Task delete and prune

The store SHALL delete a task by ID, removing its dependent message and sidecar-metadata rows in the same transaction, and SHALL report a not-found error when no row matched. The store SHALL also support pruning all completed tasks, returning the set of tasks it removed.

#### Scenario: Deleting an existing task removes dependents atomically

- **WHEN** an existing task is deleted
- **THEN** the task row and its dependent rows are removed together and the task is no longer retrievable

#### Scenario: Deleting a missing task

- **WHEN** delete targets an ID with no row
- **THEN** a not-found error is returned

#### Scenario: Pruning completed tasks

- **WHEN** prune is invoked while completed tasks exist
- **THEN** all completed tasks are removed and returned, while non-completed tasks remain

#### Scenario: Pruning with nothing to remove

- **WHEN** prune is invoked and no task has completed status
- **THEN** nothing is removed and an empty result is returned

### Requirement: Task listing order and idempotency lookup

The store SHALL list all tasks ordered by ascending creation time, and SHALL provide a lookup that returns the first non-archived task matching a (name, project) pair (or no match) for duplicate detection. Archived rows SHALL be excluded from the (name, project) lookup so a reused name is not blocked by a stale archived task.

#### Scenario: Tasks are returned oldest-first

- **WHEN** multiple tasks are listed
- **THEN** they are ordered by ascending creation time

#### Scenario: Name/project lookup finds a live task

- **WHEN** a live (non-archived) task matches the queried name and project
- **THEN** that task is returned

#### Scenario: Name/project lookup ignores archived and mismatched rows

- **WHEN** the only matching row is archived, or no row matches the name/project pair
- **THEN** no match is returned with no error

### Requirement: Projects and backends CRUD

The store SHALL persist projects and backends keyed by name, supporting upsert (insert-or-replace) on write, listing ordered by name, and delete by name. Reads SHALL reconstruct structured values (such as a project's per-project sandbox settings) from their stored encoding.

#### Scenario: Upserting and reading back a project

- **WHEN** a project is written and then listed
- **THEN** its path, branch, backend, and sandbox settings are reconstructed as written

#### Scenario: Overwriting an existing backend

- **WHEN** a backend is written with a name that already exists
- **THEN** the existing row is replaced with the new command and prompt flag

#### Scenario: Deleting a project or backend

- **WHEN** delete is called for a named project or backend
- **THEN** that row is removed from subsequent listings

### Requirement: Scalar configuration assembly and overrides

The store SHALL assemble a full configuration value by starting from defaults and applying persisted scalar overrides, projects, and backends. Stored string and boolean keys SHALL override their defaults; integer port values SHALL override only when they parse as a positive integer, otherwise the default is retained. Config writes SHALL upsert by key.

#### Scenario: Stored overrides take effect

- **WHEN** scalar config keys (defaults, keybindings, UI, sandbox, KB, API, source path) are set and the configuration is assembled
- **THEN** each stored value overrides the corresponding default

#### Scenario: Invalid port values fall back to defaults

- **WHEN** a stored port value is non-numeric, zero, or negative
- **THEN** the assembled configuration keeps the default port

#### Scenario: Setting a config value upserts by key

- **WHEN** a config key is written more than once
- **THEN** the latest value is the one read back

### Requirement: Transactional execution

The store SHALL run a caller-supplied function inside a single database transaction, committing when the function returns no error and rolling back all of its writes when the function returns an error.

#### Scenario: Successful transaction commits

- **WHEN** a transactional function performs writes and returns no error
- **THEN** all of its writes are durably committed

#### Scenario: Failing transaction rolls back

- **WHEN** a transactional function performs writes and then returns an error
- **THEN** none of its writes persist and the prior state is intact

### Requirement: Thread-safe access

The store SHALL serialize concurrent access so that reads and writes from multiple goroutines do not corrupt state or cursors. Operations that emit downstream events SHALL do so only after releasing the internal lock to avoid re-entrant deadlock.

#### Scenario: Concurrent operations remain consistent

- **WHEN** multiple goroutines read and write through the store concurrently
- **THEN** operations complete without data corruption and reads observe well-formed rows

### Requirement: Orchestrator-scoped blocking-edge query

The store SHALL expose `ListHeraBlocks(orchID)` returning every `hera_blocks` edge whose endpoints belong to the given orchestrator, as `(blocked_role_id, blocker_role_id)` pairs. This complements the substrate's per-role `HeraBlockersOf` with a single bulk read for the whole orchestrator, so the plan view can project all edges without N per-node queries. The result is deterministically ordered (by blocked then blocker role id) and excludes edges whose endpoints are archived or nuked roles, consistent with how the view filters roles.

#### Scenario: Returns all edges for an orchestrator

- **WHEN** an orchestrator has blocking edges `3a←2b` and `2a←1a`
- **THEN** `ListHeraBlocks(orchID)` returns both pairs in deterministic order

#### Scenario: Empty when no plan authored

- **WHEN** an orchestrator has no blocking edges
- **THEN** `ListHeraBlocks(orchID)` returns an empty slice without error

### Requirement: Archetype and profile-binding columns

The schema SHALL persist a task's archetype, a task's per-spawn profile override, a project's bound
profile name, and a hera role's planned archetype: a `tasks.archetype` column (the authoritative
model-resolution key), a `tasks.profile` column (the per-spawn profile override — non-empty means the
operator overrode the project's profile for this one spawn), a `projects.profile` column (the
project→profile-name binding), and a `hera_roles.archetype` column (a planned node's intended archetype,
mirrored for the live role). Each SHALL default to empty for existing rows and require no data migration.
The database SHALL NOT store profile bodies.

#### Scenario: Task archetype round-trips

- **WHEN** a task with archetype `ci_loop` is written and re-read
- **THEN** the read-back task carries `ci_loop`

#### Scenario: Task per-spawn profile override round-trips

- **WHEN** a task is created with a per-spawn profile override `custom`
- **THEN** the read-back task carries `custom` as its `profile` field

#### Scenario: Project profile name round-trips

- **WHEN** a project with profile `lean` is written and re-read
- **THEN** the read-back project carries `lean`

#### Scenario: Existing rows default to empty

- **WHEN** the new columns are added to a database with existing rows
- **THEN** those rows read empty archetype / empty profile without error

#### Scenario: Planned role archetype propagates at materialization

- **WHEN** a planned hera role with archetype `review` is materialized into a task
- **THEN** the materialized task carries `review` as its archetype

### Requirement: Per-task sandbox override column

The schema SHALL persist a task's sandbox override as a `tasks.sandbox_override`
column with three encoded states: empty string (no override), `"enabled"`, and
`"disabled"`. It SHALL default to empty for existing rows and require no data
migration.

#### Scenario: Sandbox override round-trips

- **WHEN** a task is created with a sandbox override of `"enabled"` or `"disabled"`
- **THEN** the read-back task carries that same value

#### Scenario: Existing rows default to empty

- **WHEN** the new column is added to a database with existing rows
- **THEN** those rows read an empty sandbox override without error

