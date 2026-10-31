package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/drn/argus/internal/backendkind"
)

// DefaultAccountName is the reserved account name meaning each tool's own
// default state directory (~/.claude, ~/.codex): no CLAUDE_CONFIG_DIR or
// CODEX_HOME is exported.
const DefaultAccountName = "default"

// AutoAccountName is the reserved account value meaning "pick the account with
// the most usage headroom at task creation" (internal/accountusage). It is
// accepted wherever a new task's account is selected but never stored on a
// task, and it cannot be defined as an [accounts.auto] table.
const AutoAccountName = "auto"

// IsAutoAccount reports whether name is the reserved auto-selection value.
func IsAutoAccount(name string) bool { return name == AutoAccountName }

// Account is one named identity (e.g. work / personal) that can carry a Claude
// Code config dir and/or a Codex home, each an isolated login + state dir.
type Account struct {
	Label           string `toml:"label"`
	ClaudeConfigDir string `toml:"claude_config_dir"`
	CodexHome       string `toml:"codex_home"`
	// Inherit lists ~/.claude entries symlinked into ClaudeConfigDir. nil means
	// DefaultClaudeInherit; an explicit empty list inherits nothing.
	Inherit []string `toml:"inherit"`
}

// DefaultClaudeInherit is the shared-entry list used when Inherit is unset.
// settings.json is deliberately absent: it is seeded as a one-time copy so
// Claude's own settings writes stay inside the account instead of replacing a
// shared symlink.
func DefaultClaudeInherit() []string {
	return []string{"CLAUDE.md", "skills", "commands", "agents"}
}

// NeverInheritClaude are entries that must never be symlinked between accounts.
func NeverInheritClaude(name string) bool {
	// Lower-cased: default APFS is case-insensitive, so "Projects" is the real dir.
	lower := strings.ToLower(name)
	switch lower {
	case ".claude.json", "projects", "plugins":
		return true
	}
	return strings.HasPrefix(lower, ".credentials")
}

// ExpandHome expands a leading "~" or "~/" using the user's home directory.
func ExpandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
	}
	return p
}

// DefaultClaudeDir returns Claude's own default config directory.
func DefaultClaudeDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude")
}

// DefaultCodexHome returns Codex's own default home directory.
func DefaultCodexHome() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".codex")
}

func isDefaultAccount(name string) bool {
	return name == "" || name == DefaultAccountName
}

// validAccountDir reports whether a configured dir is usable once expanded.
func validAccountDir(raw string) bool {
	dir := ExpandHome(strings.TrimSpace(raw))
	return dir != "" && filepath.IsAbs(dir)
}

// ValidateAccount reports why name is not a usable account, or nil. An account
// must define at least one of claude_config_dir / codex_home, and every
// defined dir must be absolute or ~-prefixed.
func (c Config) ValidateAccount(name string) error {
	if isDefaultAccount(name) {
		return nil
	}
	if IsAutoAccount(name) {
		if _, defined := c.Accounts[name]; defined {
			return fmt.Errorf("account %q is reserved for usage-based selection and cannot be defined", name)
		}
		return nil
	}
	acct, ok := c.Accounts[name]
	if !ok {
		return fmt.Errorf("unknown account %q", name)
	}
	claude := strings.TrimSpace(acct.ClaudeConfigDir)
	codex := strings.TrimSpace(acct.CodexHome)
	if claude == "" && codex == "" {
		return fmt.Errorf("account %q: define claude_config_dir and/or codex_home", name)
	}
	if claude != "" && !validAccountDir(claude) {
		return fmt.Errorf("account %q: claude_config_dir must be absolute or ~-prefixed", name)
	}
	if codex != "" && !validAccountDir(codex) {
		return fmt.Errorf("account %q: codex_home must be absolute or ~-prefixed", name)
	}
	if err := checkAccountDirSafe(name, "claude_config_dir", claude, DefaultClaudeDir()); err != nil {
		return err
	}
	return checkAccountDirSafe(name, "codex_home", codex, DefaultCodexHome())
}

