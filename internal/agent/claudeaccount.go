package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/uxlog"
)

// claudeSettingsFile is seeded (copied, never linked) into a new account dir.
const claudeSettingsFile = "settings.json"

// claudeAuthOverrideEnv are auth sources Claude Code ranks above the stored
// /login, so an inherited value would silently bill a different identity than
// the task's explicit account.
var claudeAuthOverrideEnv = []string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "CLAUDE_CODE_OAUTH_TOKEN"}

// BootstrapClaudeConfigDir creates dir (0700), symlinks each inherit entry
// that exists in Claude's default config dir into it, and seeds a one-time
// copy of the default settings.json. A nil inherit uses
// config.DefaultClaudeInherit. Entries already present in dir are never
// touched, and credential/global-state entries are never linked, so the
// operation is idempotent and never reads or writes credentials.
func BootstrapClaudeConfigDir(dir string, inherit []string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating claude account dir %s: %w", dir, err)
	}
	if inherit == nil {
		inherit = config.DefaultClaudeInherit()
	}
	src := config.DefaultClaudeDir()
	if src == "" {
		return nil
	}
	for _, name := range inherit {
		if name == "" || name != filepath.Base(name) || config.NeverInheritClaude(name) {
			uxlog.Log("[account] skip inherit entry %q", name)
			continue
		}
		from := filepath.Join(src, name)
		if _, err := os.Stat(from); err != nil {
			continue
		}
		to := filepath.Join(dir, name)
		if _, err := os.Lstat(to); err == nil {
			continue
		}
		if err := os.Symlink(from, to); err != nil {
			return fmt.Errorf("linking %s into claude account dir: %w", name, err)
		}
	}
	return seedClaudeSettings(src, dir)
}

// seedClaudeSettings copies <src>/settings.json into dir when dir has no
// settings.json entry of any kind. A copy (not a symlink) keeps Claude's own
// settings writes inside the account; O_EXCL refuses an existing path,
// including a symlink, so nothing is ever overwritten or written through.
// Auth overrides (apiKeyHelper, credential env entries) are dropped so the
// copy cannot outrank the account's own /login.
func seedClaudeSettings(src, dir string) error {
	to := filepath.Join(dir, claudeSettingsFile)
	if _, err := os.Lstat(to); err == nil {
		return nil
	}
	raw, err := os.ReadFile(filepath.Join(src, claudeSettingsFile))
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			uxlog.Log("[account] settings seed skipped: %v", err)
		}
		return nil
	}
	out, dropped, err := stripClaudeSettingsAuth(raw)
	if err != nil {
		uxlog.Log("[account] settings seed skipped: unparsable %s: %v", claudeSettingsFile, err)
		return nil
	}
	if len(dropped) > 0 {
		uxlog.Log("[account] settings seed dropped auth overrides: %s", strings.Join(dropped, ","))
	}
	f, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, fs.ErrExist) {
			return nil
		}
		return fmt.Errorf("seeding claude account settings: %w", err)
	}
	if _, werr := f.Write(out); werr != nil {
		_ = f.Close()
		return fmt.Errorf("seeding claude account settings: %w", werr)
	}
	if cerr := f.Close(); cerr != nil {
		return fmt.Errorf("seeding claude account settings: %w", cerr)
	}
	uxlog.Log("[account] seeded %s into %s", claudeSettingsFile, dir)
	return nil
}

// stripClaudeSettingsAuth removes auth-overriding keys from a settings.json
// document, returning the original bytes untouched when nothing is dropped.
// dropped holds key names only.
func stripClaudeSettingsAuth(raw []byte) (out []byte, dropped []string, err error) {
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, nil, err
	}
	if _, ok := doc["apiKeyHelper"]; ok {
		delete(doc, "apiKeyHelper")
		dropped = append(dropped, "apiKeyHelper")
	}
	// The env block can carry arbitrary secrets (not just Anthropic auth), so it
	// is never copied into another account's dir.
	if _, ok := doc["env"]; ok {
		delete(doc, "env")
		dropped = append(dropped, "env")
	}
	if len(dropped) == 0 {
		return raw, nil, nil
	}
	b, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, nil, err
	}
	return append(b, '\n'), dropped, nil
}

