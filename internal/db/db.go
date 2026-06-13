package db

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/drn/argus/internal/config"

	_ "modernc.org/sqlite"
)

const schemaVersion = 1

// DB is the SQLite-backed data store for tasks, projects, backends, and config.
type DB struct {
	conn *sql.DB
	mu   sync.Mutex

	// cfgLoader overlays ~/.argus/config.toml (next to the DB file) onto the
	// assembled Config in Config(). Nil for in-memory/test DBs so they never
	// read the real user file.
	cfgLoader *config.FileLoader
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

	// foreign_keys(on) makes the hera_* table FK cascades (orchestrator → roles →
	// bindings / role_status) actually fire. SQLite defaults foreign_keys OFF
	// per-connection; the modernc _pragma DSN applies it to every pooled
	// connection. Safe for the rest of the schema — no other table declares an
	// FK, so enabling enforcement is a no-op for them.
	conn, err := sql.Open("sqlite", path+"?_pragma=journal_mode(wal)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(on)")
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}

	// config.toml lives next to the DB file (~/.argus/config.toml), so deriving
	// the path from the DB path keeps tests pointed at their temp dir.
	d := &DB{conn: conn, cfgLoader: config.NewFileLoader(filepath.Join(filepath.Dir(path), config.FileName))}
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
	// Match Open: enable FK enforcement so hera_* cascade tests exercise the
	// same behavior as production.
	conn, err := sql.Open("sqlite", ":memory:?_pragma=foreign_keys(on)")
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

// WithTx executes fn within a transaction, holding the DB mutex for the
// duration. If fn returns an error, the transaction is rolled back; otherwise
// it is committed.
//
// IMPORTANT: fn must operate on the provided *sql.Tx directly. It MUST NOT
// call any *DB methods (Get, Update, Tasks, etc.) as d.mu is held for the
// duration and Go's sync.Mutex is not reentrant — doing so will deadlock.
func (d *DB) WithTx(fn func(tx *sql.Tx) error) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	tx, err := d.conn.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
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

// generateID returns a digit-only string. The HTTP API and the SPA
// interpolate task IDs into URL paths without encoding (e.g.
// `/api/tasks/${taskId}/git/diff?...`), so the format must stay URL-safe.
// If this ever changes to UUIDs/slugs, audit those callers.
func generateID() string {
	return fmt.Sprintf("%d", time.Now().UnixNano())
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
