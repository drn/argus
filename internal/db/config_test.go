package db

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/testutil"
)

// TestDB_Config_TOMLOverlay verifies that ~/.argus/config.toml (resolved next
// to the DB file) overrides both code defaults and DB-stored settings.
func TestDB_Config_TOMLOverlay(t *testing.T) {
	dir := t.TempDir()
	d, err := Open(filepath.Join(dir, "data.sql"))
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })

	// DB-stored value that the file will override, plus one the file leaves alone.
	testutil.NoError(t, d.SetConfigValue("ui.theme", "from-db"))
	testutil.NoError(t, d.SetConfigValue("ui.spinner", "from-db-spinner"))

	toml := `
[ui]
theme = "from-toml"

[backends.custom]
command = "my-agent"
`
	testutil.NoError(t, os.WriteFile(filepath.Join(dir, config.FileName), []byte(toml), 0o644))

	cfg := d.Config()

	// File wins over the DB value.
	testutil.Equal(t, cfg.UI.Theme, "from-toml")
	// DB value with no file override survives.
	testutil.Equal(t, cfg.UI.SpinnerStyle, "from-db-spinner")
	// File-added backend merges with the seeded defaults.
	testutil.Equal(t, cfg.Backends["custom"].Command, "my-agent")
	if _, ok := cfg.Backends["claude"]; !ok {
		t.Error("seeded claude backend should survive the overlay")
	}
}

// TestDB_Config_NoTOMLFile confirms the overlay is a no-op when the file is
// absent — DB/default values stand.
func TestDB_Config_NoTOMLFile(t *testing.T) {
	dir := t.TempDir()
	d, err := Open(filepath.Join(dir, "data.sql"))
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })

	testutil.NoError(t, d.SetConfigValue("ui.theme", "from-db"))

	testutil.Equal(t, d.Config().UI.Theme, "from-db")
}

// TestDB_Config_InMemoryHasNoLoader confirms in-memory DBs never wire a file
// loader (so tests can't accidentally read the real ~/.argus/config.toml).
func TestDB_Config_InMemoryHasNoLoader(t *testing.T) {
	d := testDB(t)
	if d.cfgLoader != nil {
		t.Error("in-memory DB must not have a config file loader")
	}
	// Config() must still work with a nil loader.
	testutil.Equal(t, d.Config().UI.Theme, "default")
}

func TestDB_Config_AllOverrides(t *testing.T) {
	d := testDB(t)

	overrides := map[string]string{
		"defaults.backend":         "codex",
		"defaults.share_project":   "argus",
		"defaults.permission_mode": "acceptEdits",
		"ui.theme":                 "dark",
		"ui.spinner":               "braille",
		"ui.show_elapsed":          "false",
		"ui.show_icons":            "false",
		"ui.default_agent_zoom":    "false",
		"ui.cleanup_worktrees":     "false",
		"sandbox.enabled":          "true",
		"sandbox.deny_read":        "/x,/y",
		"sandbox.extra_write":      "/a,/b",
		"kb.enabled":               "true",
		"kb.http_port":             "9999",
		"kb.metis_vault_path":      "/tmp/metis",
		"api.enabled":              "true",
		"api.http_port":            "8123",
		"argus.source_path":        "/path/to/argus",
	}
	for k, v := range overrides {
		testutil.NoError(t, d.SetConfigValue(k, v))
	}

	cfg := d.Config()

	testutil.Equal(t, cfg.Defaults.Backend, "codex")
	testutil.Equal(t, cfg.Defaults.ShareProject, "argus")
	testutil.Equal(t, cfg.Defaults.PermissionMode, "acceptEdits")
	testutil.Equal(t, cfg.UI.Theme, "dark")
	testutil.Equal(t, cfg.UI.SpinnerStyle, "braille")
	testutil.Equal(t, cfg.UI.ShowElapsed, false)
	testutil.Equal(t, cfg.UI.ShowIcons, false)
	testutil.Equal(t, cfg.UI.DefaultAgentZoom, false)
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

func TestDB_Config_DefaultAgentZoomDefaultsTrue(t *testing.T) {
	d := testDB(t)
	// No ui.default_agent_zoom override → falls back to DefaultConfig (true).
	testutil.Equal(t, d.Config().UI.DefaultAgentZoom, true)
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
