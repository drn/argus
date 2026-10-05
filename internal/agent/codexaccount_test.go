package agent

import (
	"context"
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/skills"
	"github.com/drn/argus/internal/testutil"
)

func resetCodexLoginCacheForTest(t *testing.T) {
	t.Helper()
	reset := func() {
		codexLoginMu.Lock()
		codexLoginCache = map[string]codexLoginEntry{}
		codexLoginMu.Unlock()
	}
	reset()
	t.Cleanup(reset)
}

// codexAcctCfg has a codex-capable "cx" account and a claude-only "work" one.
func codexAcctCfg(codexHome string) config.Config {
	cfg := permModeConfig("")
	cfg.Accounts = map[string]config.Account{
		"cx":   {CodexHome: codexHome},
		"work": {ClaudeConfigDir: filepath.Join(filepath.Dir(codexHome), "claude-work")},
	}
	return cfg
}

func codexTask(t *testing.T, account string) *model.Task {
	return &model.Task{ID: "t1", Name: "t", Backend: "codex", Prompt: "go", Worktree: t.TempDir(), Account: account}
}

func stubCodexOverlay(t *testing.T, overlay string) *[]string {
	t.Helper()
	var sources []string
	t.Cleanup(SetEnsureCodexSkillsForTest(func(source string) (string, error) {
		sources = append(sources, source)
		return overlay, nil
	}))
	return &sources
}

func TestResolveSpawnCodexHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	acct := filepath.Join(t.TempDir(), "cx-home")
	cfg := codexAcctCfg(acct)

	cases := []struct {
		name    string
		account string
		isCodex bool
		want    string
		wantErr string
	}{
		{"default account", "", true, "", ""},
		{"explicit default", "default", true, "", ""},
		{"non-codex backend ignores account", "cx", false, "", ""},
		{"unknown account fails loud", "gone", true, "", "gone"},
		{"claude-only account cannot run codex", "work", true, "", "no codex_home"},
		{"explicit codex account", "cx", true, acct, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveSpawnCodexHome(&model.Task{ID: "t1", Account: tc.account}, cfg, tc.isCodex)
			if tc.wantErr != "" {
				testutil.Error(t, err)
				testutil.Contains(t, err.Error(), tc.wantErr)
				return
			}
			testutil.NoError(t, err)
			testutil.Equal(t, got, tc.want)
		})
	}

	t.Run("bootstrap creates 0700 home and inherits nothing", func(t *testing.T) {
		info, err := os.Stat(acct)
		testutil.NoError(t, err)
		testutil.Equal(t, info.Mode().Perm(), os.FileMode(0o700))
		entries, err := os.ReadDir(acct)
		testutil.NoError(t, err)
		testutil.Equal(t, len(entries), 0)
		_, err = os.Stat(filepath.Join(home, ".codex"))
		testutil.True(t, os.IsNotExist(err))
	})
}

func TestCodexHomeForTask(t *testing.T) {
	acct := filepath.Join(t.TempDir(), "cx-home")
	cfg := codexAcctCfg(acct)
	t.Run("nil task", func(t *testing.T) {
		_, err := CodexHomeForTask(nil, cfg)
		testutil.Error(t, err)
	})
	t.Run("default", func(t *testing.T) {
		got, err := CodexHomeForTask(&model.Task{Account: "default"}, cfg)
		testutil.NoError(t, err)
		testutil.Equal(t, got, "")
	})
	t.Run("explicit", func(t *testing.T) {
		got, err := CodexHomeForTask(&model.Task{Account: "cx"}, cfg)
		testutil.NoError(t, err)
		testutil.Equal(t, got, acct)
	})
	t.Run("unknown", func(t *testing.T) {
		_, err := CodexHomeForTask(&model.Task{Account: "gone"}, cfg)
		testutil.Error(t, err)
	})
}

