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
	if !cfg.UI.DefaultAgentZoom {
		t.Error("DefaultAgentZoom should default to true")
	}
	if !cfg.Hera.Enabled {
		t.Error("Hera.Enabled should default to true (absent key ⇒ enabled)")
	}
	if !cfg.Supervisor.Enabled {
		t.Error("Supervisor.Enabled should default to true (absent key ⇒ supervisor mode, as of P4)")
	}
}

func TestHeraConfig_DefaultEnabled(t *testing.T) {
	cfg := DefaultConfig()
	if !cfg.Hera.Enabled {
		t.Error("Hera.Enabled must default to true; absent DB key ⇒ native Hera on")
	}
}

func TestHeraConfig_TOMLOverride(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Hera.Enabled = false
	if cfg.Hera.Enabled {
		t.Error("explicit false must override the default")
	}
}

// TestSupervisorConfig_DefaultEnabled pins the P4 flip: an absent key ⇒
// supervisor mode ON, mirroring hera.enabled. The in-process runner is reached
// only via an explicit "false" (the retained rollback).
func TestSupervisorConfig_DefaultEnabled(t *testing.T) {
	cfg := DefaultConfig()
	if !cfg.Supervisor.Enabled {
		t.Error("Supervisor.Enabled must default to true; absent key ⇒ out-of-process supervisor (ON, as of P4)")
	}
}

// TestSupervisorConfig_ExplicitFalseRollback confirms the rollback knob: setting
// Enabled=false restores the in-process path (retained one release, not deleted).
func TestSupervisorConfig_ExplicitFalseRollback(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Supervisor.Enabled = false
	if cfg.Supervisor.Enabled {
		t.Error("explicit false must override the default-ON (rollback to in-process runner)")
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

func TestPermissionModeFlags(t *testing.T) {
	cases := []struct {
		mode string
		want string
	}{
		{PermissionModeDefault, "--permission-mode default"},
		{PermissionModeAcceptEdits, "--permission-mode acceptEdits"},
		{PermissionModePlan, "--permission-mode plan"},
		{PermissionModeBypassAllow, "--allow-dangerously-skip-permissions --permission-mode plan"},
		{PermissionModeBypassActive, "--dangerously-skip-permissions"},
		{"", ""},
		{"bogus", ""},
	}
	for _, tc := range cases {
		t.Run(tc.mode, func(t *testing.T) {
			if got := PermissionModeFlags(tc.mode); got != tc.want {
				t.Errorf("PermissionModeFlags(%q) = %q, want %q", tc.mode, got, tc.want)
			}
		})
	}
}

func TestPermissionModeLabel(t *testing.T) {
	for _, m := range PermissionModes {
		if PermissionModeLabel(m) == "" {
			t.Errorf("PermissionModeLabel(%q) is empty", m)
		}
	}
	// Unknown values echo back verbatim.
	if got := PermissionModeLabel("custom"); got != "custom" {
		t.Errorf("PermissionModeLabel(custom) = %q, want custom", got)
	}
}

func TestDefaultConfig_PermissionModeAndCleanCommand(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.Defaults.PermissionMode != PermissionModeBypassActive {
		t.Errorf("default permission mode = %q, want %q", cfg.Defaults.PermissionMode, PermissionModeBypassActive)
	}
	// Permission flags must NOT be baked into the command — they're injected.
	if cmd := cfg.Backends["claude"].Command; cmd != "claude" {
		t.Errorf("claude command = %q, want bare \"claude\"", cmd)
	}
}
