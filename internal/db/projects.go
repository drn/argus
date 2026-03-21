package db

import (
	"strings"

	"github.com/drn/argus/internal/config"
)

func (d *DB) Projects() map[string]config.Project {
	d.mu.Lock()
	defer d.mu.Unlock()

	rows, err := d.conn.Query(`SELECT name, path, branch, backend, sandbox_enabled, sandbox_deny_read, sandbox_extra_write FROM projects ORDER BY name`)
	if err != nil {
		return make(map[string]config.Project)
	}
	defer rows.Close()

	projects := make(map[string]config.Project)
	for rows.Next() {
		var name string
		var p config.Project
		var sandboxEnabled, sandboxDenyRead, sandboxExtraWrite string
		if err := rows.Scan(&name, &p.Path, &p.Branch, &p.Backend, &sandboxEnabled, &sandboxDenyRead, &sandboxExtraWrite); err != nil {
			continue
		}
		switch sandboxEnabled {
		case "true":
			v := true
			p.Sandbox.Enabled = &v
		case "false":
			v := false
			p.Sandbox.Enabled = &v
		}
		if sandboxDenyRead != "" {
			p.Sandbox.DenyRead = splitCSV(sandboxDenyRead)
		}
		if sandboxExtraWrite != "" {
			p.Sandbox.ExtraWrite = splitCSV(sandboxExtraWrite)
		}
		projects[name] = p
	}
	return projects
}

func (d *DB) SetProject(name string, p config.Project) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	sandboxEnabled := ""
	if p.Sandbox.Enabled != nil {
		if *p.Sandbox.Enabled {
			sandboxEnabled = "true"
		} else {
			sandboxEnabled = "false"
		}
	}
	sandboxDenyRead := strings.Join(p.Sandbox.DenyRead, ",")
	sandboxExtraWrite := strings.Join(p.Sandbox.ExtraWrite, ",")

	_, err := d.conn.Exec(`INSERT OR REPLACE INTO projects (name, path, branch, backend, sandbox_enabled, sandbox_deny_read, sandbox_extra_write) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		name, p.Path, p.Branch, p.Backend, sandboxEnabled, sandboxDenyRead, sandboxExtraWrite)
	return err
}

func (d *DB) DeleteProject(name string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	_, err := d.conn.Exec(`DELETE FROM projects WHERE name=?`, name)
	return err
}