func TestBuildCmd_CodexAccountEnv(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", "")
	t.Setenv("CODEX_SQLITE_HOME", "/inherited-sqlite")
	t.Setenv("OPENAI_API_KEY", "inherited-openai")
	t.Setenv("CODEX_API_KEY", "inherited-codex")
	t.Setenv("CLAUDE_CONFIG_DIR", "/inherited-claude")
	acct := filepath.Join(t.TempDir(), "cx-home")
	overlay := filepath.Join(t.TempDir(), "overlay")

	lastEnv := func(env []string, name string) (string, bool) {
		val, found := "", false
		for _, kv := range env {
			if v, ok := strings.CutPrefix(kv, name+"="); ok {
				val, found = v, true
			}
		}
		return val, found
	}

	t.Run("explicit account uses its own home everywhere", func(t *testing.T) {
		sources := stubCodexOverlay(t, overlay)
		cmd, _, err := BuildCmd(codexTask(t, "cx"), codexAcctCfg(acct), false)
		testutil.NoError(t, err)
		testutil.DeepEqual(t, *sources, []string{acct})
		v, _ := lastEnv(cmd.Env, "CODEX_HOME")
		testutil.Equal(t, v, overlay)
		v, _ = lastEnv(cmd.Env, "CODEX_SQLITE_HOME")
		testutil.Equal(t, v, acct)
		for _, name := range []string{"OPENAI_API_KEY", "CODEX_API_KEY", "CLAUDE_CONFIG_DIR"} {
			_, found := lastEnv(cmd.Env, name)
			testutil.False(t, found)
		}
	})

	t.Run("overlay failure still exports the account home", func(t *testing.T) {
		t.Cleanup(SetEnsureCodexSkillsForTest(func(string) (string, error) {
			return "", os.ErrPermission
		}))
		cmd, _, err := BuildCmd(codexTask(t, "cx"), codexAcctCfg(acct), false)
		testutil.NoError(t, err)
		v, _ := lastEnv(cmd.Env, "CODEX_HOME")
		testutil.Equal(t, v, acct)
		v, _ = lastEnv(cmd.Env, "CODEX_SQLITE_HOME")
		testutil.Equal(t, v, acct)
	})

	t.Run("backend env_vars mapping still wins", func(t *testing.T) {
		stubCodexOverlay(t, overlay)
		t.Setenv("ARGUS_TEST_CODEX_SRC", "mapped")
		cfg := codexAcctCfg(acct)
		b := cfg.Backends["codex"]
		b.EnvVars = map[string]string{"OPENAI_API_KEY": "ARGUS_TEST_CODEX_SRC"}
		cfg.Backends["codex"] = b
		cmd, _, err := BuildCmd(codexTask(t, "cx"), cfg, false)
		testutil.NoError(t, err)
		v, _ := lastEnv(cmd.Env, "OPENAI_API_KEY")
		testutil.Equal(t, v, "mapped")
	})

	t.Run("default account keeps today's behavior", func(t *testing.T) {
		sources := stubCodexOverlay(t, overlay)
		cmd, _, err := BuildCmd(codexTask(t, ""), codexAcctCfg(acct), false)
		testutil.NoError(t, err)
		testutil.DeepEqual(t, *sources, []string{""})
		v, _ := lastEnv(cmd.Env, "CODEX_HOME")
		testutil.Equal(t, v, overlay)
		v, _ = lastEnv(cmd.Env, "CODEX_SQLITE_HOME")
		testutil.Equal(t, v, "/inherited-sqlite")
		v, _ = lastEnv(cmd.Env, "OPENAI_API_KEY")
		testutil.Equal(t, v, "inherited-openai")
		_, found := lastEnv(cmd.Env, "CLAUDE_CONFIG_DIR")
		testutil.False(t, found)
	})

	t.Run("unknown account refuses to spawn", func(t *testing.T) {
		stubCodexOverlay(t, overlay)
		_, _, err := BuildCmd(codexTask(t, "gone"), codexAcctCfg(acct), false)
		testutil.Error(t, err)
		testutil.Contains(t, err.Error(), "gone")
	})

	t.Run("claude task on a codex-capable account exports no codex home", func(t *testing.T) {
		sources := stubCodexOverlay(t, overlay)
		cfg := codexAcctCfg(acct)
		cfg.Accounts["both"] = config.Account{CodexHome: acct, ClaudeConfigDir: filepath.Join(t.TempDir(), "c")}
		task := &model.Task{ID: "t1", Name: "t", Prompt: "go", Worktree: t.TempDir(), Account: "both"}
		cmd, _, err := BuildCmd(task, cfg, false)
		testutil.NoError(t, err)
		testutil.Equal(t, len(*sources), 0)
		v, _ := lastEnv(cmd.Env, "CODEX_HOME")
		testutil.Equal(t, v, "")
		v, _ = lastEnv(cmd.Env, "OPENAI_API_KEY")
		testutil.Equal(t, v, "inherited-openai")
	})
}

