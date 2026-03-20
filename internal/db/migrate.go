package db

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/drn/argus/internal/config"
)

// migrate checks if the database has been initialized. If not, it seeds defaults.
func (d *DB) migrate() error {
	var version int
	err := d.conn.QueryRow(`SELECT version FROM schema_version LIMIT 1`).Scan(&version)
	if err == nil {
		return nil // already migrated
	}
	if !errors.Is(err, sql.ErrNoRows) {
		var count int
		if countErr := d.conn.QueryRow(`SELECT COUNT(*) FROM schema_version`).Scan(&count); countErr != nil {
			return fmt.Errorf("checking schema version: %w", err)
		}
		if count > 0 {
			return nil
		}
	}

	if err := d.seedDefaults(); err != nil {
		return err
	}

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
			if _, err := d.conn.Exec(`INSERT INTO backends (name, command, prompt_flag) VALUES (?, ?, ?)`,
				name, b.Command, b.PromptFlag); err != nil {
				return err
			}
		} else if err == nil && (existing == "echo" || existing == "cat" || existing == "true") {
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
			"kb.http_port":          fmt.Sprintf("%d", cfg.KB.HTTPPort),
			"kb.metis_vault_path":   config.DefaultMetisVaultPath(),
			"kb.argus_vault_path":   config.DefaultArgusVaultPath(),
			"kb.auto_create_tasks":  fmt.Sprintf("%t", cfg.KB.AutoCreateTasks),
		}
		for k, v := range defaults {
			if _, err := d.conn.Exec(`INSERT OR IGNORE INTO config (key, value) VALUES (?, ?)`, k, v); err != nil {
				return err
			}
		}
	}

	return nil
}

// fixupBackends runs on every Open and corrects known-outdated backend
// configurations. This is separate from seedDefaults (which only runs during
// migration) so that improvements to the default command propagate to
// existing databases on the next startup.
func (d *DB) fixupBackends() error {
	cfg := config.DefaultConfig()

	for name, want := range cfg.Backends {
		var command, promptFlag string
		err := d.conn.QueryRow(
			`SELECT command, prompt_flag FROM backends WHERE name=?`, name,
		).Scan(&command, &promptFlag)
		if err != nil {
			continue // backend doesn't exist — seedDefaults handles insertion
		}

		needsUpdate := false

		// Fix: claude backend is missing --dangerously-skip-permissions.
		if name == "claude" && !strings.Contains(command, "--dangerously-skip-permissions") {
			needsUpdate = true
		}

		// Migrate: add --permission-mode plan so agents default to read-only plan mode.
		// Appends to existing command (preserving user customizations) rather than replacing.
		if name == "claude" && !strings.Contains(command, "--permission-mode") {
			appended := command + " --permission-mode plan"
			if _, err := d.conn.Exec(
				`UPDATE backends SET command=? WHERE name=?`, appended, name,
			); err != nil {
				return err
			}
			command = appended // update local var so subsequent checks see the new command
		}

		// Fix: codex backend uses old flags (--yolo or --full-auto) instead of
		// --dangerously-bypass-approvals-and-sandbox.
		// Scoped to name=="codex" — users who renamed their codex backend must update manually.
		if name == "codex" && !strings.Contains(command, "--dangerously-bypass-approvals-and-sandbox") {
			needsUpdate = true
		}

		// Fix: prompt_flag is "-p" (print/non-interactive mode) when the
		// default is empty (interactive mode).
		if promptFlag == "-p" && want.PromptFlag == "" {
			needsUpdate = true
		}

		// Fix: command still has --worktree flag from when Argus delegated
		// worktree creation to Claude Code. Argus now creates worktrees
		// itself and sets cmd.Dir instead.
		if strings.Contains(command, "--worktree") || strings.Contains(command, " -w") {
			needsUpdate = true
		}

		if needsUpdate {
			if _, err := d.conn.Exec(
				`UPDATE backends SET command=?, prompt_flag=? WHERE name=?`,
				want.Command, want.PromptFlag, name,
			); err != nil {
				return err
			}
		}
	}

	return nil
}
