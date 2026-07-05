package config

import (
	"os"
	"path/filepath"
)

// Config is the top-level configuration.
type Config struct {
	Defaults    Defaults           `toml:"defaults"`
	Backends    map[string]Backend `toml:"backends"`
	Projects    map[string]Project `toml:"projects"`
	Keybindings Keybindings        `toml:"keybindings"`
	UI          UIConfig           `toml:"ui"`
	Sandbox     SandboxConfig      `toml:"sandbox"`
	KB          KBConfig           `toml:"kb"`
	API         APIConfig          `toml:"api"`
	Hera        HeraConfig         `toml:"hera"`
	Supervisor  SupervisorConfig   `toml:"supervisor"`
	Argus       ArgusConfig        `toml:"argus"`
}

// SupervisorConfig controls whether the daemon drives agent PTYs through the
// out-of-process session-supervisor (see context/plans/session-supervisor.md)
// instead of its own in-process runner.
type SupervisorConfig struct {
	// Enabled defaults to TRUE (absent key ⇒ on, as of P4 — mirroring how
	// hera.enabled defaults on). When true, the daemon connects to the
	// session-supervisor over supervisor.sock (auto-starting it if absent) and
	// proxies every session through it, so the daemon can bounce without
	// interrupting agents. An explicit "false" (DB or config.toml) is the
	// ROLLBACK: it restores the in-process runner path — byte-identical to
	// pre-P2 — which is RETAINED for one release per the migration plan, NOT
	// deleted. config.toml wins over the DB when both are set (standard overlay
	// rule; mirrors kb.enabled / api.enabled / hera.enabled).
	Enabled bool `toml:"enabled"`
}

// HeraConfig controls the native Hera multi-agent coordination view.
type HeraConfig struct {
	// Enabled defaults to true (absent key ⇒ on). Set to false to disable
	// the native Hera view and fall back to the legacy DAG-only second tab.
	// config.toml wins over the DB when both are set (standard overlay rule).
	Enabled bool `toml:"enabled"`

	// CoordinatorContextBudget is the token count at/above which the
	// context-budget Stop hook (argus hera-report) begins nudging a
	// coordinator to recycle. Defaults to 200000.
	CoordinatorContextBudget int `toml:"coordinator_context_budget"`
}

// ArgusConfig holds settings for self-updating the Argus binary.
type ArgusConfig struct {
	SourcePath string `toml:"source_path"` // local clone of the Argus repo for go install
}

// APIConfig controls the HTTP REST API for remote control.
type APIConfig struct {
	Enabled  bool `toml:"enabled"`   // default false — must be turned on in settings
	HTTPPort int  `toml:"http_port"` // default 7743
}

// KBConfig controls the knowledge base server.
type KBConfig struct {
	Enabled        bool   `toml:"enabled"`          // default false — must be turned on in settings
	HTTPPort       int    `toml:"http_port"`        // default 7742
	MetisVaultPath string `toml:"metis_vault_path"` // Obsidian vault for KB indexing (Metis)
}

// iCloudObsidianBase is the default iCloud-synced Obsidian vault parent directory.
const iCloudObsidianBase = "Library/Mobile Documents/iCloud~md~obsidian/Documents"

// DefaultMetisVaultPath returns the default iCloud path for the Metis (KB) vault.
func DefaultMetisVaultPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, iCloudObsidianBase, "Metis")
}

type Defaults struct {
	Backend string `toml:"backend"`
	// ShareProject is the project name preselected in the New Task form when
	// the PWA share target lands a payload (iOS/Android share sheet → /share).
	// Empty falls back to the currently expanded project folder.
	ShareProject string `toml:"share_project"`
	// PermissionMode controls the permission flags injected into Claude-style
	// backend commands by agent.BuildCmd (one of the PermissionModes values).
	// It is NOT baked into the backend command string — see PermissionModeFlags.
	PermissionMode string `toml:"permission_mode"`
}

// PermissionMode values control how Claude-style sessions launch. The flags are
// injected at command-build time (agent.BuildCmd) rather than stored in the
// backend command, so the mode is a single configurable knob.
const (
	PermissionModeDefault     = "default"
	PermissionModeAcceptEdits = "acceptEdits"
	PermissionModePlan        = "plan"
	// PermissionModeBypassAllow starts in plan mode but adds bypassPermissions
	// to the Shift+Tab cycle via --allow-dangerously-skip-permissions (the
	// documented way to keep "dangerously skip permissions" reachable without
	// activating it at launch).
	PermissionModeBypassAllow = "bypass-allow"
	// PermissionModeBypassActive launches directly in bypassPermissions mode.
	PermissionModeBypassActive = "bypass-active"
)

