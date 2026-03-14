package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.Defaults.Backend != "claude" {
		t.Errorf("default backend = %q, want claude", cfg.Defaults.Backend)
	}
	if _, ok := cfg.Backends["claude"]; !ok {
		t.Error("claude backend should exist")
	}
	if cfg.Projects == nil {
		t.Error("projects map should be initialized")
	}
	if !cfg.UI.ShowElapsed {
		t.Error("ShowElapsed should default to true")
	}
	if !cfg.UI.ShowIcons {
		t.Error("ShowIcons should default to true")
	}
}

func TestDefaultKeybindings(t *testing.T) {
	kb := DefaultKeybindings()
	if kb.New != "n" {
		t.Errorf("New = %q, want n", kb.New)
	}
	if kb.Quit != "q" {
		t.Errorf("Quit = %q, want q", kb.Quit)
	}
	if kb.Help != "?" {
		t.Errorf("Help = %q, want ?", kb.Help)
	}
}

func TestConfigDir_Default(t *testing.T) {
	// Unset XDG to test default behavior
	old := os.Getenv("XDG_CONFIG_HOME")
	os.Unsetenv("XDG_CONFIG_HOME")
	defer os.Setenv("XDG_CONFIG_HOME", old)

	dir := ConfigDir()
	home, _ := os.UserHomeDir()
	expected := filepath.Join(home, ".config", "argus")
	if dir != expected {
		t.Errorf("ConfigDir() = %q, want %q", dir, expected)
	}
}

func TestConfigDir_XDG(t *testing.T) {
	old := os.Getenv("XDG_CONFIG_HOME")
	os.Setenv("XDG_CONFIG_HOME", "/tmp/xdg")
	defer os.Setenv("XDG_CONFIG_HOME", old)

	if dir := ConfigDir(); dir != "/tmp/xdg/argus" {
		t.Errorf("ConfigDir() = %q, want /tmp/xdg/argus", dir)
	}
}

func TestLoad_MissingFile(t *testing.T) {
	// Point XDG to a temp dir with no config.toml
	old := os.Getenv("XDG_CONFIG_HOME")
	os.Setenv("XDG_CONFIG_HOME", t.TempDir())
	defer os.Setenv("XDG_CONFIG_HOME", old)

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	// Should fall back to defaults
	if cfg.Defaults.Backend != "claude" {
		t.Errorf("expected default backend, got %q", cfg.Defaults.Backend)
	}
}

func TestLoad_ValidTOML(t *testing.T) {
	dir := t.TempDir()
	argusDir := filepath.Join(dir, "argus")
	os.MkdirAll(argusDir, 0o755)

	tomlContent := `
[defaults]
backend = "codex"

[backends.codex]
command = "codex"
prompt_flag = "--prompt"

[projects.myapp]
path = "/home/user/myapp"
backend = "codex"

[keybindings]
new = "a"

[ui]
theme = "dark"
show_elapsed = false
show_icons = false
`
	os.WriteFile(filepath.Join(argusDir, "config.toml"), []byte(tomlContent), 0o644)

	old := os.Getenv("XDG_CONFIG_HOME")
	os.Setenv("XDG_CONFIG_HOME", dir)
	defer os.Setenv("XDG_CONFIG_HOME", old)

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Defaults.Backend != "codex" {
		t.Errorf("backend = %q, want codex", cfg.Defaults.Backend)
	}
	if cfg.Backends["codex"].Command != "codex" {
		t.Error("codex backend not loaded")
	}
	if _, ok := cfg.Projects["myapp"]; !ok {
		t.Error("myapp project not loaded")
	}
	if cfg.Keybindings.New != "a" {
		t.Errorf("keybinding new = %q, want a", cfg.Keybindings.New)
	}
	if cfg.UI.ShowElapsed {
		t.Error("ShowElapsed should be false")
	}
}

func TestLoad_InvalidTOML(t *testing.T) {
	dir := t.TempDir()
	argusDir := filepath.Join(dir, "argus")
	os.MkdirAll(argusDir, 0o755)
	os.WriteFile(filepath.Join(argusDir, "config.toml"), []byte("not valid toml [[["), 0o644)

	old := os.Getenv("XDG_CONFIG_HOME")
	os.Setenv("XDG_CONFIG_HOME", dir)
	defer os.Setenv("XDG_CONFIG_HOME", old)

	_, err := Load()
	if err == nil {
		t.Error("expected error for invalid TOML")
	}
}

func TestSave(t *testing.T) {
	dir := t.TempDir()
	old := os.Getenv("XDG_CONFIG_HOME")
	os.Setenv("XDG_CONFIG_HOME", dir)
	defer os.Setenv("XDG_CONFIG_HOME", old)

	cfg := DefaultConfig()
	cfg.Defaults.Backend = "test-backend"
	cfg.Projects["myproject"] = Project{Path: "/tmp/myproject", Backend: "claude"}

	if err := Save(cfg); err != nil {
		t.Fatalf("Save() error: %v", err)
	}

	// Verify file was written
	path := filepath.Join(dir, "argus", "config.toml")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("config file not created: %v", err)
	}

	// Load it back and verify round-trip
	loaded, err := Load()
	if err != nil {
		t.Fatalf("Load() after Save() error: %v", err)
	}
	if loaded.Defaults.Backend != "test-backend" {
		t.Errorf("round-trip backend = %q, want test-backend", loaded.Defaults.Backend)
	}
	if p, ok := loaded.Projects["myproject"]; !ok {
		t.Error("myproject not found after round-trip")
	} else if p.Path != "/tmp/myproject" {
		t.Errorf("project path = %q, want /tmp/myproject", p.Path)
	}
}

func TestShouldCleanupWorktrees(t *testing.T) {
	// nil (default) should return true
	ui := UIConfig{}
	if !ui.ShouldCleanupWorktrees() {
		t.Error("nil CleanupWorktrees should default to true")
	}

	// explicit true
	tr := true
	ui.CleanupWorktrees = &tr
	if !ui.ShouldCleanupWorktrees() {
		t.Error("explicit true should return true")
	}

	// explicit false
	fa := false
	ui.CleanupWorktrees = &fa
	if ui.ShouldCleanupWorktrees() {
		t.Error("explicit false should return false")
	}
}

func TestLoad_NilMapsInitialized(t *testing.T) {
	// A minimal TOML that doesn't define backends or projects maps
	dir := t.TempDir()
	argusDir := filepath.Join(dir, "argus")
	os.MkdirAll(argusDir, 0o755)

	tomlContent := `
[defaults]
backend = "custom"
`
	os.WriteFile(filepath.Join(argusDir, "config.toml"), []byte(tomlContent), 0o644)

	old := os.Getenv("XDG_CONFIG_HOME")
	os.Setenv("XDG_CONFIG_HOME", dir)
	defer os.Setenv("XDG_CONFIG_HOME", old)

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}

	// Maps should be initialized even though TOML didn't define them
	if cfg.Backends == nil {
		t.Error("Backends map should be initialized after Load")
	}
	if cfg.Projects == nil {
		t.Error("Projects map should be initialized after Load")
	}
	if cfg.Defaults.Backend != "custom" {
		t.Errorf("backend = %q, want custom", cfg.Defaults.Backend)
	}
}
