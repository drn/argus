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

// SetBackend writes a backend row. Backends are HARDCODED at the user-facing
// surface — there is no UI add/edit and no HTTP POST/PUT route. This method
// remains because (1) the Playwright test harness in cmd/argus-test-server
// seeds a bash-backed backend for integration testing, and (2) the test suite
// extensively injects ad-hoc backends. Production code must NOT call this:
// `seedDefaults` and `fixupBackends` in migrate.go use the raw SQL path
// directly so a future change here doesn't accidentally unlock user-facing
// writes. If you need to add a new shipping backend, edit
// config.DefaultConfig().Backends.
func (d *DB) SetBackend(name string, b config.Backend) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	_, err := d.conn.Exec(`INSERT OR REPLACE INTO backends (name, command, prompt_flag) VALUES (?, ?, ?)`,
		name, b.Command, b.PromptFlag)
	return err
}

// DeleteBackend removes a backend row. Test-only — backends are hardcoded
// post-lockdown and no production code path deletes them. Test fixtures
// (e.g. TestFixupBackends_InsertsMissingDefault) call this to simulate a
// pre-existing DB that predates a shipping backend so the fixup-on-Open
// reinsertion path can be exercised. If you find yourself reaching for this
// from production code, the right answer is to edit config.DefaultConfig()
// instead.
func (d *DB) DeleteBackend(name string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	_, err := d.conn.Exec(`DELETE FROM backends WHERE name = ?`, name)
	return err
}
