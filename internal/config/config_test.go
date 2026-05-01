package config

import (
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
