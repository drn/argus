package db

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/model"
	_ "modernc.org/sqlite"
)

const schemaVersion = 1

// DB is the SQLite-backed data store for tasks, projects, backends, and config.
type DB struct {
	conn *sql.DB
	mu   sync.Mutex
}

// DataDir returns the argus data directory (~/.argus).
func DataDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".argus")
}

// DefaultPath returns the default database path.
func DefaultPath() string {
	return filepath.Join(DataDir(), "data.sql")
}

// Open opens (or creates) the SQLite database at path.
// It creates tables if needed, seeds defaults, and fixes outdated backends.
func Open(path string) (*DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("creating data dir: %w", err)
	}

	conn, err := sql.Open("sqlite", path+"?_pragma=journal_mode(wal)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}

	d := &DB{conn: conn}
	if err := d.createTables(); err != nil {
		conn.Close()
		return nil, err
	}
	if err := d.migrate(); err != nil {
		conn.Close()
		return nil, err
	}
	if err := d.fixupBackends(); err != nil {
		conn.Close()
		return nil, err
	}
	return d, nil
}

// OpenInMemory creates an in-memory database for testing.
func OpenInMemory() (*DB, error) {
	conn, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		return nil, err
	}

	d := &DB{conn: conn}
	if err := d.createTables(); err != nil {
		conn.Close()
		return nil, err
	}
	// Seed defaults for in-memory (no migration from files).
	if err := d.seedDefaults(); err != nil {
		conn.Close()
		return nil, err
	}
	return d, nil
}

// Close closes the database connection.
func (d *DB) Close() error {
	return d.conn.Close()
}

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
			created_at TEXT NOT NULL,
			started_at TEXT NOT NULL DEFAULT '',
			ended_at   TEXT NOT NULL DEFAULT ''
		);
		CREATE TABLE IF NOT EXISTS projects (
			name    TEXT PRIMARY KEY,
			path    TEXT NOT NULL,
			branch  TEXT NOT NULL DEFAULT '',
			backend TEXT NOT NULL DEFAULT ''
		);
		CREATE TABLE IF NOT EXISTS backends (
			name        TEXT PRIMARY KEY,
			command     TEXT NOT NULL,
			prompt_flag TEXT NOT NULL DEFAULT ''
		);
		CREATE TABLE IF NOT EXISTS config (
			key   TEXT PRIMARY KEY,
			value TEXT NOT NULL
		);
	`
	_, err := d.conn.Exec(ddl)
	return err
}

// --- Tasks ---

// taskColumns is the canonical column list for task queries.
const taskColumns = `id, name, status, project, branch, prompt, backend, worktree, agent_pid, session_id, created_at, started_at, ended_at`

// scanner is implemented by both *sql.Row and *sql.Rows.
type scanner interface {
	Scan(dest ...any) error
}

// scanTask reads a task from a row using the canonical column order.
func scanTask(row scanner) (*model.Task, error) {
	t := &model.Task{}
	var status, createdAt, startedAt, endedAt string
	if err := row.Scan(&t.ID, &t.Name, &status, &t.Project, &t.Branch, &t.Prompt, &t.Backend, &t.Worktree, &t.AgentPID, &t.SessionID, &createdAt, &startedAt, &endedAt); err != nil {
		return nil, err
	}
	t.Status, _ = model.ParseStatus(status)
	t.CreatedAt = parseTime(createdAt)
	t.StartedAt = parseTime(startedAt)
	t.EndedAt = parseTime(endedAt)
	return t, nil
}

func (d *DB) Tasks() []*model.Task {
	d.mu.Lock()
	defer d.mu.Unlock()

	rows, err := d.conn.Query(`SELECT ` + taskColumns + ` FROM tasks ORDER BY created_at ASC`)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var tasks []*model.Task
	for rows.Next() {
		if t, err := scanTask(rows); err == nil {
			tasks = append(tasks, t)
		}
	}
	return tasks
}

func (d *DB) Add(t *model.Task) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if t.ID == "" {
		t.ID = generateID()
	}
	if t.CreatedAt.IsZero() {
		t.CreatedAt = time.Now()
	}

	_, err := d.conn.Exec(`INSERT INTO tasks (id, name, status, project, branch, prompt, backend, worktree, agent_pid, session_id, created_at, started_at, ended_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ID, t.Name, t.Status.String(), t.Project, t.Branch, t.Prompt, t.Backend, t.Worktree, t.AgentPID, t.SessionID,
		formatTime(t.CreatedAt), formatTime(t.StartedAt), formatTime(t.EndedAt))
	return err
}

