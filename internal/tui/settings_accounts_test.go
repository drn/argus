package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

func acctKey(sv *SettingsView, k tcell.Key, r rune) bool {
	return sv.HandleKey(tcell.NewEventKey(k, r, 0))
}

func acctType(sv *SettingsView, text string) {
	for _, r := range text {
		acctKey(sv, tcell.KeyRune, r)
	}
}

func accountsSV(t *testing.T) (*SettingsView, *db.DB) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	sv := testSettingsView(t)
	sv.setCategory(catAccounts)
	sv.setFocus(focusPane)
	d, ok := sv.database.(*db.DB)
	testutil.True(t, ok)
	return sv, d
}

func selectRow(t *testing.T, sv *SettingsView, kind settingsRowKind, key string) {
	t.Helper()
	for i, r := range sv.rows {
		if r.kind == kind && r.key == key {
			sv.cursor = i
			return
		}
	}
	t.Fatalf("row kind=%d key=%q not found in %v", kind, key, sv.rows)
}

func addAccount(t *testing.T, sv *SettingsView, name string) {
	t.Helper()
	acctKey(sv, tcell.KeyRune, 'n')
	acctType(sv, name)
	acctKey(sv, tcell.KeyEnter, 0)
}

func TestAccounts_CategoryReachableAndEmptyState(t *testing.T) {
	sv, _ := accountsSV(t)
	found := false
	for _, c := range builtinCategories {
		found = found || c == catAccounts
	}
	testutil.True(t, found)
	testutil.Equal(t, sv.category.Label(), "Accounts")
	testutil.Equal(t, sv.rows[0].kind, srAccountDefault)
	testutil.Contains(t, sv.rows[0].label, "Default account: default")
}

func TestAccounts_AddSeedsDirPersistsAndSelects(t *testing.T) {
	sv, d := accountsSV(t)
	addAccount(t, sv, "personal")

	testutil.Equal(t, sv.accountErr, "")
	testutil.Equal(t, sv.accounts["personal"].ClaudeConfigDir, "~/.claude-personal")
	stored, err := d.Accounts()
	testutil.NoError(t, err)
	testutil.Equal(t, stored["personal"].ClaudeConfigDir, "~/.claude-personal")
	testutil.Equal(t, d.Config().Accounts["personal"].ClaudeConfigDir, "~/.claude-personal")
	testutil.Equal(t, sv.SelectedRow().kind, srAccount)
	testutil.Equal(t, sv.SelectedRow().key, "personal")
	testutil.False(t, sv.IsEditing())
}

func TestAccounts_AddRejectsBadNames(t *testing.T) {
	cases := []struct{ name, input, wantErr string }{
		{"reserved", "default", "reserved"},
		{"reserved other case", "DEFAULT", "reserved"},
		{"bad chars", "my acct", "letters, digits"},
		{"slash", "a/b", "letters, digits"},
		{"duplicate", "work", "already exists"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sv, d := accountsSV(t)
			addAccount(t, sv, "work")
			testutil.Equal(t, sv.accountErr, "")
			addAccount(t, sv, tc.input)
			testutil.Contains(t, sv.accountErr, tc.wantErr)
			stored, _ := d.Accounts()
			testutil.Equal(t, len(stored), 1)
			testutil.False(t, sv.IsEditing())
		})
	}

	t.Run("blank cancels silently", func(t *testing.T) {
		sv, d := accountsSV(t)
		addAccount(t, sv, "")
		testutil.Equal(t, sv.accountErr, "")
		stored, _ := d.Accounts()
		testutil.Equal(t, len(stored), 0)
	})
	t.Run("escape cancels", func(t *testing.T) {
		sv, d := accountsSV(t)
		acctKey(sv, tcell.KeyRune, 'n')
		acctType(sv, "x")
		acctKey(sv, tcell.KeyEscape, 0)
		testutil.False(t, sv.IsEditing())
		stored, _ := d.Accounts()
		testutil.Equal(t, len(stored), 0)
	})
}

