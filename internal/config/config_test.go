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
