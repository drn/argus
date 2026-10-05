package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

func seedDefaultClaude(t *testing.T, names ...string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	src := filepath.Join(home, ".claude")
	testutil.NoError(t, os.MkdirAll(src, 0o755))
	for _, n := range names {
		testutil.NoError(t, os.WriteFile(filepath.Join(src, n), []byte("x"), 0o644))
	}
	return src
}

func TestBootstrapClaudeConfigDir(t *testing.T) {
	t.Run("links defaults, skips missing", func(t *testing.T) {
		src := seedDefaultClaude(t, "CLAUDE.md")
		dir := filepath.Join(t.TempDir(), "acct")
		testutil.NoError(t, BootstrapClaudeConfigDir(dir, nil))
		fi, err := os.Stat(dir)
		testutil.NoError(t, err)
		testutil.Equal(t, fi.Mode().Perm(), os.FileMode(0o700))
		got, err := os.Readlink(filepath.Join(dir, "CLAUDE.md"))
		testutil.NoError(t, err)
		testutil.Equal(t, got, filepath.Join(src, "CLAUDE.md"))
		_, err = os.Lstat(filepath.Join(dir, "skills"))
		testutil.True(t, os.IsNotExist(err))
	})

	t.Run("never inherits credentials or global state", func(t *testing.T) {
		seedDefaultClaude(t, ".credentials.json", ".claude.json", "projects", "plugins", "settings.json")
		dir := filepath.Join(t.TempDir(), "acct")
		testutil.NoError(t, BootstrapClaudeConfigDir(dir, []string{".credentials.json", ".claude.json", "projects", "plugins", "settings.json"}))
		for _, n := range []string{".credentials.json", ".claude.json", "projects", "plugins"} {
			_, err := os.Lstat(filepath.Join(dir, n))
			testutil.True(t, os.IsNotExist(err))
		}
		_, err := os.Lstat(filepath.Join(dir, "settings.json"))
		testutil.NoError(t, err)
	})

	t.Run("existing entry untouched and idempotent", func(t *testing.T) {
		seedDefaultClaude(t, "settings.json")
		dir := filepath.Join(t.TempDir(), "acct")
		testutil.NoError(t, os.MkdirAll(dir, 0o700))
		own := filepath.Join(dir, "settings.json")
		testutil.NoError(t, os.WriteFile(own, []byte("mine"), 0o600))
		testutil.NoError(t, BootstrapClaudeConfigDir(dir, nil))
		testutil.NoError(t, BootstrapClaudeConfigDir(dir, nil))
		b, err := os.ReadFile(own)
		testutil.NoError(t, err)
		testutil.Equal(t, string(b), "mine")
		fi, err := os.Lstat(own)
		testutil.NoError(t, err)
		testutil.True(t, fi.Mode()&os.ModeSymlink == 0)
	})

	t.Run("explicit empty inherits nothing; path-escaping entry skipped", func(t *testing.T) {
		seedDefaultClaude(t, "settings.json")
		dir := filepath.Join(t.TempDir(), "acct")
		testutil.NoError(t, BootstrapClaudeConfigDir(dir, []string{}))
		testutil.NoError(t, BootstrapClaudeConfigDir(dir, []string{"../settings.json"}))
		ents, err := os.ReadDir(dir)
		testutil.NoError(t, err)
		testutil.Equal(t, len(ents), 0)
	})
}

func acctCfg(dir string) config.Config {
	cfg := permModeConfig("")
	cfg.Accounts = map[string]config.Account{"work": {ClaudeConfigDir: dir}}
	return cfg
}

func envHas(env []string, kv string) bool {
	for _, e := range env {
		if e == kv {
			return true
		}
	}
	return false
}