func TestAccounts_EditFields(t *testing.T) {
	sv, d := accountsSV(t)
	addAccount(t, sv, "personal")

	edit := func(field, text string) {
		selectRow(t, sv, srAccountField, accountKey("personal", field))
		acctKey(sv, tcell.KeyRune, 'e')
		testutil.True(t, sv.IsEditing())
		for sv.acctEdit.buf != "" {
			acctKey(sv, tcell.KeyBackspace2, 0)
		}
		acctType(sv, text)
		acctKey(sv, tcell.KeyEnter, 0)
	}

	edit(acctFieldCodex, "~/.codex-personal")
	edit(acctFieldLabel, "Personal plan")
	testutil.Equal(t, sv.accountErr, "")
	stored, _ := d.Accounts()
	testutil.Equal(t, stored["personal"].CodexHome, "~/.codex-personal")
	testutil.Equal(t, stored["personal"].Label, "Personal plan")
	testutil.Equal(t, stored["personal"].ClaudeConfigDir, "~/.claude-personal")

	t.Run("invalid dir rejected and value unchanged", func(t *testing.T) {
		edit(acctFieldClaude, "~")
		testutil.Contains(t, sv.accountErr, "Rejected Claude config dir")
		stored, _ := d.Accounts()
		testutil.Equal(t, stored["personal"].ClaudeConfigDir, "~/.claude-personal")
	})
	t.Run("relative dir rejected", func(t *testing.T) {
		edit(acctFieldClaude, "relative/dir")
		testutil.Contains(t, sv.accountErr, "absolute or ~-prefixed")
	})
	t.Run("sensitive dir rejected", func(t *testing.T) {
		edit(acctFieldCodex, "~/.ssh/codex")
		testutil.Contains(t, sv.accountErr, "must not be inside")
	})
	t.Run("clearing one dir is fine while the other is set", func(t *testing.T) {
		edit(acctFieldClaude, "")
		testutil.Equal(t, sv.accountErr, "")
		stored, _ := d.Accounts()
		testutil.Equal(t, stored["personal"].ClaudeConfigDir, "")
	})
	t.Run("clearing the last dir is rejected", func(t *testing.T) {
		edit(acctFieldCodex, "")
		testutil.Contains(t, sv.accountErr, "define claude_config_dir and/or codex_home")
		stored, _ := d.Accounts()
		testutil.Equal(t, stored["personal"].CodexHome, "~/.codex-personal")
	})
	t.Run("escape cancels without writing", func(t *testing.T) {
		selectRow(t, sv, srAccountField, accountKey("personal", acctFieldLabel))
		acctKey(sv, tcell.KeyEnter, 0)
		acctType(sv, "zzz")
		acctKey(sv, tcell.KeyEscape, 0)
		stored, _ := d.Accounts()
		testutil.Equal(t, stored["personal"].Label, "Personal plan")
	})
}

func TestAccounts_PasteIntoEdit(t *testing.T) {
	sv, _ := accountsSV(t)
	acctKey(sv, tcell.KeyRune, 'n')
	sv.PasteHandler()("per sonal\n", nil)
	testutil.Equal(t, sv.acctEdit.buf, "per sonal")
}

func TestAccounts_DeleteNeedsConfirmAndShowsTaskCount(t *testing.T) {
	sv, d := accountsSV(t)
	addAccount(t, sv, "personal")
	testutil.NoError(t, d.Add(&model.Task{ID: "t1", Name: "a", Project: "p", Account: "personal"}))
	testutil.NoError(t, d.Add(&model.Task{ID: "t2", Name: "b", Project: "p", Account: "personal"}))
	testutil.NoError(t, d.Add(&model.Task{ID: "t3", Name: "c", Project: "p"}))
	selectRow(t, sv, srAccount, "personal")

	acctKey(sv, tcell.KeyRune, 'd')
	stored, _ := d.Accounts()
	testutil.Equal(t, len(stored), 1)
	testutil.Equal(t, sv.pendingAccountDelete, "personal")
	testutil.Equal(t, sv.accountTaskCount("personal"), 2)
	testutil.Contains(t, readSettingsScreen(t, sv, 120, 40), "2 task(s) use it")

	acctKey(sv, tcell.KeyRune, 'd')
	stored, _ = d.Accounts()
	testutil.Equal(t, len(stored), 0)
	_, still := sv.accounts["personal"]
	testutil.False(t, still)
}

func TestAccounts_MovingCursorCancelsPendingDelete(t *testing.T) {
	sv, d := accountsSV(t)
	addAccount(t, sv, "personal")
	selectRow(t, sv, srAccount, "personal")
	acctKey(sv, tcell.KeyRune, 'd')
	testutil.Equal(t, sv.pendingAccountDelete, "personal")
	acctKey(sv, tcell.KeyDown, 0)
	testutil.Equal(t, sv.pendingAccountDelete, "")
	acctKey(sv, tcell.KeyRune, 'd') // acts on the new row, not a stale confirm
	stored, _ := d.Accounts()
	testutil.Equal(t, len(stored), 1)
}