func (d *DB) Update(t *model.Task) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	res, err := d.conn.Exec(`UPDATE tasks SET name=?, status=?, project=?, branch=?, prompt=?, backend=?, worktree=?, agent_pid=?, session_id=?, created_at=?, started_at=?, ended_at=? WHERE id=?`,
		t.Name, t.Status.String(), t.Project, t.Branch, t.Prompt, t.Backend, t.Worktree, t.AgentPID, t.SessionID,
		formatTime(t.CreatedAt), formatTime(t.StartedAt), formatTime(t.EndedAt), t.ID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("task not found: %s", t.ID)
	}
	return nil
}

func (d *DB) Delete(id string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	res, err := d.conn.Exec(`DELETE FROM tasks WHERE id=?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("task not found: %s", id)
	}
	return nil
}

func (d *DB) Get(id string) (*model.Task, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	row := d.conn.QueryRow(`SELECT `+taskColumns+` FROM tasks WHERE id=?`, id)
	t, err := scanTask(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("task not found: %s", id)
	}
	if err != nil {
		return nil, err
	}
	return t, nil
}

func (d *DB) PruneCompleted() ([]*model.Task, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	// Fetch completed tasks first
	rows, err := d.conn.Query(`SELECT ` + taskColumns + ` FROM tasks WHERE status='complete'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var pruned []*model.Task
	for rows.Next() {
		if t, err := scanTask(rows); err == nil {
			pruned = append(pruned, t)
		}
	}

	if len(pruned) == 0 {
		return nil, nil
	}

	_, err = d.conn.Exec(`DELETE FROM tasks WHERE status='complete'`)
	if err != nil {
		return nil, err
	}
	return pruned, nil
}

// --- Projects ---

func (d *DB) Projects() map[string]config.Project {
	d.mu.Lock()
	defer d.mu.Unlock()

	rows, err := d.conn.Query(`SELECT name, path, branch, backend FROM projects ORDER BY name`)
	if err != nil {
		return make(map[string]config.Project)
	}
	defer rows.Close()

	projects := make(map[string]config.Project)
	for rows.Next() {
		var name string
		var p config.Project
		if err := rows.Scan(&name, &p.Path, &p.Branch, &p.Backend); err != nil {
			continue
		}
		projects[name] = p
	}
	return projects
}

func (d *DB) SetProject(name string, p config.Project) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	_, err := d.conn.Exec(`INSERT OR REPLACE INTO projects (name, path, branch, backend) VALUES (?, ?, ?, ?)`,
		name, p.Path, p.Branch, p.Backend)
	return err
}

func (d *DB) DeleteProject(name string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	_, err := d.conn.Exec(`DELETE FROM projects WHERE name=?`, name)
	return err
}

// --- Backends ---

func (d *DB) Backends() map[string]config.Backend {
	d.mu.Lock()
	defer d.mu.Unlock()

	rows, err := d.conn.Query(`SELECT name, command, prompt_flag FROM backends ORDER BY name`)
	if err != nil {
		return make(map[string]config.Backend)
	}
	defer rows.Close()

	backends := make(map[string]config.Backend)
	for rows.Next() {
		var name string
		var b config.Backend
		if err := rows.Scan(&name, &b.Command, &b.PromptFlag); err != nil {
			continue
		}
		backends[name] = b
	}
	return backends
}

