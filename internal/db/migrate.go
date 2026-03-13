package db

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/model"
)

// migrate checks if the database has been initialized. If not, it imports
// data from the legacy JSON/TOML files and seeds defaults.
func (d *DB) migrate() error {
	var version int
	err := d.conn.QueryRow(`SELECT version FROM schema_version LIMIT 1`).Scan(&version)
	if err == nil {
		return nil // already migrated
	}
	if err != sql.ErrNoRows && err.Error() != "no rows in result set" {
		// Table exists but is empty — fall through to migrate.
		// Any other error means we should check if it's just empty.
		var count int
		if countErr := d.conn.QueryRow(`SELECT COUNT(*) FROM schema_version`).Scan(&count); countErr != nil {
			return fmt.Errorf("checking schema version: %w", err)
		}
		if count > 0 {
			return nil // has a version, skip migration
		}
	}

	// Import from legacy files
	if err := d.importLegacyTasks(); err != nil {
		return fmt.Errorf("importing tasks: %w", err)
	}
	if err := d.importLegacyConfig(); err != nil {
		return fmt.Errorf("importing config: %w", err)
	}

	// Ensure defaults exist
	if err := d.seedDefaults(); err != nil {
		return err
	}

	// Mark migration complete
	_, err = d.conn.Exec(`INSERT INTO schema_version (version) VALUES (?)`, schemaVersion)
	return err
}

// runSeedDefaults is an exported wrapper for testing.
func (d *DB) runSeedDefaults() error {
	return d.seedDefaults()
}

// seedDefaults inserts the default backend and config values if they don't
// already exist. Safe to call multiple times.
func (d *DB) seedDefaults() error {
	cfg := config.DefaultConfig()

	// Default backends — insert if missing, and fix placeholder commands
	// (e.g. "echo") that may have been written by earlier development builds.
	for name, b := range cfg.Backends {
		var existing string
		err := d.conn.QueryRow(`SELECT command FROM backends WHERE name=?`, name).Scan(&existing)
		if err == sql.ErrNoRows {
			// Backend doesn't exist — insert the default
			if _, err := d.conn.Exec(`INSERT INTO backends (name, command, prompt_flag) VALUES (?, ?, ?)`,
				name, b.Command, b.PromptFlag); err != nil {
				return err
			}
		} else if err == nil && (existing == "echo" || existing == "cat" || existing == "true") {
			// Backend exists but has a placeholder command — replace with default
			if _, err := d.conn.Exec(`UPDATE backends SET command=?, prompt_flag=? WHERE name=?`,
				b.Command, b.PromptFlag, name); err != nil {
				return err
			}
		}
	}

	// Default config values — only if no config exists
	var configCount int
	d.conn.QueryRow(`SELECT COUNT(*) FROM config`).Scan(&configCount)
	if configCount == 0 {
		defaults := map[string]string{
			"defaults.backend":      cfg.Defaults.Backend,
			"keybindings.new":       cfg.Keybindings.New,
			"keybindings.attach":    cfg.Keybindings.Attach,
			"keybindings.status":    cfg.Keybindings.Status,
			"keybindings.delete":    cfg.Keybindings.Delete,
			"keybindings.quit":      cfg.Keybindings.Quit,
			"keybindings.help":      cfg.Keybindings.Help,
			"keybindings.filter":    cfg.Keybindings.Filter,
			"keybindings.prompt":    cfg.Keybindings.Prompt,
			"keybindings.worktree":  cfg.Keybindings.Worktree,
			"ui.theme":              cfg.UI.Theme,
			"ui.show_elapsed":       fmt.Sprintf("%t", cfg.UI.ShowElapsed),
			"ui.show_icons":         fmt.Sprintf("%t", cfg.UI.ShowIcons),
		}
		for k, v := range defaults {
			if _, err := d.conn.Exec(`INSERT OR IGNORE INTO config (key, value) VALUES (?, ?)`, k, v); err != nil {
				return err
			}
		}
	}

	return nil
}