func TestAccounts_CycleDefaultAccountPersists(t *testing.T) {
	sv, d := accountsSV(t)
	addAccount(t, sv, "personal")
	selectRow(t, sv, srAccountDefault, "_default")

	acctKey(sv, tcell.KeyRight, 0)
	testutil.Equal(t, d.Config().DefaultAccount, "personal")
	testutil.Contains(t, sv.rows[0].label, "Default account: personal")

	acctKey(sv, tcell.KeyEnter, 0) // wraps back to default, stored as unset
	testutil.Equal(t, d.Config().DefaultAccount, "")
	testutil.Contains(t, sv.rows[0].label, "Default account: default")

	acctKey(sv, tcell.KeyRight, 0)
	acctKey(sv, tcell.KeyRune, 'd') // reset
	testutil.Equal(t, d.Config().DefaultAccount, "")
}

func TestAccounts_CycleProjectAccountPersists(t *testing.T) {
	sv, d := accountsSV(t)
	testutil.NoError(t, d.SetProject("argus", config.Project{Path: t.TempDir()}))
	sv.Refresh()
	addAccount(t, sv, "personal")
	selectRow(t, sv, srProjectAccount, "argus")
	testutil.Contains(t, sv.SelectedRow().label, "(global default)")

	want := []string{"default", "personal", ""}
	for _, w := range want {
		acctKey(sv, tcell.KeyRight, 0)
		testutil.Equal(t, d.Config().ProjectAccounts["argus"], w)
	}
	acctKey(sv, tcell.KeyRight, 0)
	acctKey(sv, tcell.KeyRune, 'd')
	_, mapped := d.Config().ProjectAccounts["argus"]
	testutil.False(t, mapped)
}

func tomlAccountsSV(t *testing.T, toml string) (*SettingsView, *db.DB) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	sv := testSettingsViewWithConfigToml(t, toml)
	sv.setCategory(catAccounts)
	sv.setFocus(focusPane)
	d, ok := sv.database.(*db.DB)
	testutil.True(t, ok)
	return sv, d
}

func TestAccounts_ConfigTomlEntriesAreReadOnly(t *testing.T) {
	sv, d := tomlAccountsSV(t, `
default_account = "work"

[accounts.work]
claude_config_dir = "/toml/work"

[project_accounts]
argus = "work"
`)
	testutil.NoError(t, d.SetProject("argus", config.Project{Path: t.TempDir()}))
	testutil.NoError(t, d.SetAccount("personal", config.Account{ClaudeConfigDir: "/db/personal"}))
	sv.Refresh()

	testutil.Contains(t, sv.rows[0].label, "(config.toml)")
	selectRow(t, sv, srAccount, "work")
	testutil.Contains(t, sv.SelectedRow().label, "(config.toml)")

	t.Run("edit refused", func(t *testing.T) {
		selectRow(t, sv, srAccountField, accountKey("work", acctFieldClaude))
		acctKey(sv, tcell.KeyRune, 'e')
		testutil.False(t, sv.IsEditing())
		testutil.Contains(t, sv.accountErr, "config.toml")
	})
	t.Run("delete refused", func(t *testing.T) {
		selectRow(t, sv, srAccount, "work")
		acctKey(sv, tcell.KeyRune, 'd')
		acctKey(sv, tcell.KeyRune, 'd')
		testutil.Contains(t, sv.accountErr, "config.toml")
		testutil.Equal(t, sv.accounts["work"].ClaudeConfigDir, "/toml/work")
	})
	t.Run("default cycle refused", func(t *testing.T) {
		selectRow(t, sv, srAccountDefault, "_default")
		acctKey(sv, tcell.KeyRight, 0)
		testutil.Contains(t, sv.accountErr, "default_account")
		testutil.Equal(t, d.Config().DefaultAccount, "work")
	})
	t.Run("project cycle refused", func(t *testing.T) {
		selectRow(t, sv, srProjectAccount, "argus")
		acctKey(sv, tcell.KeyRight, 0)
		testutil.Contains(t, sv.accountErr, "config.toml")
		testutil.Equal(t, d.Config().ProjectAccounts["argus"], "work")
	})
	t.Run("db-only account stays editable", func(t *testing.T) {
		selectRow(t, sv, srAccountField, accountKey("personal", acctFieldLabel))
		acctKey(sv, tcell.KeyRune, 'e')
		testutil.True(t, sv.IsEditing())
		acctType(sv, "Me")
		acctKey(sv, tcell.KeyEnter, 0)
		stored, _ := d.Accounts()
		testutil.Equal(t, stored["personal"].Label, "Me")
	})
	t.Run("new account name colliding with a toml account is rejected", func(t *testing.T) {
		addAccount(t, sv, "work")
		testutil.Contains(t, sv.accountErr, "already exists")
	})
}

