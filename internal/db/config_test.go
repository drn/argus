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
		"hera.enabled":             "false",
		"supervisor.enabled":       "true",
		"argus.source_path":        "/path/to/argus",
		"todo.backend":             "things3",
		"todo.things3.project":     "Argus",
		"todo.things3.tag":         "argus",
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
	testutil.Equal(t, cfg.Hera.Enabled, false)
	testutil.Equal(t, cfg.Supervisor.Enabled, true)
	testutil.Equal(t, cfg.Argus.SourcePath, "/path/to/argus")
	testutil.Equal(t, cfg.Todo.Backend, "things3")
	testutil.Equal(t, cfg.Todo.Things3.Project, "Argus")
	testutil.Equal(t, cfg.Todo.Things3.Tag, "argus")
}

// TestDB_Config_SupervisorEnabledDefaultTrue verifies that an absent
// supervisor.enabled key leaves the supervisor ON (the P4 default — agents run
// under the out-of-process session-supervisor so the daemon can bounce freely),
// matching the hera/kb/api absent-key convention (here: default ON like hera).
func TestDB_Config_SupervisorEnabledDefaultTrue(t *testing.T) {
	d := testDB(t)
	cfg := d.Config()
	testutil.Equal(t, cfg.Supervisor.Enabled, true)
}

// TestDB_Config_SupervisorEnabledExplicitTrue verifies that writing "true"
// enables the out-of-process session-supervisor.
func TestDB_Config_SupervisorEnabledExplicitTrue(t *testing.T) {
	d := testDB(t)
	testutil.NoError(t, d.SetConfigValue("supervisor.enabled", "true"))
	cfg := d.Config()
	testutil.Equal(t, cfg.Supervisor.Enabled, true)
}

// TestDB_Config_SupervisorEnabledExplicitFalse verifies the DB rollback: writing
// "false" disables the supervisor (restores the in-process runner path) even
// though the default is now ON.
func TestDB_Config_SupervisorEnabledExplicitFalse(t *testing.T) {
	d := testDB(t)
	testutil.NoError(t, d.SetConfigValue("supervisor.enabled", "false"))
	cfg := d.Config()
	testutil.Equal(t, cfg.Supervisor.Enabled, false)
}

// TestDB_Config_SupervisorTOMLRollback verifies the config.toml rollback path:
// a `supervisor.enabled = false` in config.toml overrides both the default-ON
// and a DB-stored "true", restoring the retained in-process runner. This is the
// power-user rollback knob (toml wins over DB; standard overlay precedence).
func TestDB_Config_SupervisorTOMLRollback(t *testing.T) {
	dir := t.TempDir()
	d, err := Open(filepath.Join(dir, "data.sql"))
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })

	// DB says ON; the toml file must still win and force OFF (the rollback).
	testutil.NoError(t, d.SetConfigValue("supervisor.enabled", "true"))

	toml := `
[supervisor]
enabled = false
`
	testutil.NoError(t, os.WriteFile(filepath.Join(dir, config.FileName), []byte(toml), 0o644))

	testutil.Equal(t, d.Config().Supervisor.Enabled, false)
}

// TestDB_Config_HeraEnabledDefaultTrue verifies that an absent hera.enabled key
// leaves Hera enabled (the default), matching the kb/api absent-key convention.
func TestDB_Config_HeraEnabledDefaultTrue(t *testing.T) {
	d := testDB(t)
	cfg := d.Config()
	testutil.Equal(t, cfg.Hera.Enabled, true)
}

// TestDB_Config_HeraEnabledExplicitFalse verifies that writing "false" disables
// the native Hera view.
func TestDB_Config_HeraEnabledExplicitFalse(t *testing.T) {
	d := testDB(t)
	testutil.NoError(t, d.SetConfigValue("hera.enabled", "false"))
	cfg := d.Config()
	testutil.Equal(t, cfg.Hera.Enabled, false)
}

// TestDB_Config_HeraEnabledExplicitTrue verifies that writing "true" re-enables
// Hera after a prior disable.
func TestDB_Config_HeraEnabledExplicitTrue(t *testing.T) {
	d := testDB(t)
	testutil.NoError(t, d.SetConfigValue("hera.enabled", "false"))
	testutil.NoError(t, d.SetConfigValue("hera.enabled", "true"))
	cfg := d.Config()
	testutil.Equal(t, cfg.Hera.Enabled, true)
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
