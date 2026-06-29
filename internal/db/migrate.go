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
		envVars, merr := marshalEnvVars(b.EnvVars)
		if merr != nil {
			return merr
		}
		var existing string
		err := d.conn.QueryRow(`SELECT command FROM backends WHERE name=?`, name).Scan(&existing)
		if err == sql.ErrNoRows {
			if _, err := d.conn.Exec(`INSERT INTO backends (name, command, prompt_flag, env_vars) VALUES (?, ?, ?, ?)`,
				name, b.Command, b.PromptFlag, envVars); err != nil {
				return err
			}
		} else if err == nil && (existing == "echo" || existing == "cat" || existing == "true") {
			if _, err := d.conn.Exec(`UPDATE backends SET command=?, prompt_flag=?, env_vars=? WHERE name=?`,
				b.Command, b.PromptFlag, envVars, name); err != nil {
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
			"ui.default_agent_zoom": fmt.Sprintf("%t", cfg.UI.DefaultAgentZoom),
			"kb.http_port":          fmt.Sprintf("%d", cfg.KB.HTTPPort),
			"kb.metis_vault_path":   config.DefaultMetisVaultPath(),
			"api.http_port":         fmt.Sprintf("%d", cfg.API.HTTPPort),
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
// existing databases on the next startup. It also inserts missing default
// backends so users of pre-existing databases pick up newly-shipped backends
// (e.g. when "pi" was added) without needing a schema bump.
func (d *DB) fixupBackends() error {
	cfg := config.DefaultConfig()

	for name, want := range cfg.Backends {
		wantEnvVars, merr := marshalEnvVars(want.EnvVars)
		if merr != nil {
			return merr
		}
		var command, promptFlag, envVars string
		err := d.conn.QueryRow(
			`SELECT command, prompt_flag, env_vars FROM backends WHERE name=?`, name,
		).Scan(&command, &promptFlag, &envVars)
		if errors.Is(err, sql.ErrNoRows) {
			if _, ierr := d.conn.Exec(
				`INSERT INTO backends (name, command, prompt_flag, env_vars) VALUES (?, ?, ?, ?)`,
				name, want.Command, want.PromptFlag, wantEnvVars,
			); ierr != nil {
				return ierr
			}
			continue
		}
		if err != nil {
			continue
		}

		// Propagate a newly-shipped default credential mapping to a pre-existing
		// row that has none yet (e.g. the codex OPENAI_API_KEY -> HERA_OPENAI
		// mapping). Guarded on the stored value being empty so a user who
		// customized (or deliberately cleared) the mapping is never clobbered.
		// The mapping holds no secret value — only target -> source descriptors.
		if envVars == "" && wantEnvVars != "" {
			if _, uerr := d.conn.Exec(
				`UPDATE backends SET env_vars=? WHERE name=?`, wantEnvVars, name,
			); uerr != nil {
				return uerr
			}
		}

		needsUpdate := false

		// Migrate: permission flags used to be baked into the claude command
		// (e.g. "claude --dangerously-skip-permissions --permission-mode plan").
		// They are now injected by agent.BuildCmd from defaults.permission_mode,
		// which falls back to bypass-active (--dangerously-skip-permissions) when
		// unconfigured. Strip the baked flags once so the injected setting becomes
		// the single source of truth. We intentionally do NOT seed a config value:
		// leaving the key unset means the launched command reverts to the bypass
		// default, undoing the previously force-added plan mode. Idempotent —
		// once stripped there are no tokens to remove.
		if name == "claude" && commandHasPermissionTokens(command) {
			stripped := stripPermissionTokens(command)
			if stripped != command {
				if _, uerr := d.conn.Exec(
					`UPDATE backends SET command=? WHERE name=?`, stripped, name,
				); uerr != nil {
					return uerr
				}
				command = stripped // subsequent checks see the stripped command
			}
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

// commandHasPermissionTokens reports whether a backend command contains any
// baked-in Claude permission flag that the new injection model now owns.
func commandHasPermissionTokens(command string) bool {
	return strings.Contains(command, "--permission-mode") ||
		strings.Contains(command, "--dangerously-skip-permissions") ||
		strings.Contains(command, "--allow-dangerously-skip-permissions")
}

// stripPermissionTokens removes permission-related flags (and the value token
// following a bare --permission-mode) from a command, preserving all other
// flags. Used once during migration to hand permission control to the injected
// defaults.permission_mode setting.
func stripPermissionTokens(command string) string {
	fields := strings.Fields(command)
	out := make([]string, 0, len(fields))
	for i := 0; i < len(fields); i++ {
		f := fields[i]
		switch {
		case f == "--dangerously-skip-permissions",
			f == "--allow-dangerously-skip-permissions":
			continue
		case f == "--permission-mode":
			i++ // also drop the following value token (e.g. "plan")
			continue
		case strings.HasPrefix(f, "--permission-mode="):
			continue
		default:
			out = append(out, f)
		}
	}
	return strings.Join(out, " ")
}