// importLegacyTasks reads tasks from ~/.config/argus/tasks.json.
func (d *DB) importLegacyTasks() error {
	path := filepath.Join(config.ConfigDir(), "tasks.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	var tasks []*model.Task
	if err := json.Unmarshal(data, &tasks); err != nil {
		return nil // skip corrupted file
	}

	for _, t := range tasks {
		_, err := d.conn.Exec(`INSERT OR IGNORE INTO tasks (id, name, status, project, branch, prompt, backend, worktree, agent_pid, session_id, created_at, started_at, ended_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			t.ID, t.Name, t.Status, t.Project, t.Branch, t.Prompt, t.Backend, t.Worktree, t.AgentPID, t.SessionID,
			formatTime(t.CreatedAt), formatTime(t.StartedAt), formatTime(t.EndedAt))
		if err != nil {
			return err
		}
	}
	return nil
}

// legacyConfig mirrors config.Config for TOML parsing during migration.
type legacyConfig struct {
	Defaults    config.Defaults            `toml:"defaults"`
	Backends    map[string]config.Backend  `toml:"backends"`
	Projects    map[string]config.Project  `toml:"projects"`
	Keybindings config.Keybindings         `toml:"keybindings"`
	UI          legacyUIConfig             `toml:"ui"`
}

type legacyUIConfig struct {
	Theme            string `toml:"theme"`
	ShowElapsed      *bool  `toml:"show_elapsed"`
	ShowIcons        *bool  `toml:"show_icons"`
	CleanupWorktrees *bool  `toml:"cleanup_worktrees,omitempty"`
}

// importLegacyConfig reads config from ~/.config/argus/config.toml.
func (d *DB) importLegacyConfig() error {
	path := filepath.Join(config.ConfigDir(), "config.toml")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	var cfg legacyConfig
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return nil // skip corrupted file
	}

	// Import backends
	for name, b := range cfg.Backends {
		if _, err := d.conn.Exec(`INSERT OR IGNORE INTO backends (name, command, prompt_flag) VALUES (?, ?, ?)`,
			name, b.Command, b.PromptFlag); err != nil {
			return err
		}
	}

	// Import projects
	for name, p := range cfg.Projects {
		if _, err := d.conn.Exec(`INSERT OR IGNORE INTO projects (name, path, branch, backend) VALUES (?, ?, ?, ?)`,
			name, p.Path, p.Branch, p.Backend); err != nil {
			return err
		}
	}

	// Import config values
	kv := map[string]string{}
	if cfg.Defaults.Backend != "" {
		kv["defaults.backend"] = cfg.Defaults.Backend
	}
	if cfg.Keybindings.New != "" {
		kv["keybindings.new"] = cfg.Keybindings.New
	}
	if cfg.Keybindings.Attach != "" {
		kv["keybindings.attach"] = cfg.Keybindings.Attach
	}
	if cfg.Keybindings.Status != "" {
		kv["keybindings.status"] = cfg.Keybindings.Status
	}
	if cfg.Keybindings.Delete != "" {
		kv["keybindings.delete"] = cfg.Keybindings.Delete
	}
	if cfg.Keybindings.Quit != "" {
		kv["keybindings.quit"] = cfg.Keybindings.Quit
	}
	if cfg.Keybindings.Help != "" {
		kv["keybindings.help"] = cfg.Keybindings.Help
	}
	if cfg.Keybindings.Filter != "" {
		kv["keybindings.filter"] = cfg.Keybindings.Filter
	}
	if cfg.Keybindings.Prompt != "" {
		kv["keybindings.prompt"] = cfg.Keybindings.Prompt
	}
	if cfg.Keybindings.Worktree != "" {
		kv["keybindings.worktree"] = cfg.Keybindings.Worktree
	}
	if cfg.UI.Theme != "" {
		kv["ui.theme"] = cfg.UI.Theme
	}
	if cfg.UI.ShowElapsed != nil {
		kv["ui.show_elapsed"] = fmt.Sprintf("%t", *cfg.UI.ShowElapsed)
	}
	if cfg.UI.ShowIcons != nil {
		kv["ui.show_icons"] = fmt.Sprintf("%t", *cfg.UI.ShowIcons)
	}
	if cfg.UI.CleanupWorktrees != nil {
		kv["ui.cleanup_worktrees"] = fmt.Sprintf("%t", *cfg.UI.CleanupWorktrees)
	}

	for k, v := range kv {
		if _, err := d.conn.Exec(`INSERT OR IGNORE INTO config (key, value) VALUES (?, ?)`, k, v); err != nil {
			return err
		}
	}

	return nil
}
