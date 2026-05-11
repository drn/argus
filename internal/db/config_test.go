package db

import (
	"testing"

	"github.com/drn/argus/internal/testutil"
)

func TestDB_Config_AllOverrides(t *testing.T) {
	d := testDB(t)

	overrides := map[string]string{
		"defaults.backend":     "codex",
		"ui.theme":             "dark",
		"ui.spinner":           "braille",
		"ui.show_elapsed":      "false",
		"ui.show_icons":        "false",
		"ui.cleanup_worktrees": "false",
		"sandbox.enabled":      "true",
		"sandbox.deny_read":    "/x,/y",
		"sandbox.extra_write":  "/a,/b",
		"kb.enabled":           "true",
		"kb.http_port":         "9999",
		"kb.metis_vault_path":  "/tmp/metis",
		"api.enabled":          "true",
		"api.http_port":        "8123",
		"argus.source_path":    "/path/to/argus",
	}
	for k, v := range overrides {
		testutil.NoError(t, d.SetConfigValue(k, v))
	}

	cfg := d.Config()

	testutil.Equal(t, cfg.Defaults.Backend, "codex")
	testutil.Equal(t, cfg.UI.Theme, "dark")
	testutil.Equal(t, cfg.UI.SpinnerStyle, "braille")
	testutil.Equal(t, cfg.UI.ShowElapsed, false)
	testutil.Equal(t, cfg.UI.ShowIcons, false)
	if cfg.UI.CleanupWorktrees == nil || *cfg.UI.CleanupWorktrees {
		t.Error("CleanupWorktrees should be set false")
	}
	testutil.Equal(t, cfg.Sandbox.Enabled, true)
	testutil.Equal(t, len(cfg.Sandbox.DenyRead), 2)
	testutil.Equal(t, len(cfg.Sandbox.ExtraWrite), 2)
	testutil.Equal(t, cfg.KB.Enabled, true)
	testutil.Equal(t, cfg.KB.HTTPPort, 9999)
	testutil.Equal(t, cfg.KB.MetisVaultPath, "/tmp/metis")
	testutil.Equal(t, cfg.API.Enabled, true)
	testutil.Equal(t, cfg.API.HTTPPort, 8123)
	testutil.Equal(t, cfg.Argus.SourcePath, "/path/to/argus")
}

// TestDB_Config_BadIntegerPorts covers the strconv.Atoi error path for ports.
func TestDB_Config_BadIntegerPorts(t *testing.T) {
	d := testDB(t)
	testutil.NoError(t, d.SetConfigValue("kb.http_port", "not-an-int"))
	testutil.NoError(t, d.SetConfigValue("api.http_port", "also-not-int"))

	cfg := d.Config()
	// Should fall back to defaults (7742 / 7743) when atoi fails.
	testutil.Equal(t, cfg.KB.HTTPPort, 7742)
	testutil.Equal(t, cfg.API.HTTPPort, 7743)
}

// TestDB_Config_NegativeOrZeroPorts ensures negative/zero ports do not override defaults.
func TestDB_Config_NegativeOrZeroPorts(t *testing.T) {
	d := testDB(t)
	testutil.NoError(t, d.SetConfigValue("kb.http_port", "0"))
	testutil.NoError(t, d.SetConfigValue("api.http_port", "-1"))

	cfg := d.Config()
	testutil.Equal(t, cfg.KB.HTTPPort, 7742)
	testutil.Equal(t, cfg.API.HTTPPort, 7743)
}