// checkAccountDirSafe rejects a dir that is the tool's own default (that is the
// "default" account) or that is $HOME, "/", or an ancestor of $HOME, since the
// account dir is granted sandbox write access.
func checkAccountDirSafe(name, key, raw, toolDefault string) error {
	if raw == "" {
		return nil
	}
	dir := filepath.Clean(ExpandHome(raw))
	if toolDefault != "" && dir == filepath.Clean(toolDefault) {
		return fmt.Errorf("account %q: %s is the tool default; use the %q account instead", name, key, DefaultAccountName)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	home = filepath.Clean(home)
	if dir == home || dir == string(filepath.Separator) || strings.HasPrefix(home, dir+string(filepath.Separator)) {
		return fmt.Errorf("account %q: %s must be a dedicated directory, not %s", name, key, dir)
	}
	// The dir is granted sandbox write access, so never a place holding other secrets or Argus state.
	for _, sensitive := range []string{".ssh", ".argus", ".aws", ".gnupg", ".kube", "Library"} {
		root := filepath.Join(home, sensitive)
		if dir == root || strings.HasPrefix(dir, root+string(filepath.Separator)) {
			return fmt.Errorf("account %q: %s must not be inside ~/%s", name, key, sensitive)
		}
	}
	return nil
}

// ClaudeConfigDir resolves an account to its Claude config directory. explicit
// is false for the default account (no CLAUDE_CONFIG_DIR should be exported).
// A valid account without claude_config_dir is an error.
func (c Config) ClaudeConfigDir(name string) (dir string, explicit bool, err error) {
	if isDefaultAccount(name) {
		return DefaultClaudeDir(), false, nil
	}
	if IsAutoAccount(name) {
		return "", false, fmt.Errorf("account %q must be resolved to a concrete account first", name)
	}
	if err := c.ValidateAccount(name); err != nil {
		return "", false, err
	}
	raw := strings.TrimSpace(c.Accounts[name].ClaudeConfigDir)
	if raw == "" {
		return "", false, fmt.Errorf("account %q has no claude_config_dir", name)
	}
	return filepath.Clean(ExpandHome(raw)), true, nil
}

// CodexHome resolves an account to its Codex home directory. explicit is false
// for the default account (no CODEX_HOME should be exported). A valid account
// without codex_home is an error.
func (c Config) CodexHome(name string) (dir string, explicit bool, err error) {
	if isDefaultAccount(name) {
		return DefaultCodexHome(), false, nil
	}
	if IsAutoAccount(name) {
		return "", false, fmt.Errorf("account %q must be resolved to a concrete account first", name)
	}
	if err := c.ValidateAccount(name); err != nil {
		return "", false, err
	}
	raw := strings.TrimSpace(c.Accounts[name].CodexHome)
	if raw == "" {
		return "", false, fmt.Errorf("account %q has no codex_home", name)
	}
	return filepath.Clean(ExpandHome(raw)), true, nil
}

// AccountSupports reports whether account name can run a task on the backend
// with the given command. The default account supports every backend; a named
// account supports Claude backends only with claude_config_dir and Codex
// backends only with codex_home, and no other backend (pi, opencode,
// antigravity, custom). "auto" supports every backend: its candidates always
// include "default".
func (c Config) AccountSupports(name, backendCommand string) bool {
	if isDefaultAccount(name) || (IsAutoAccount(name) && c.ValidateAccount(name) == nil) {
		return true
	}
	if c.ValidateAccount(name) != nil {
		return false
	}
	acct := c.Accounts[name]
	switch backendkind.Of(backendCommand) {
	case backendkind.Claude:
		return strings.TrimSpace(acct.ClaudeConfigDir) != ""
	case backendkind.Codex:
		return strings.TrimSpace(acct.CodexHome) != ""
	}
	return false
}

// ResolveAccount picks the account for a new task: explicit selection, else
// the project's default, else the global default, else "default". Project /
// global names that are not valid accounts are ignored; the explicit value is
// returned as given so the caller can reject an unknown name.
func (c Config) ResolveAccount(explicit, project string) string {
	if explicit != "" {
		return explicit
	}
	if p := c.ProjectAccounts[project]; p != "" && c.ValidateAccount(p) == nil {
		return p
	}
	if g := c.DefaultAccount; g != "" && c.ValidateAccount(g) == nil {
		return g
	}
	return DefaultAccountName
}

// AccountNames returns "default" followed by configured account names
// (sorted), skipping invalid ones.
func (c Config) AccountNames() []string {
	names := make([]string, 0, len(c.Accounts))
	for n := range c.Accounts {
		if !isDefaultAccount(n) && !IsAutoAccount(n) && c.ValidateAccount(n) == nil {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	return append([]string{DefaultAccountName}, names...)
}

// AccountNamesFor is AccountNames filtered to accounts that support the
// backend with the given command ("default" is always first).
func (c Config) AccountNamesFor(backendCommand string) []string {
	all := c.AccountNames()
	out := make([]string, 0, len(all))
	for _, n := range all {
		if c.AccountSupports(n, backendCommand) {
			out = append(out, n)
		}
	}
	return out
}
