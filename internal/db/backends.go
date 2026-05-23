package db

import (
	"fmt"

	"github.com/drn/argus/internal/config"
)

func (d *DB) Backends() (map[string]config.Backend, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	rows, err := d.conn.Query(`SELECT name, command, prompt_flag FROM backends ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("query backends: %w", err)
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
	return backends, nil
}

// SetBackend writes a backend row. Called by:
//   - `handleCreateBackend` / `handleUpdateBackend` in internal/api (master-
//     only REST surface, added when backends became user-mutable from the
//     Settings tab and the remote TUI).
//   - The Playwright test harness in cmd/argus-test-server seeds a bash
//     backend for integration testing.
//   - The test suite injects ad-hoc backends per case.
//
// `seedDefaults` and `fixupBackends` in migrate.go intentionally still use
// the raw SQL path so default-seeding doesn't go through whatever the
// public API path mutates over time. If you need to add a new shipping
// backend at default-install time, edit config.DefaultConfig().Backends.
func (d *DB) SetBackend(name string, b config.Backend) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	_, err := d.conn.Exec(`INSERT OR REPLACE INTO backends (name, command, prompt_flag) VALUES (?, ?, ?)`,
		name, b.Command, b.PromptFlag)
	return err
}

// DeleteBackend removes a backend row. Called by `handleDeleteBackend` in
// internal/api (master-only) and by test fixtures (e.g.
// TestFixupBackends_InsertsMissingDefault) that simulate a pre-existing DB
// predating a shipping backend so the fixup-on-Open reinsertion path can
// be exercised.
//
// If you need to drop a shipping backend at default-install time, edit
// config.DefaultConfig() — the migrate.go path doesn't go through here.
func (d *DB) DeleteBackend(name string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	_, err := d.conn.Exec(`DELETE FROM backends WHERE name = ?`, name)
	return err
}