func (d *DB) SetBackend(name string, b config.Backend) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	_, err := d.conn.Exec(`INSERT OR REPLACE INTO backends (name, command, prompt_flag) VALUES (?, ?, ?)`,
		name, b.Command, b.PromptFlag)
	return err
}

// --- Config (assembles config.Config from DB) ---

func (d *DB) Config() config.Config {
	cfg := config.DefaultConfig()

	// Load backends
	cfg.Backends = d.Backends()

	// Load projects
	cfg.Projects = d.Projects()

	// Load scalar config values — hold mutex through iteration
	// to prevent concurrent writes while the rows cursor is open.
	d.mu.Lock()
	kv := make(map[string]string)
	rows, err := d.conn.Query(`SELECT key, value FROM config`)
	if err != nil {
		d.mu.Unlock()
		return cfg
	}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			continue
		}
		kv[k] = v
	}
	rows.Close()
	d.mu.Unlock()

	// String config fields: map config key → pointer to struct field.
	stringFields := []struct {
		key  string
		dest *string
	}{
		{"defaults.backend", &cfg.Defaults.Backend},
		{"keybindings.new", &cfg.Keybindings.New},
		{"keybindings.attach", &cfg.Keybindings.Attach},
		{"keybindings.status", &cfg.Keybindings.Status},
		{"keybindings.delete", &cfg.Keybindings.Delete},
		{"keybindings.quit", &cfg.Keybindings.Quit},
		{"keybindings.help", &cfg.Keybindings.Help},
		{"keybindings.filter", &cfg.Keybindings.Filter},
		{"keybindings.prompt", &cfg.Keybindings.Prompt},
		{"keybindings.worktree", &cfg.Keybindings.Worktree},
		{"ui.theme", &cfg.UI.Theme},
	}
	for _, f := range stringFields {
		if v, ok := kv[f.key]; ok {
			*f.dest = v
		}
	}

	// Bool config fields
	boolFields := []struct {
		key  string
		dest *bool
	}{
		{"ui.show_elapsed", &cfg.UI.ShowElapsed},
		{"ui.show_icons", &cfg.UI.ShowIcons},
	}
	for _, f := range boolFields {
		if v, ok := kv[f.key]; ok {
			*f.dest = v == "true"
		}
	}

	// Optional bool (pointer) config fields
	if v, ok := kv["ui.cleanup_worktrees"]; ok {
		val := v == "true"
		cfg.UI.CleanupWorktrees = &val
	}

	// Sandbox config
	if v, ok := kv["sandbox.enabled"]; ok {
		cfg.Sandbox.Enabled = v == "true"
	}
	if v, ok := kv["sandbox.deny_read"]; ok && v != "" {
		cfg.Sandbox.DenyRead = splitCSV(v)
	}
	if v, ok := kv["sandbox.extra_write"]; ok && v != "" {
		cfg.Sandbox.ExtraWrite = splitCSV(v)
	}

	return cfg
}

// splitCSV splits a comma-separated string into trimmed non-empty parts.
func splitCSV(s string) []string {
	parts := strings.Split(s, ",")
	var result []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}

func (d *DB) SetConfigValue(key, value string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	_, err := d.conn.Exec(`INSERT OR REPLACE INTO config (key, value) VALUES (?, ?)`, key, value)
	return err
}

// SetSandboxEnabled toggles sandbox mode.
func (d *DB) SetSandboxEnabled(enabled bool) error {
	v := "false"
	if enabled {
		v = "true"
	}
	return d.SetConfigValue("sandbox.enabled", v)
}

// --- Helpers ---

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339Nano)
}

func parseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, _ := time.Parse(time.RFC3339Nano, s)
	return t
}

func generateID() string {
	return fmt.Sprintf("%d", time.Now().UnixNano())
}
