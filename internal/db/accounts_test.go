package db

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/testutil"
)

func TestAccounts_CRUDRoundTrip(t *testing.T) {
	d, err := OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })

	got, err := d.Accounts()
	testutil.NoError(t, err)
	testutil.Equal(t, len(got), 0)

	testutil.NoError(t, d.SetAccount("personal", config.Account{Label: "Me", ClaudeConfigDir: "/x/claude", CodexHome: "/x/codex"}))
	testutil.NoError(t, d.SetAccount("personal", config.Account{ClaudeConfigDir: "/y/claude"}))
	testutil.NoError(t, d.SetAccount("work", config.Account{CodexHome: "/w/codex"}))

	got, err = d.Accounts()
	testutil.NoError(t, err)
	testutil.DeepEqual(t, got, map[string]config.Account{
		"personal": {ClaudeConfigDir: "/y/claude"},
		"work":     {CodexHome: "/w/codex"},
	})

	testutil.NoError(t, d.DeleteAccount("personal"))
	got, _ = d.Accounts()
	testutil.Equal(t, len(got), 1)
}

func TestConfig_LoadsStoredAccountSelection(t *testing.T) {
	d, err := OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })

	testutil.NoError(t, d.SetAccount("personal", config.Account{ClaudeConfigDir: "/x/claude"}))
	testutil.NoError(t, d.SetDefaultAccount("personal"))
	testutil.NoError(t, d.SetProjectAccount("argus", "personal"))
	testutil.NoError(t, d.SetProjectAccount("other", "work"))
	testutil.NoError(t, d.SetProjectAccount("other", ""))

	cfg := d.Config()
	testutil.Equal(t, cfg.Accounts["personal"].ClaudeConfigDir, "/x/claude")
	testutil.Equal(t, cfg.DefaultAccount, "personal")
	testutil.DeepEqual(t, cfg.ProjectAccounts, map[string]string{"argus": "personal"})
}

func fileDB(t *testing.T, toml string) *DB {
	t.Helper()
	dir := t.TempDir()
	d, err := Open(filepath.Join(dir, "data.sql"))
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })
	testutil.NoError(t, os.WriteFile(filepath.Join(dir, config.FileName), []byte(toml), 0o644))
	return d
}

func TestConfig_TomlOverridesStoredAccountsPerName(t *testing.T) {
	d := fileDB(t, `
default_account = "work"

[accounts.work]
claude_config_dir = "/toml/work"

[project_accounts]
argus = "work"
`)
	testutil.NoError(t, d.SetAccount("work", config.Account{ClaudeConfigDir: "/db/work"}))
	testutil.NoError(t, d.SetAccount("personal", config.Account{ClaudeConfigDir: "/db/personal"}))
	testutil.NoError(t, d.SetDefaultAccount("personal"))
	testutil.NoError(t, d.SetProjectAccount("argus", "personal"))
	testutil.NoError(t, d.SetProjectAccount("only-db", "personal"))

	cfg := d.Config()
	testutil.Equal(t, cfg.Accounts["work"].ClaudeConfigDir, "/toml/work")
	testutil.Equal(t, cfg.Accounts["personal"].ClaudeConfigDir, "/db/personal")
	testutil.Equal(t, cfg.DefaultAccount, "work")
	testutil.Equal(t, cfg.ProjectAccounts["argus"], "work")
	testutil.Equal(t, cfg.ProjectAccounts["only-db"], "personal")

	src := d.AccountsFromConfigToml()
	testutil.True(t, src.Accounts["work"])
	testutil.False(t, src.Accounts["personal"])
	testutil.True(t, src.Default)
	testutil.True(t, src.Projects["argus"])
	testutil.False(t, src.Projects["only-db"])
}

func TestAccountsFromConfigToml_EmptyForInMemory(t *testing.T) {
	d, err := OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })
	src := d.AccountsFromConfigToml()
	testutil.Equal(t, len(src.Accounts), 0)
	testutil.False(t, src.Default)
}