func TestAccounts_RemoteModeIsReadOnly(t *testing.T) {
	sv, d := accountsSV(t)
	testutil.NoError(t, d.SetAccount("personal", config.Account{ClaudeConfigDir: "/db/personal"}))
	sv.Refresh()
	sv.SetRemote(true)
	sv.rebuildRows()

	acctKey(sv, tcell.KeyRune, 'n')
	testutil.False(t, sv.IsEditing())
	testutil.Contains(t, sv.accountErr, "--remote")

	selectRow(t, sv, srAccountField, accountKey("personal", acctFieldLabel))
	acctKey(sv, tcell.KeyRune, 'e')
	testutil.False(t, sv.IsEditing())
	testutil.Contains(t, sv.accountErr, "--remote")
}

func TestAccounts_EditStateClearedWhenLeavingCategory(t *testing.T) {
	sv, _ := accountsSV(t)
	acctKey(sv, tcell.KeyRune, 'n')
	acctType(sv, "abc")
	testutil.True(t, sv.IsEditing())
	sv.acctEdit.active = false // typed edit ends before a category switch via the rail
	sv.accountErr = "stale"
	sv.setCategory(catSystem)
	testutil.Equal(t, sv.accountErr, "")
	testutil.False(t, sv.IsEditing())
}

func TestAccounts_DetailRendersForEveryRowKind(t *testing.T) {
	sv, d := accountsSV(t)
	testutil.NoError(t, d.SetProject("argus", config.Project{Path: t.TempDir()}))
	sv.Refresh()
	addAccount(t, sv, "personal")
	selectRow(t, sv, srAccountField, accountKey("personal", acctFieldCodex))
	acctKey(sv, tcell.KeyRune, 'e')
	acctType(sv, "~/.codex-personal")
	acctKey(sv, tcell.KeyEnter, 0)

	for _, tc := range []struct {
		kind settingsRowKind
		key  string
		want string
	}{
		{srAccountDefault, "_default", "Default account"},
		{srAccount, "personal", "First login (Claude)"},
		{srAccountField, accountKey("personal", acctFieldCodex), "CODEX_HOME=~/.codex-personal codex login"},
		{srProjectAccount, "argus", "Project default: argus"},
	} {
		selectRow(t, sv, tc.kind, tc.key)
		testutil.Contains(t, readSettingsScreen(t, sv, 120, 40), tc.want)
	}

	t.Run("new-name prompt row", func(t *testing.T) {
		acctKey(sv, tcell.KeyRune, 'n')
		out := readSettingsScreen(t, sv, 120, 40)
		testutil.Contains(t, out, "New account name:")
		testutil.True(t, strings.Contains(out, "[enter] save"))
		acctKey(sv, tcell.KeyEscape, 0)
	})
}

func TestNextAccountOption(t *testing.T) {
	opts := []string{"default", "a", "b"}
	testutil.Equal(t, nextAccountOption(opts, "default", false), "a")
	testutil.Equal(t, nextAccountOption(opts, "b", false), "default")
	testutil.Equal(t, nextAccountOption(opts, "gone", false), "default")
	testutil.Equal(t, nextAccountOption(opts, "", true), "default")
	testutil.Equal(t, nextAccountOption(opts, "b", true), "")
	testutil.Equal(t, nextAccountOption(opts, "gone", true), "")
}