func TestBuildCmd_CodexAccountGetsPerLaunchMCP(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CODEX_HOME", "")
	acct := filepath.Join(t.TempDir(), "cx-home")
	stubCodexOverlay(t, filepath.Join(t.TempDir(), "overlay"))
	cfg := codexAcctCfg(acct)
	cfg.MCPPort = 7742

	for _, account := range []string{"", "cx"} {
		t.Run("account="+account, func(t *testing.T) {
			cmd, _, err := BuildCmd(codexTask(t, account), cfg, false)
			testutil.NoError(t, err)
			testutil.Contains(t, cmd.Args[len(cmd.Args)-1], "mcp_servers.argus.url=")
		})
	}
	t.Run("no config written into the account home", func(t *testing.T) {
		_, err := os.Stat(filepath.Join(acct, "config.toml"))
		testutil.True(t, os.IsNotExist(err))
	})
}

func TestSandboxWithCodexHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	base := config.SandboxConfig{ExtraWrite: []string{"/x"}}
	t.Run("empty dir unchanged", func(t *testing.T) {
		testutil.DeepEqual(t, sandboxWithCodexHome(base, "").ExtraWrite, []string{"/x"})
	})
	t.Run("default codex home already writable", func(t *testing.T) {
		testutil.DeepEqual(t, sandboxWithCodexHome(base, filepath.Join(home, ".codex")).ExtraWrite, []string{"/x"})
		testutil.DeepEqual(t, sandboxWithCodexHome(base, filepath.Join(home, ".codex", "sub")).ExtraWrite, []string{"/x"})
	})
	t.Run("account home appended without aliasing", func(t *testing.T) {
		got := sandboxWithCodexHome(base, "/accts/cx")
		testutil.DeepEqual(t, got.ExtraWrite, []string{"/x", "/accts/cx"})
		testutil.DeepEqual(t, base.ExtraWrite, []string{"/x"})
	})
}

