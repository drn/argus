package db

import (
	"log/slog"
	"strconv"

	"github.com/drn/argus/internal/config"
)

func (d *DB) Config() config.Config {
	cfg := config.DefaultConfig()

	// Load backends (use defaults on error).
	if backends, err := d.Backends(); err == nil {
		cfg.Backends = backends
	} else {
		slog.Error("db.Config: failed to load backends", "err", err)
	}

	// Load projects (use defaults on error).
	if projects, err := d.Projects(); err == nil {
		cfg.Projects = projects
	} else {
		slog.Error("db.Config: failed to load projects", "err", err)
	}

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
		{"defaults.share_project", &cfg.Defaults.ShareProject},
		{"defaults.permission_mode", &cfg.Defaults.PermissionMode},
		// Keybindings are NOT DB-backed: they live in keymap defaults +
		// config.toml overrides only (the old keybindings.* rows were dropped).
		{"ui.theme", &cfg.UI.Theme},
		{"ui.spinner", &cfg.UI.SpinnerStyle},
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
		{"ui.default_agent_zoom", &cfg.UI.DefaultAgentZoom},
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
	if v, ok := kv["sandbox.allow_apple_events"]; ok && v != "" {
		cfg.Sandbox.AllowAppleEvents = splitCSV(v)
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

	// API config
	if v, ok := kv["api.enabled"]; ok {
		cfg.API.Enabled = v == "true"
	}
	if v, ok := kv["api.http_port"]; ok {
		if port, err := strconv.Atoi(v); err == nil && port > 0 {
			cfg.API.HTTPPort = port
		}
	}

	// Hera config. Absent key ⇒ true (enabled by default); only an explicit
	// "false" stored in the DB disables it. The config.toml overlay (applied
	// below by cfgLoader.Apply) wins over this DB value when both are set.
	if v, ok := kv["hera.enabled"]; ok {
		cfg.Hera.Enabled = v == "true"
	}

	// Supervisor config. Absent key ⇒ true (on by default as of P4 — the base
	// value from DefaultConfig is true, mirroring hera.enabled): agents run
	// under the out-of-process session-supervisor so the daemon can bounce
	// without interrupting them. An explicit "false" stored in the DB is the
	// ROLLBACK — it restores the retained in-process runner path (byte-identical
	// to pre-P2). The config.toml overlay (applied below by cfgLoader.Apply)
	// wins over this DB value when both are set, so a toml supervisor.enabled
	// = false is also a valid rollback.
	if v, ok := kv["supervisor.enabled"]; ok {
		cfg.Supervisor.Enabled = v == "true"
	}

	// Argus self-update config.
	if v, ok := kv["argus.source_path"]; ok {
		cfg.Argus.SourcePath = v
	}

	// Todo backend config. Read live on every Config() call (no caching
	// beyond this per-call query) so a backend selected in Settings takes
	// effect on the MCP server's very next tools/list without a restart.
	if v, ok := kv["todo.backend"]; ok {
		cfg.Todo.Backend = v
	}
	if v, ok := kv["todo.things3.project"]; ok {
		cfg.Todo.Things3.Project = v
	}
	if v, ok := kv["todo.things3.tag"]; ok {
		cfg.Todo.Things3.Tag = v
	}

	// Overlay the optional ~/.argus/config.toml on top of defaults + DB. Fields
	// present in the file win; absent fields keep the value resolved above.
	// Nil loader (in-memory/test DB) is a no-op.
	return d.cfgLoader.Apply(cfg)
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
