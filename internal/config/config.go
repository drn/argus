package config

// Config is the top-level configuration.
type Config struct {
	Defaults    Defaults           `toml:"defaults"`
	Backends    map[string]Backend `toml:"backends"`
	Projects    map[string]Project `toml:"projects"`
	Keybindings Keybindings        `toml:"keybindings"`
	UI          UIConfig           `toml:"ui"`
	Sandbox     SandboxConfig      `toml:"sandbox"`
}

type Defaults struct {
	Backend string `toml:"backend"`
}

type Backend struct {
	Command    string `toml:"command"`
	PromptFlag string `toml:"prompt_flag"`
}

type Project struct {
	Path    string `toml:"path"`
	Branch  string `toml:"branch"`
	Backend string `toml:"backend"`
}

type Keybindings struct {
	New      string `toml:"new"`
	Attach   string `toml:"attach"`
	Status   string `toml:"status"`
	Delete   string `toml:"delete"`
	Quit     string `toml:"quit"`
	Help     string `toml:"help"`
	Filter   string `toml:"filter"`
	Prompt   string `toml:"prompt"`
	Worktree string `toml:"worktree"`
}

type UIConfig struct {
	Theme            string `toml:"theme"`
	ShowElapsed      bool   `toml:"show_elapsed"`
	ShowIcons        bool   `toml:"show_icons"`
	CleanupWorktrees *bool  `toml:"cleanup_worktrees,omitempty"`
}

// SandboxConfig controls OS-level sandboxing of agent processes.
type SandboxConfig struct {
	Enabled        bool     `toml:"enabled"`
	AllowedDomains []string `toml:"allowed_domains"`
	DenyRead       []string `toml:"deny_read"`
	ExtraWrite     []string `toml:"extra_write"`
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
		Defaults: Defaults{Backend: "claude"},
		Backends: map[string]Backend{
			"claude": {
				Command:    "claude --dangerously-skip-permissions",
				PromptFlag: "",
			},
			"codex": {
				Command:    "codex --yolo",
				PromptFlag: "",
			},
		},
		Projects:    make(map[string]Project),
		Keybindings: DefaultKeybindings(),
		UI: UIConfig{
			Theme:       "default",
			ShowElapsed: true,
			ShowIcons:   true,
		},
	}
}

func DefaultKeybindings() Keybindings {
	return Keybindings{
		New:      "n",
		Attach:   "enter",
		Status:   "s",
		Delete:   "d",
		Quit:     "q",
		Help:     "?",
		Filter:   "/",
		Prompt:   "p",
		Worktree: "w",
	}
}
