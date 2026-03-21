package db

import (
	"github.com/drn/argus/internal/config"
)

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