func TestBuildCmd_AccountClaudeConfigDir(t *testing.T) {
	seedDefaultClaude(t, "CLAUDE.md")
	acctDir := filepath.Join(t.TempDir(), "work")

	t.Run("exports CLAUDE_CONFIG_DIR and bootstraps", func(t *testing.T) {
		task := &model.Task{Name: "t", Prompt: "go", Worktree: t.TempDir(), Account: "work"}
		cmd, cleanup, err := BuildCmd(task, acctCfg(acctDir), false)
		testutil.NoError(t, err)
		if cleanup != nil {
			cleanup()
		}
		testutil.True(t, envHas(cmd.Env, "CLAUDE_CONFIG_DIR="+acctDir))
		_, err = os.Lstat(filepath.Join(acctDir, "CLAUDE.md"))
		testutil.NoError(t, err)
	})

	t.Run("default and empty export nothing", func(t *testing.T) {
		t.Setenv("CLAUDE_CONFIG_DIR", "")
		for _, a := range []string{"", "default"} {
			task := &model.Task{Name: "t", Prompt: "go", Worktree: t.TempDir(), Account: a}
			cmd, _, err := BuildCmd(task, acctCfg(acctDir), false)
			testutil.NoError(t, err)
			for _, e := range cmd.Env {
				testutil.False(t, strings.HasPrefix(e, "CLAUDE_CONFIG_DIR=/"))
			}
		}
	})

	t.Run("unknown stored account fails loud", func(t *testing.T) {
		task := &model.Task{Name: "t", Prompt: "go", Worktree: t.TempDir(), Account: "gone"}
		_, _, err := BuildCmd(task, acctCfg(acctDir), false)
		testutil.Error(t, err)
		testutil.Contains(t, err.Error(), "gone")
	})

	t.Run("non-claude backend never exports a claude config dir", func(t *testing.T) {
		t.Setenv("CLAUDE_CONFIG_DIR", "/inherited")
		task := &model.Task{Name: "t", Backend: "codex", Prompt: "go", Worktree: t.TempDir()}
		cmd, _, err := BuildCmd(task, acctCfg(acctDir), false)
		testutil.NoError(t, err)
		for _, e := range cmd.Env {
			testutil.False(t, strings.HasPrefix(e, "CLAUDE_CONFIG_DIR=/"))
		}
	})
}

func TestBootstrapClaudeConfigDir_SeedsSettings(t *testing.T) {
	t.Run("copies once, not a symlink", func(t *testing.T) {
		src := seedDefaultClaude(t)
		testutil.NoError(t, os.WriteFile(filepath.Join(src, "settings.json"), []byte(`{"theme":"dark"}`), 0o644))
		dir := filepath.Join(t.TempDir(), "acct")
		testutil.NoError(t, BootstrapClaudeConfigDir(dir, nil))
		to := filepath.Join(dir, "settings.json")
		fi, err := os.Lstat(to)
		testutil.NoError(t, err)
		testutil.True(t, fi.Mode().IsRegular())
		testutil.Equal(t, fi.Mode().Perm(), os.FileMode(0o600))
		b, err := os.ReadFile(to)
		testutil.NoError(t, err)
		testutil.Equal(t, string(b), `{"theme":"dark"}`)

		testutil.NoError(t, os.WriteFile(to, []byte(`{"theme":"light"}`), 0o600))
		testutil.NoError(t, BootstrapClaudeConfigDir(dir, nil))
		b, err = os.ReadFile(to)
		testutil.NoError(t, err)
		testutil.Equal(t, string(b), `{"theme":"light"}`)
	})

	t.Run("never writes through an existing symlink", func(t *testing.T) {
		src := seedDefaultClaude(t)
		srcSettings := filepath.Join(src, "settings.json")
		testutil.NoError(t, os.WriteFile(srcSettings, []byte(`{"a":1}`), 0o644))
		dir := filepath.Join(t.TempDir(), "acct")
		testutil.NoError(t, os.MkdirAll(dir, 0o700))
		target := filepath.Join(t.TempDir(), "elsewhere.json")
		testutil.NoError(t, os.Symlink(target, filepath.Join(dir, "settings.json")))
		testutil.NoError(t, BootstrapClaudeConfigDir(dir, nil))
		_, err := os.Stat(target)
		testutil.True(t, os.IsNotExist(err))
	})

	t.Run("drops auth overrides and the whole env block", func(t *testing.T) {
		src := seedDefaultClaude(t)
		raw := `{"apiKeyHelper":"helper.sh","theme":"dark","env":{"ANTHROPIC_API_KEY":"x","CLAUDE_CODE_OAUTH_TOKEN":"y","FOO":"bar"}}`
		testutil.NoError(t, os.WriteFile(filepath.Join(src, "settings.json"), []byte(raw), 0o644))
		dir := filepath.Join(t.TempDir(), "acct")
		testutil.NoError(t, BootstrapClaudeConfigDir(dir, nil))
		b, err := os.ReadFile(filepath.Join(dir, "settings.json"))
		testutil.NoError(t, err)
		var got map[string]any
		testutil.NoError(t, json.Unmarshal(b, &got))
		testutil.DeepEqual(t, got, map[string]any{"theme": "dark"})
	})

	t.Run("missing or unparsable source seeds nothing", func(t *testing.T) {
		src := seedDefaultClaude(t)
		dir := filepath.Join(t.TempDir(), "acct")
		testutil.NoError(t, BootstrapClaudeConfigDir(dir, nil))
		_, err := os.Lstat(filepath.Join(dir, "settings.json"))
		testutil.True(t, os.IsNotExist(err))

		testutil.NoError(t, os.WriteFile(filepath.Join(src, "settings.json"), []byte("{nope"), 0o644))
		testutil.NoError(t, BootstrapClaudeConfigDir(dir, nil))
		_, err = os.Lstat(filepath.Join(dir, "settings.json"))
		testutil.True(t, os.IsNotExist(err))
	})
}

