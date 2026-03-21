package db

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

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
