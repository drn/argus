package db

import (
	"strconv"

	"github.com/drn/argus/internal/config"
)

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
		{"defaults.todo_project", &cfg.Defaults.TodoProject},
		{"defaults.review_prompt", &cfg.Defaults.ReviewPrompt},
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

	// KB config
	if v, ok := kv["kb.enabled"]; ok {
		cfg.KB.Enabled = v == "true"
	}
	if v, ok := kv["kb.http_port"]; ok {
		if port, err := strconv.Atoi(v); err == nil && port > 0 {
			cfg.KB.HTTPPort = port
		}
	}
	if v, ok := kv["kb.metis_vault_path"]; ok {
		cfg.KB.MetisVaultPath = v
	}
	if v, ok := kv["kb.argus_vault_path"]; ok {
		cfg.KB.ArgusVaultPath = v
	}
	if v, ok := kv["kb.auto_create_tasks"]; ok {
		cfg.KB.AutoCreateTasks = v == "true"
	}

	// API config
	if v, ok := kv["api.enabled"]; ok {
		cfg.API.Enabled = v == "true"
	}
	if v, ok := kv["api.http_port"]; ok {
		if port, err := strconv.Atoi(v); err == nil && port > 0 {
			cfg.API.HTTPPort = port
		}
	}

	return cfg
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