func TestSpawnBaseEnv(t *testing.T) {
	base := []string{"PATH=/bin", "CLAUDE_CONFIG_DIR=/inh", "ANTHROPIC_API_KEY=k", "ANTHROPIC_AUTH_TOKEN=t", "CLAUDE_CODE_OAUTH_TOKEN=o", "OPENAI_API_KEY=ok", "CODEX_API_KEY=ck", "HOME=/h"}
	cases := []struct {
		name                         string
		claudeExplicit, codexExplict bool
		want                         []string
	}{
		{"default keeps auth, drops config dir", false, false,
			[]string{"PATH=/bin", "ANTHROPIC_API_KEY=k", "ANTHROPIC_AUTH_TOKEN=t", "CLAUDE_CODE_OAUTH_TOKEN=o", "OPENAI_API_KEY=ok", "CODEX_API_KEY=ck", "HOME=/h"}},
		{"claude explicit drops claude auth overrides", true, false,
			[]string{"PATH=/bin", "OPENAI_API_KEY=ok", "CODEX_API_KEY=ck", "HOME=/h"}},
		{"codex explicit drops codex auth overrides and config dir", false, true,
			[]string{"PATH=/bin", "ANTHROPIC_API_KEY=k", "ANTHROPIC_AUTH_TOKEN=t", "CLAUDE_CODE_OAUTH_TOKEN=o", "HOME=/h"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			testutil.DeepEqual(t, spawnBaseEnv(base, spawnAuthStrip(tc.claudeExplicit, tc.codexExplict), "t1"), tc.want)
		})
	}
}

func TestBuildCmd_AccountEnvHygiene(t *testing.T) {
	seedDefaultClaude(t)
	acctDir := filepath.Join(t.TempDir(), "work")
	t.Setenv("CLAUDE_CONFIG_DIR", "/inherited")
	t.Setenv("ANTHROPIC_API_KEY", "inherited-key")

	countPrefix := func(env []string, prefix string) int {
		n := 0
		for _, e := range env {
			if strings.HasPrefix(e, prefix) {
				n++
			}
		}
		return n
	}

	t.Run("explicit claude account", func(t *testing.T) {
		task := &model.Task{Name: "t", Prompt: "go", Worktree: t.TempDir(), Account: "work"}
		cmd, _, err := BuildCmd(task, acctCfg(acctDir), false)
		testutil.NoError(t, err)
		testutil.Equal(t, countPrefix(cmd.Env, "CLAUDE_CONFIG_DIR="), 1)
		testutil.True(t, envHas(cmd.Env, "CLAUDE_CONFIG_DIR="+acctDir))
		testutil.Equal(t, countPrefix(cmd.Env, "ANTHROPIC_API_KEY="), 0)
	})

	t.Run("explicit backend env_vars mapping still wins", func(t *testing.T) {
		t.Setenv("ARGUS_TEST_SRC_KEY", "mapped")
		cfg := acctCfg(acctDir)
		b := cfg.Backends["claude"]
		b.EnvVars = map[string]string{"ANTHROPIC_API_KEY": "ARGUS_TEST_SRC_KEY"}
		cfg.Backends["claude"] = b
		task := &model.Task{Name: "t", Prompt: "go", Worktree: t.TempDir(), Account: "work"}
		cmd, _, err := BuildCmd(task, cfg, false)
		testutil.NoError(t, err)
		testutil.True(t, envHas(cmd.Env, "ANTHROPIC_API_KEY=mapped"))
		testutil.False(t, envHas(cmd.Env, "ANTHROPIC_API_KEY=inherited-key"))
	})

	t.Run("default claude account strips inherited config dir", func(t *testing.T) {
		task := &model.Task{Name: "t", Prompt: "go", Worktree: t.TempDir()}
		cmd, _, err := BuildCmd(task, acctCfg(acctDir), false)
		testutil.NoError(t, err)
		testutil.Equal(t, countPrefix(cmd.Env, "CLAUDE_CONFIG_DIR="), 0)
		testutil.True(t, envHas(cmd.Env, "ANTHROPIC_API_KEY=inherited-key"))
	})

	t.Run("non-claude backend strips inherited config dir", func(t *testing.T) {
		task := &model.Task{Name: "t", Backend: "pi", Prompt: "go", Worktree: t.TempDir()}
		cmd, _, err := BuildCmd(task, acctCfg(acctDir), false)
		testutil.NoError(t, err)
		testutil.Equal(t, countPrefix(cmd.Env, "CLAUDE_CONFIG_DIR="), 0)
	})
}