// resolveSpawnClaudeConfigDir returns the explicit Claude config dir a Claude
// task spawns under, bootstrapping it, or "" for the default account and for
// non-Claude backends. An unknown/removed stored account is an error: a spawn
// must never silently fall back to the default identity.
func resolveSpawnClaudeConfigDir(task *model.Task, cfg config.Config, isClaude bool) (string, error) {
	if !isClaude || task.Account == "" || task.Account == config.DefaultAccountName {
		return "", nil
	}
	dir, explicit, err := cfg.ClaudeConfigDir(task.Account)
	if err != nil {
		uxlog.Log("[account] task %q: %v", task.ID, err)
		return "", fmt.Errorf("claude account %q: %w", task.Account, err)
	}
	if !explicit {
		return "", nil
	}
	if err := BootstrapClaudeConfigDir(dir, cfg.Accounts[task.Account].Inherit); err != nil {
		uxlog.Log("[account] task %q bootstrap failed: %v", task.ID, err)
		return "", fmt.Errorf("claude account %q: %w", task.Account, err)
	}
	return dir, nil
}

// spawnAuthStrip lists the inherited auth-override variables to drop for a
// task on an explicit Claude and/or Codex account.
func spawnAuthStrip(claudeExplicit, codexExplicit bool) []string {
	var names []string
	if claudeExplicit {
		names = append(names, claudeAuthOverrideEnv...)
	}
	if codexExplicit {
		names = append(names, codexAuthOverrideEnv...)
	}
	return names
}

// spawnBaseEnv filters the inherited environment for a spawned session. An
// inherited CLAUDE_CONFIG_DIR is always dropped (the task's own account
// decides it), as is every name in authStrip (the auth overrides a backend
// ranks above an explicit account's login). Backend env_vars and the op
// bootstrap are appended after this, so explicit mappings still win.
func spawnBaseEnv(environ []string, authStrip []string, taskID string) []string {
	strip := map[string]bool{"CLAUDE_CONFIG_DIR": true}
	for _, k := range authStrip {
		strip[k] = true
	}
	out := make([]string, 0, len(environ))
	removed := map[string]bool{}
	for _, kv := range environ {
		name, _, _ := strings.Cut(kv, "=")
		if strip[name] {
			removed[name] = true
			continue
		}
		out = append(out, kv)
	}
	if len(removed) > 0 {
		names := make([]string, 0, len(removed))
		for n := range removed {
			names = append(names, n)
		}
		sort.Strings(names)
		uxlog.Log("[account] task %q: stripped inherited env %s", taskID, strings.Join(names, ","))
	}
	return out
}

// sandboxWithClaudeConfigDir appends the task's own account Claude dir to
// ExtraWrite when it lies outside the default Claude dir (already writable),
// so /login token persistence inside the sandbox does not EPERM.
func sandboxWithClaudeConfigDir(sc config.SandboxConfig, dir string) config.SandboxConfig {
	if dir == "" {
		return sc
	}
	def := config.DefaultClaudeDir()
	if dir == def || (def != "" && strings.HasPrefix(dir, def+string(filepath.Separator))) {
		return sc
	}
	sc.ExtraWrite = append(append([]string(nil), sc.ExtraWrite...), dir)
	return sc
}

// claudeConfigDirFromEnv returns the CLAUDE_CONFIG_DIR a session was spawned
// with (last assignment wins, as in exec.Cmd), or "" for the default account.
func claudeConfigDirFromEnv(env []string) string {
	dir := ""
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, "CLAUDE_CONFIG_DIR="); ok {
			dir = v
		}
	}
	return dir
}