func TestBuildCmd_SandboxGrantsCodexAccountHome(t *testing.T) {
	if !IsSandboxAvailable() {
		t.Skip("sandbox-exec unavailable")
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CODEX_HOME", "")
	acct := filepath.Join(t.TempDir(), "cx-home")
	other := filepath.Join(t.TempDir(), "other-home")
	stubCodexOverlay(t, filepath.Join(t.TempDir(), "overlay"))
	cfg := codexAcctCfg(acct)
	cfg.Accounts["other"] = config.Account{CodexHome: other}
	cfg.Sandbox.Enabled = true

	profileOf := func(t *testing.T, task *model.Task) string {
		t.Helper()
		cmd, cleanup, err := BuildCmd(task, cfg, false)
		testutil.NoError(t, err)
		t.Cleanup(cleanup)
		script := cmd.Args[len(cmd.Args)-1]
		_, rest, ok := strings.Cut(script, " -f ")
		testutil.True(t, ok)
		b, err := os.ReadFile(strings.Trim(strings.Fields(rest)[0], "'"))
		testutil.NoError(t, err)
		return string(b)
	}

	t.Run("codex task on cx", func(t *testing.T) {
		p := profileOf(t, codexTask(t, "cx"))
		testutil.Contains(t, p, evalSymlinksOrKeep(acct))
		testutil.False(t, strings.Contains(p, evalSymlinksOrKeep(other)))
	})
	t.Run("default codex task gets no account home", func(t *testing.T) {
		p := profileOf(t, codexTask(t, ""))
		testutil.False(t, strings.Contains(p, evalSymlinksOrKeep(acct)))
	})
}

func seedCodexStateDB(t *testing.T, dir, cwd, id string) {
	t.Helper()
	testutil.NoError(t, os.MkdirAll(dir, 0o700))
	conn, err := sql.Open("sqlite", filepath.Join(dir, codexStateDB))
	testutil.NoError(t, err)
	defer func() { _ = conn.Close() }()
	_, err = conn.Exec(`CREATE TABLE threads (id TEXT, cwd TEXT, updated_at INTEGER)`)
	testutil.NoError(t, err)
	_, err = conn.Exec(`INSERT INTO threads VALUES (?, ?, 1)`, id, cwd)
	testutil.NoError(t, err)
}

func TestCaptureSessionID_CodexAccount(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", "")
	t.Setenv("CODEX_SQLITE_HOME", "")
	acct := filepath.Join(t.TempDir(), "cx-home")
	wt := "/wt/acct"
	const acctID = "11111111-2222-3333-4444-555555555555"
	seedCodexStateDB(t, acct, wt, acctID)
	cfg := codexAcctCfg(acct)

	t.Run("explicit account reads its own state db", func(t *testing.T) {
		got, err := CaptureSessionID(&model.Task{Backend: "codex", Worktree: wt, Account: "cx"}, cfg)
		testutil.NoError(t, err)
		testutil.Equal(t, got, acctID)
	})
	t.Run("default account does not see the account db", func(t *testing.T) {
		_, err := CaptureSessionID(&model.Task{Backend: "codex", Worktree: wt}, cfg)
		testutil.Error(t, err)
	})
	t.Run("unknown account fails loud", func(t *testing.T) {
		_, err := CaptureSessionID(&model.Task{Backend: "codex", Worktree: wt, Account: "gone"}, cfg)
		testutil.Error(t, err)
	})
	t.Run("explicit home beats CODEX_SQLITE_HOME", func(t *testing.T) {
		t.Setenv("CODEX_SQLITE_HOME", t.TempDir())
		got, err := CaptureCodexSessionIDIn(acct, wt)
		testutil.NoError(t, err)
		testutil.Equal(t, got, acctID)
	})
}

func TestCodexLoginStatus_InertUnderTest(t *testing.T) {
	_, err := CodexLoginStatus(context.Background(), "")
	testutil.ErrorIs(t, err, errCodexLoginUnknown)
}

func TestCodexLoginStatus(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", "")
	acct := filepath.Join(t.TempDir(), "cx-home")

	stub := func(t *testing.T, script string) *[]string {
		t.Helper()
		resetCodexLoginCacheForTest(t)
		var homes []string
		old := codexLoginCmd
		codexLoginCmd = func(ctx context.Context, codexHome string) *exec.Cmd {
			homes = append(homes, codexHome)
			return exec.CommandContext(ctx, "sh", "-c", script)
		}
		t.Cleanup(func() { codexLoginCmd = old })
		return &homes
	}

	t.Run("exit 0 is logged in, using the source home without an overlay", func(t *testing.T) {
		homes := stub(t, "exit 0")
		ok, err := codexLoginStatus(context.Background(), acct)
		testutil.NoError(t, err)
		testutil.True(t, ok)
		testutil.DeepEqual(t, *homes, []string{acct})
	})
	t.Run("non-zero exit is logged out", func(t *testing.T) {
		stub(t, "exit 1")
		ok, err := codexLoginStatus(context.Background(), acct)
		testutil.NoError(t, err)
		testutil.False(t, ok)
	})
	t.Run("result is cached per home", func(t *testing.T) {
		homes := stub(t, "exit 0")
		_, _ = codexLoginStatus(context.Background(), acct)
		_, _ = codexLoginStatus(context.Background(), acct)
		testutil.Equal(t, len(*homes), 1)
	})
	t.Run("overlay home preferred once Argus built it", func(t *testing.T) {
		homes := stub(t, "exit 0")
		overlay, err := skills.ArgusCodexHomeFor(acct)
		testutil.NoError(t, err)
		testutil.NoError(t, os.MkdirAll(filepath.Join(overlay, "skills"), 0o700))
		_, err = codexLoginStatus(context.Background(), acct)
		testutil.NoError(t, err)
		testutil.DeepEqual(t, *homes, []string{overlay})
	})
	t.Run("default source resolves the user codex home", func(t *testing.T) {
		homes := stub(t, "exit 0")
		_, err := codexLoginStatus(context.Background(), "")
		testutil.NoError(t, err)
		testutil.DeepEqual(t, *homes, []string{filepath.Join(home, ".codex")})
	})
	t.Run("missing binary is an error", func(t *testing.T) {
		resetCodexLoginCacheForTest(t)
		old := codexLoginCmd
		codexLoginCmd = func(ctx context.Context, _ string) *exec.Cmd {
			return exec.CommandContext(ctx, filepath.Join(t.TempDir(), "no-such-codex"))
		}
		t.Cleanup(func() { codexLoginCmd = old })
		_, err := codexLoginStatus(context.Background(), filepath.Join(t.TempDir(), "x"))
		testutil.Error(t, err)
	})
	t.Run("cancelled context is an error", func(t *testing.T) {
		stub(t, "sleep 5")
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := codexLoginStatus(ctx, filepath.Join(t.TempDir(), "y"))
		testutil.Error(t, err)
	})
	t.Run("real command sets CODEX_HOME and drops inherited one", func(t *testing.T) {
		t.Setenv("CODEX_HOME", "/inherited")
		cmd := codexLoginCmd(context.Background(), acct)
		testutil.DeepEqual(t, cmd.Args, []string{"codex", "login", "status"})
		n := 0
		for _, kv := range cmd.Env {
			if strings.HasPrefix(kv, "CODEX_HOME=") {
				n++
				testutil.Equal(t, kv, "CODEX_HOME="+acct)
			}
		}
		testutil.Equal(t, n, 1)
	})
}