func TestSandboxWithClaudeConfigDir(t *testing.T) {
	src := seedDefaultClaude(t)
	outside := filepath.Join(t.TempDir(), "work")
	base := config.SandboxConfig{ExtraWrite: []string{"/x"}}

	testutil.DeepEqual(t, sandboxWithClaudeConfigDir(base, "").ExtraWrite, []string{"/x"})
	testutil.DeepEqual(t, sandboxWithClaudeConfigDir(base, filepath.Join(src, "acct")).ExtraWrite, []string{"/x"})
	sc := sandboxWithClaudeConfigDir(base, outside)
	testutil.DeepEqual(t, sc.ExtraWrite, []string{"/x", outside})
	testutil.DeepEqual(t, base.ExtraWrite, []string{"/x"})

	path, _, cleanup, err := GenerateSandboxConfig(t.TempDir(), sc)
	testutil.NoError(t, err)
	defer cleanup()
	b, err := os.ReadFile(path)
	testutil.NoError(t, err)
	testutil.Contains(t, string(b), "(allow file-write* (subpath \""+evalSymlinksOrKeep(outside)+"\"))")
}

// TestBuildCmd_SandboxGrantsOnlyTaskAccount pins that the sandbox grants write
// access to the task's own account dir only, and only for a Claude task.
func TestBuildCmd_SandboxGrantsOnlyTaskAccount(t *testing.T) {
	if !IsSandboxAvailable() {
		t.Skip("sandbox-exec unavailable")
	}
	seedDefaultClaude(t)
	work := filepath.Join(t.TempDir(), "work")
	other := filepath.Join(t.TempDir(), "other")
	cfg := acctCfg(work)
	cfg.Accounts["other"] = config.Account{ClaudeConfigDir: other}
	cfg.Sandbox.Enabled = true

	profileOf := func(t *testing.T, task *model.Task) string {
		t.Helper()
		cmd, cleanup, err := BuildCmd(task, cfg, false)
		testutil.NoError(t, err)
		t.Cleanup(cleanup)
		script := cmd.Args[len(cmd.Args)-1]
		_, rest, ok := strings.Cut(script, " -f ")
		testutil.True(t, ok)
		path := strings.Trim(strings.Fields(rest)[0], "'")
		b, err := os.ReadFile(path)
		testutil.NoError(t, err)
		return string(b)
	}

	t.Run("claude task on work", func(t *testing.T) {
		p := profileOf(t, &model.Task{Name: "t", Prompt: "go", Worktree: t.TempDir(), Account: "work"})
		testutil.Contains(t, p, evalSymlinksOrKeep(work))
		testutil.False(t, strings.Contains(p, evalSymlinksOrKeep(other)))
	})
	t.Run("non-claude task gets no account dir", func(t *testing.T) {
		p := profileOf(t, &model.Task{Name: "t", Backend: "pi", Prompt: "go", Worktree: t.TempDir(), Account: "work"})
		testutil.False(t, strings.Contains(p, evalSymlinksOrKeep(work)))
		testutil.False(t, strings.Contains(p, evalSymlinksOrKeep(other)))
	})
}

func TestClaudeConfigDirFromEnv(t *testing.T) {
	testutil.Equal(t, claudeConfigDirFromEnv(nil), "")
	testutil.Equal(t, claudeConfigDirFromEnv([]string{"CLAUDE_CONFIG_DIR=/a", "X=1", "CLAUDE_CONFIG_DIR=/b"}), "/b")
	testutil.Equal(t, (*Session)(nil).claudeConfigDir(), "")
}

// TestBuildCmd_AccountGetsProcessScopedMCP pins that an explicit-account task
// receives the argus MCP server the same way a default-account task does: via
// the process-scoped --mcp-config flag, with nothing written into either
// config dir.
func TestBuildCmd_AccountGetsProcessScopedMCP(t *testing.T) {
	src := seedDefaultClaude(t)
	acctDir := filepath.Join(t.TempDir(), "work")
	cfg := acctCfg(acctDir)
	cfg.MCPPort = 7742
	for _, account := range []string{"", "work"} {
		t.Run("account="+account, func(t *testing.T) {
			task := &model.Task{Name: "t", Prompt: "go", Worktree: t.TempDir(), Account: account}
			cmd, _, err := BuildCmd(task, cfg, false)
			testutil.NoError(t, err)
			testutil.Contains(t, cmd.Args[len(cmd.Args)-1], "--mcp-config")
			testutil.Contains(t, cmd.Args[len(cmd.Args)-1], "http://localhost:7742/mcp")
		})
	}
	for _, p := range []string{filepath.Join(acctDir, ".claude.json"), filepath.Join(src, "settings.json"), filepath.Join(acctDir, "settings.json")} {
		_, err := os.Lstat(p)
		testutil.True(t, os.IsNotExist(err))
	}
}