// PermissionModes is the ordered list of selectable permission modes, used by
// the settings UI to cycle through values.
var PermissionModes = []string{
	PermissionModeDefault,
	PermissionModeAcceptEdits,
	PermissionModePlan,
	PermissionModeBypassAllow,
	PermissionModeBypassActive,
}

// PermissionModeFlags returns the Claude CLI flags for a permission mode.
// Returns "" for an empty or unrecognized mode (no flags injected).
func PermissionModeFlags(mode string) string {
	switch mode {
	case PermissionModeDefault:
		return "--permission-mode default"
	case PermissionModeAcceptEdits:
		return "--permission-mode acceptEdits"
	case PermissionModePlan:
		return "--permission-mode plan"
	case PermissionModeBypassAllow:
		return "--allow-dangerously-skip-permissions --permission-mode plan"
	case PermissionModeBypassActive:
		return "--dangerously-skip-permissions"
	default:
		return ""
	}
}

// PermissionModeLabel returns a human-readable label for the settings UI.
func PermissionModeLabel(mode string) string {
	switch mode {
	case PermissionModeDefault:
		return "Default (prompt per action)"
	case PermissionModeAcceptEdits:
		return "Accept edits"
	case PermissionModePlan:
		return "Plan (read-only)"
	case PermissionModeBypassAllow:
		return "Plan + bypass reachable (Shift+Tab)"
	case PermissionModeBypassActive:
		return "Bypass permissions (active)"
	default:
		return mode
	}
}

type Backend struct {
	Command    string `toml:"command"`
	PromptFlag string `toml:"prompt_flag"`
	// Model is the default model for this backend, injected by agent.BuildCmd
	// as `--model <value>` for known backend CLIs (claude, codex, pi). Empty
	// means the CLI's own default. A per-task model (model.Task.Model) takes
	// precedence over this value.
	Model string `toml:"model"`
	// Models is an optional list of selectable model identifiers offered for
	// this backend in the new-task model selector. Empty falls back to the
	// built-in agent.KnownModels list for the backend command. This is a
	// config.toml overlay field only (no DB column); it overrides the built-in
	// list for power users who run models the curated list does not name.
	Models []string `toml:"models"`
}

// ProjectSandboxConfig holds per-project sandbox overrides.
// A nil Enabled means "inherit from global"; non-nil overrides the global setting.
//
// The DB projects table is the PRIMARY source for these per-project overrides.
// The fields carry no explicit `toml:` tags, but note this does NOT keep them
// out of TOML: the parent Project.Sandbox field is `toml:"sandbox"`, so the
// config.toml overlay (config.FileLoader) will decode a
// [projects.<name>.sandbox] table into this struct, matching each field by its
// lowercased Go name (enabled, denyread, extrawrite, allowappleevents). That is
// acceptable as part of the deliberate "config.toml overrides everything" layer
// — but if you add explicit tags here, pick snake_case to match SandboxConfig's
// global fields (deny_read, extra_write, …) so the file syntax is consistent.
type ProjectSandboxConfig struct {
	Enabled    *bool    // nil = inherit global; true/false = override
	DenyRead   []string // additional paths appended to the global deny_read list
	ExtraWrite []string // additional paths appended to the global extra_write list
	// AllowAppleEvents are CFBundleIdentifiers (e.g. "com.apple.iChat") allowed
	// as appleevent-send destinations from sandboxed agents for this project.
	// Appended to the global allow_apple_events list. The macOS Seatbelt
	// (deny default) profile blocks all AppleEvent dispatch by default, so
	// scripting Messages/Finder/etc. from inside a sandboxed agent requires
	// an explicit destination allow rule — TCC alone is not sufficient.
	AllowAppleEvents []string
}

type Project struct {
	Path    string               `toml:"path"`
	Branch  string               `toml:"branch"`
	Backend string               `toml:"backend"`
	Sandbox ProjectSandboxConfig `toml:"sandbox"`
}