func TestAccounts_DeleteResetsDanglingReferences(t *testing.T) {
	sv, d := accountsSV(t)
	testutil.NoError(t, d.SetProject("argus", config.Project{Path: t.TempDir()}))
	testutil.NoError(t, d.SetProject("other", config.Project{Path: t.TempDir()}))
	sv.Refresh()
	addAccount(t, sv, "work")
	addAccount(t, sv, "personal")
	testutil.NoError(t, d.SetDefaultAccount("work"))
	testutil.NoError(t, d.SetProjectAccount("argus", "work"))
	testutil.NoError(t, d.SetProjectAccount("other", "personal"))
	sv.Refresh()
	selectRow(t, sv, srAccount, "work")

	acctKey(sv, tcell.KeyRune, 'd')
	prompt := readSettingsScreen(t, sv, 140, 40)
	testutil.Contains(t, prompt, "it is the default account")
	testutil.Contains(t, prompt, "1 project default(s) will reset")

	acctKey(sv, tcell.KeyRune, 'd')
	cfg := d.Config()
	testutil.Equal(t, cfg.DefaultAccount, "")
	_, argusMapped := cfg.ProjectAccounts["argus"]
	testutil.False(t, argusMapped)
	testutil.Equal(t, cfg.ProjectAccounts["other"], "personal") // unrelated mapping untouched
	testutil.Contains(t, sv.rows[0].label, "Default account: default")
}

func TestAccounts_TomlReferencesToDeletedAccountShowMissing(t *testing.T) {
	sv, d := tomlAccountsSV(t, `
default_account = "ghost"

[project_accounts]
argus = "ghost"
`)
	testutil.NoError(t, d.SetProject("argus", config.Project{Path: t.TempDir()}))
	sv.Refresh()
	testutil.Contains(t, sv.rows[0].label, "(missing)")
	selectRow(t, sv, srProjectAccount, "argus")
	testutil.Contains(t, sv.SelectedRow().label, "(missing)")
}

func TestAccounts_RejectsSharedDirectories(t *testing.T) {
	sv, d := accountsSV(t)
	addAccount(t, sv, "work")
	addAccount(t, sv, "personal")

	selectRow(t, sv, srAccountField, accountKey("personal", acctFieldClaude))
	acctKey(sv, tcell.KeyRune, 'e')
	for sv.acctEdit.buf != "" {
		acctKey(sv, tcell.KeyBackspace2, 0)
	}
	acctType(sv, "~/.claude-work/")
	acctKey(sv, tcell.KeyEnter, 0)
	testutil.Contains(t, sv.accountErr, `already used by account "work"`)
	stored, _ := d.Accounts()
	testutil.Equal(t, stored["personal"].ClaudeConfigDir, "~/.claude-personal")
}

func TestAccounts_PipeInTomlNameStaysIntact(t *testing.T) {
	sv, _ := tomlAccountsSV(t, "[accounts.\"a|b\"]\nclaude_config_dir = \"/toml/ab\"\n")
	selectRow(t, sv, srAccount, "a|b")
	testutil.Equal(t, rowAccountName(sv.SelectedRow()), "a|b")
	selectRow(t, sv, srAccountField, accountKey("a|b", acctFieldClaude))
	testutil.Equal(t, rowAccountName(sv.SelectedRow()), "a|b")

	acctKey(sv, tcell.KeyRune, 'e') // toml-defined, so refused; must not mint a DB account "a"
	testutil.False(t, sv.IsEditing())
	_, minted := sv.accounts["a"]
	testutil.False(t, minted)
	testutil.Equal(t, rowAccountName(&settingsRow{kind: srProjectAccount, key: "x"}), "")
}

func TestAccounts_StalePendingDeleteIsCancelledOnRefresh(t *testing.T) {
	sv, d := accountsSV(t)
	addAccount(t, sv, "personal")
	selectRow(t, sv, srAccount, "personal")
	acctKey(sv, tcell.KeyRune, 'd')
	testutil.Equal(t, sv.pendingAccountDelete, "personal")
	testutil.NoError(t, d.DeleteAccount("personal"))
	sv.Refresh()
	testutil.Equal(t, sv.pendingAccountDelete, "")
}

func TestAccounts_EditCancelledWhenConfigTomlStartsDefiningIt(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	d, err := db.Open(filepath.Join(dir, "data.sql"))
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })
	sv := NewSettingsView(d)
	sv.Refresh()
	sv.setCategory(catAccounts)
	sv.setFocus(focusPane)

	addAccount(t, sv, "work")
	selectRow(t, sv, srAccountField, accountKey("work", acctFieldLabel))
	acctKey(sv, tcell.KeyRune, 'e')
	testutil.True(t, sv.IsEditing())

	testutil.NoError(t, os.WriteFile(filepath.Join(dir, config.FileName),
		[]byte("[accounts.work]\nclaude_config_dir = \"/toml/work\"\n"), 0o644))
	sv.Refresh()
	testutil.False(t, sv.IsEditing())
	testutil.Equal(t, sv.accounts["work"].ClaudeConfigDir, "/toml/work")
}
