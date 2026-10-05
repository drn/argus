package config

import (
	"path/filepath"
	"testing"

	"github.com/drn/argus/internal/testutil"
)

func testAccountsCfg() Config {
	return Config{
		Accounts: map[string]Account{
			"personal":  {ClaudeConfigDir: "~/.claude-personal", CodexHome: "~/.codex-personal"},
			"abs":       {ClaudeConfigDir: "/opt/claude-abs"},
			"codexonly": {CodexHome: "/opt/codex-only"},
			"rel":       {ClaudeConfigDir: "relative/dir"},
			"badcodex":  {ClaudeConfigDir: "/ok", CodexHome: "rel/codex"},
			"empty":     {},
		},
		DefaultAccount:  "personal",
		ProjectAccounts: map[string]string{"work-proj": "abs", "bad-proj": "nope"},
	}
}

func TestValidateAccount(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg := testAccountsCfg()
	tests := []struct {
		name    string
		wantErr string
	}{
		{"", ""},
		{"default", ""},
		{"personal", ""},
		{"abs", ""},
		{"codexonly", ""},
		{"rel", "claude_config_dir must be absolute"},
		{"badcodex", "codex_home must be absolute"},
		{"empty", "claude_config_dir and/or codex_home"},
		{"nope", "unknown account"},
	}
	for _, tc := range tests {
		t.Run("name="+tc.name, func(t *testing.T) {
			err := cfg.ValidateAccount(tc.name)
			if tc.wantErr == "" {
				testutil.NoError(t, err)
				return
			}
			testutil.Error(t, err)
			testutil.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

func TestClaudeConfigDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfg := testAccountsCfg()

	tests := []struct {
		name     string
		wantDir  string
		explicit bool
		wantErr  bool
	}{
		{"", filepath.Join(home, ".claude"), false, false},
		{"default", filepath.Join(home, ".claude"), false, false},
		{"personal", filepath.Join(home, ".claude-personal"), true, false},
		{"abs", "/opt/claude-abs", true, false},
		{"codexonly", "", false, true},
		{"rel", "", false, true},
		{"empty", "", false, true},
		{"nope", "", false, true},
	}
	for _, tc := range tests {
		t.Run("name="+tc.name, func(t *testing.T) {
			dir, explicit, err := cfg.ClaudeConfigDir(tc.name)
			if tc.wantErr {
				testutil.Error(t, err)
				return
			}
			testutil.NoError(t, err)
			testutil.Equal(t, dir, tc.wantDir)
			testutil.Equal(t, explicit, tc.explicit)
		})
	}
}

func TestCodexHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfg := testAccountsCfg()

	tests := []struct {
		name     string
		wantDir  string
		explicit bool
		wantErr  bool
	}{
		{"", filepath.Join(home, ".codex"), false, false},
		{"default", filepath.Join(home, ".codex"), false, false},
		{"personal", filepath.Join(home, ".codex-personal"), true, false},
		{"codexonly", "/opt/codex-only", true, false},
		{"abs", "", false, true},
		{"badcodex", "", false, true},
		{"nope", "", false, true},
	}
	for _, tc := range tests {
		t.Run("name="+tc.name, func(t *testing.T) {
			dir, explicit, err := cfg.CodexHome(tc.name)
			if tc.wantErr {
				testutil.Error(t, err)
				return
			}
			testutil.NoError(t, err)
			testutil.Equal(t, dir, tc.wantDir)
			testutil.Equal(t, explicit, tc.explicit)
		})
	}
}

func TestAccountSupports(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg := testAccountsCfg()
	tests := []struct {
		account, command string
		want             bool
	}{
		{"default", "claude", true},
		{"default", "codex", true},
		{"default", "pi", true},
		{"", "opencode run", true},
		{"default", "", true},
		{"personal", "claude --dangerously-skip-permissions", true},
		{"personal", "/usr/local/bin/codex", true},
		{"abs", "claude", true},
		{"abs", "codex", false},
		{"codexonly", "codex", true},
		{"codexonly", "claude", false},
		{"personal", "pi", false},
		{"personal", "opencode", false},
		{"personal", "echo hello", false},
		{"personal", "", false},
		{"rel", "claude", false},
		{"nope", "claude", false},
	}
	for _, tc := range tests {
		t.Run(tc.account+"/"+tc.command, func(t *testing.T) {
			testutil.Equal(t, cfg.AccountSupports(tc.account, tc.command), tc.want)
		})
	}
}

func TestResolveAccount(t *testing.T) {
	cfg := testAccountsCfg()
	tests := []struct {
		name, explicit, project, want string
	}{
		{"explicit wins", "abs", "work-proj", "abs"},
		{"explicit unknown returned as-is", "zzz", "", "zzz"},
		{"project default", "", "work-proj", "abs"},
		{"invalid project falls to global", "", "bad-proj", "personal"},
		{"unmapped project falls to global", "", "other", "personal"},
		{"nothing configured", "", "", "default"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := cfg
			if tc.name == "nothing configured" {
				c = Config{}
			}
			testutil.Equal(t, c.ResolveAccount(tc.explicit, tc.project), tc.want)
		})
	}

	t.Run("invalid global ignored", func(t *testing.T) {
		c := Config{DefaultAccount: "ghost"}
		testutil.Equal(t, c.ResolveAccount("", ""), "default")
	})
}

func TestAccountNames(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Run("all", func(t *testing.T) {
		testutil.DeepEqual(t, testAccountsCfg().AccountNames(), []string{"default", "abs", "codexonly", "personal"})
		testutil.DeepEqual(t, Config{}.AccountNames(), []string{"default"})
	})
	t.Run("for backend", func(t *testing.T) {
		cfg := testAccountsCfg()
		testutil.DeepEqual(t, cfg.AccountNamesFor("claude"), []string{"default", "abs", "personal"})
		testutil.DeepEqual(t, cfg.AccountNamesFor("codex"), []string{"default", "codexonly", "personal"})
		testutil.DeepEqual(t, cfg.AccountNamesFor("pi"), []string{"default"})
	})
}

func TestNeverInheritClaude(t *testing.T) {
	for _, n := range []string{".claude.json", "projects", "plugins", ".credentials.json", "Projects", "PLUGINS", ".Claude.json", ".CREDENTIALS.json"} {
		testutil.True(t, NeverInheritClaude(n))
	}
	for _, n := range DefaultClaudeInherit() {
		testutil.False(t, NeverInheritClaude(n))
	}
}

func TestDefaultClaudeInherit(t *testing.T) {
	testutil.DeepEqual(t, DefaultClaudeInherit(), []string{"CLAUDE.md", "skills", "commands", "agents"})
}

func TestAccounts_ParseFromTOML(t *testing.T) {
	path := writeFile(t, `
default_account = "personal"

[project_accounts]
argus = "personal"

[accounts.personal]
claude_config_dir = "~/.claude-personal"
codex_home = "~/.codex-personal"
label = "Personal"

[accounts.bare]
claude_config_dir = "/x"
inherit = []
`)
	l := NewFileLoader(path)
	got := l.Apply(DefaultConfig())
	testutil.NoError(t, l.Err())
	testutil.Equal(t, got.DefaultAccount, "personal")
	testutil.Equal(t, got.ProjectAccounts["argus"], "personal")
	testutil.Equal(t, got.Accounts["personal"].Label, "Personal")
	testutil.Equal(t, got.Accounts["personal"].CodexHome, "~/.codex-personal")
	testutil.Nil(t, got.Accounts["personal"].Inherit)
	testutil.NotNil(t, got.Accounts["bare"].Inherit)
	testutil.Equal(t, len(got.Accounts["bare"].Inherit), 0)
}

func TestFileLoader_AccountMapsNotMutatingBase(t *testing.T) {
	path := writeFile(t, "[accounts.b]\nclaude_config_dir = \"/b\"\n")
	base := Config{Accounts: map[string]Account{"a": {ClaudeConfigDir: "/a"}}}
	got := NewFileLoader(path).Apply(base)
	testutil.Equal(t, len(base.Accounts), 1)
	testutil.Equal(t, len(got.Accounts), 2)
}

func TestValidateAccount_UnsafeDirs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cases := []struct {
		name    string
		acct    Account
		wantErr string
	}{
		{"claude tool default", Account{ClaudeConfigDir: "~/.claude"}, "tool default"},
		{"codex tool default", Account{CodexHome: "~/.codex"}, "tool default"},
		{"home itself", Account{ClaudeConfigDir: "~"}, "dedicated directory"},
		{"filesystem root", Account{CodexHome: "/"}, "dedicated directory"},
		{"ancestor of home", Account{ClaudeConfigDir: filepath.Dir(home)}, "dedicated directory"},
		{"inside ssh", Account{ClaudeConfigDir: "~/.ssh/claude"}, "must not be inside"},
		{"argus data dir", Account{CodexHome: "~/.argus"}, "must not be inside"},
		{"library", Account{ClaudeConfigDir: "~/Library/Application Support/x"}, "must not be inside"},
		{"dedicated dir ok", Account{ClaudeConfigDir: "~/.claude-personal", CodexHome: "~/.codex-personal"}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := Config{Accounts: map[string]Account{"x": tc.acct}}.ValidateAccount("x")
			if tc.wantErr == "" {
				testutil.NoError(t, err)
				return
			}
			testutil.Error(t, err)
			testutil.Contains(t, err.Error(), tc.wantErr)
		})
	}
}