// Keybindings holds user keybinding OVERRIDES, scoped by TUI context. Each inner
// map is action-id → keyspec string (e.g. {"new": "x", "fork": "ctrl+g"}); an
// absent entry keeps the built-in default. The defaults themselves live in
// internal/tui/keymap (DefaultKeymap), NOT here — this struct carries only what
// the user changed. Unknown action ids and unknown context tables are ignored
// (forward-compatible, matching FileLoader's lenient decode). config.toml is the
// sole source for these overrides; there are no DB-backed keybinding rows.
type Keybindings struct {
	Global    map[string]string `toml:"global"`
	TaskList  map[string]string `toml:"tasklist"`
	Agent     map[string]string `toml:"agent"`
	FilePanel map[string]string `toml:"filepanel"`
	Diff      map[string]string `toml:"diff"`
	Settings  map[string]string `toml:"settings"`
	HeraRail  map[string]string `toml:"hera_rail"`
}

type UIConfig struct {
	Theme            string `toml:"theme"`
	ShowElapsed      bool   `toml:"show_elapsed"`
	ShowIcons        bool   `toml:"show_icons"`
	CleanupWorktrees *bool  `toml:"cleanup_worktrees,omitempty"`
	SpinnerStyle     string `toml:"spinner_style"`
	// DefaultAgentZoom controls the resting agent-view layout: true (the
	// default) opens single-pane/zoomed with the side panels collapsed; false
	// opens the 1:3:1 three-pane layout. Ctrl+Z still toggles at runtime.
	DefaultAgentZoom bool `toml:"default_agent_zoom"`
}

// SandboxConfig controls OS-level sandboxing of agent processes.
type SandboxConfig struct {
	Enabled    bool     `toml:"enabled"`
	DenyRead   []string `toml:"deny_read"`
	ExtraWrite []string `toml:"extra_write"`
	// AllowAppleEvents lists CFBundleIdentifiers permitted as
	// appleevent-send destinations from sandboxed agents. Each entry is
	// emitted as (allow appleevent-send (appleevent-destination "<id>"))
	// in the generated SBPL profile. Required to script Messages.app,
	// Finder, etc. — the (deny default) base profile blocks AppleEvents
	// regardless of TCC grants.
	AllowAppleEvents []string `toml:"allow_apple_events"`
}

// ShouldCleanupWorktrees returns whether worktrees should be auto-removed on task delete.
// Defaults to true if not explicitly set.
func (u UIConfig) ShouldCleanupWorktrees() bool {
	if u.CleanupWorktrees == nil {
		return true
	}
	return *u.CleanupWorktrees
}

// DefaultConfig returns a config with sensible defaults.
func DefaultConfig() Config {
	return Config{
		Defaults: Defaults{Backend: "claude", PermissionMode: PermissionModeBypassActive},
		Backends: map[string]Backend{
			"claude": {
				// Permission flags are injected by agent.BuildCmd from
				// Defaults.PermissionMode — not baked into the command.
				Command:    "claude",
				PromptFlag: "",
			},
			"codex": {
				Command:    "codex --dangerously-bypass-approvals-and-sandbox",
				PromptFlag: "",
			},
			"pi": {
				Command:    "pi",
				PromptFlag: "",
			},
			"opencode": {
				// opencode is a capture-style backend (no start-time
				// --session-id; it mints its own ses_… ID). The TUI takes its
				// initial prompt via --prompt, so the prompt rides the
				// PromptFlag path. Permissions are deliberately left to the
				// user's own opencode config — the bare command is the default.
				Command:    "opencode",
				PromptFlag: "--prompt",
			},
		},
		Projects:    make(map[string]Project),
		Keybindings: DefaultKeybindings(),
		UI: UIConfig{
			Theme:            "default",
			ShowElapsed:      true,
			ShowIcons:        true,
			SpinnerStyle:     "progress",
			DefaultAgentZoom: true,
		},
		KB: KBConfig{
			HTTPPort: 7742,
		},
		API: APIConfig{
			HTTPPort: 7743,
		},
		Hera: HeraConfig{
			Enabled:                  true, // default on; only explicit "false" in DB/toml disables it
			CoordinatorContextBudget: 200000,
		},
		Supervisor: SupervisorConfig{
			// Default ON as of P4: agents run under the out-of-process
			// session-supervisor so the daemon can bounce without interrupting
			// them. Only an explicit "false" in DB/toml rolls back to the
			// retained in-process runner path.
			Enabled: true,
		},
	}
}

// DefaultKeybindings returns the empty override set. Built-in default bindings
// live in internal/tui/keymap (DefaultKeymap); this layer carries only the
// user's config.toml overrides, so the default is "no overrides".
func DefaultKeybindings() Keybindings {
	return Keybindings{}
}
