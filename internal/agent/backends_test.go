package agent

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/drn/argus/internal/backendkind"
	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

func TestFamilyRegistry_CoversEveryKind(t *testing.T) {
	seen := map[backendkind.Kind]bool{}
	for _, f := range Families() {
		testutil.Equal(t, seen[f.Kind], false)
		seen[f.Kind] = true
		testutil.Equal(t, f.IsCustom(), false)
	}
	for _, s := range backendkind.All() {
		if !seen[s.Kind] {
			t.Errorf("backendkind %q has no agent.Family entry", s.Kind)
		}
	}
	testutil.Equal(t, len(seen), len(backendkind.All()))
}

func TestFamilyRegistry_Invariants(t *testing.T) {
	for _, f := range append(Families(), customFamily) {
		t.Run(string(f.Kind), func(t *testing.T) {
			if f.ResumeCmd == nil && f.ResumeFlag == "" {
				t.Error("family has no resume form")
			}
			if !f.PinsSessionID && f.Capture == nil {
				t.Error("capture-style family must declare Capture")
			}
			if f.RecaptureEveryExit && f.Capture == nil {
				t.Error("RecaptureEveryExit without Capture")
			}
			if f.PermissionFlags != nil && len(f.PermissionFlagTokens) == 0 {
				t.Error("PermissionFlags without PermissionFlagTokens would double-inject")
			}
			if f.SkillDelivery == SkillsGlobal && f.InjectGlobal == nil {
				t.Error("SkillsGlobal needs InjectGlobal to register the dir")
			}
		})
	}
}

func TestFamilyOf(t *testing.T) {
	testutil.Equal(t, FamilyOf("agy").Kind, backendkind.Antigravity)
	testutil.Equal(t, FamilyOf("/opt/homebrew/bin/agy --x").Kind, backendkind.Antigravity)
	testutil.Equal(t, FamilyOf("my-wrapper").IsCustom(), true)
	testutil.Equal(t, FamilyForKind("nope").IsCustom(), true)
	testutil.Equal(t, IsAntigravityBackend("antigravity"), true)
	testutil.Equal(t, IsAntigravityBackend("agy-helper"), false)
}

func TestFamily_HasPermissionFlag(t *testing.T) {
	agy := FamilyForKind(backendkind.Antigravity)
	claude := FamilyForKind(backendkind.Claude)
	cases := []struct {
		name    string
		fam     *Family
		command string
		want    bool
	}{
		{"agy skip", agy, "agy --dangerously-skip-permissions", true},
		{"agy mode value", agy, "agy --mode plan", true},
		{"agy mode equals", agy, "agy --mode=plan", true},
		{"agy --model is not --mode", agy, "agy --model gemini", false},
		{"agy --model then --mode", agy, "agy --model x --mode plan", true},
		{"agy trailing --mode", agy, "agy --mode", true},
		{"claude flag after separator still counts", claude, "claude -- --permission-mode", true},
		{"claude quoted flag", claude, `claude "--permission-mode" plan`, true},
		{"claude equals form", claude, "claude --permission-mode=plan", true},
		{"claude legacy substring semantics", claude, "claude --permission-modeX", true},
		{"claude permission-mode", claude, "claude --permission-mode plan", true},
		{"claude allow-dangerously", claude, "claude --allow-dangerously-skip-permissions", true},
		{"claude bare", claude, "claude", false},
		{"custom never", customFamily, "x --mode plan", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			testutil.Equal(t, tc.fam.HasPermissionFlag(tc.command), tc.want)
		})
	}
}

func TestAntigravityPermissionFlags(t *testing.T) {
	cases := map[string]string{
		config.PermissionModeDefault:      "",
		config.PermissionModeAcceptEdits:  "--mode accept-edits",
		config.PermissionModePlan:         "--mode plan",
		config.PermissionModeBypassAllow:  "--mode plan",
		config.PermissionModeBypassActive: "--dangerously-skip-permissions",
		"":                                "",
	}
	for mode, want := range cases {
		t.Run(mode, func(t *testing.T) {
			testutil.Equal(t, antigravityPermissionFlags(mode), want)
		})
	}
}

func TestFamily_PromptFlagFor(t *testing.T) {
	agy := FamilyForKind(backendkind.Antigravity)
	testutil.Equal(t, agy.PromptFlagFor(config.Backend{}), "--prompt-interactive")
	testutil.Equal(t, agy.PromptFlagFor(config.Backend{PromptFlag: "-i"}), "-i")
	testutil.Equal(t, customFamily.PromptFlagFor(config.Backend{}), "")
}

func agyConfig(command string) config.Config {
	cfg := config.DefaultConfig()
	cfg.Backends["antigravity"] = config.Backend{Command: command, PromptFlag: cfg.Backends["antigravity"].PromptFlag}
	return cfg
}

func TestBuildCmd_Antigravity(t *testing.T) {
	cases := []struct {
		name   string
		cfg    func() config.Config
		task   model.Task
		resume bool
		want   string
	}{
		{
			name: "default config new session",
			cfg:  config.DefaultConfig,
			task: model.Task{Backend: "antigravity", Prompt: "fix it", SessionID: "ignored"},
			want: "agy --dangerously-skip-permissions --prompt-interactive 'fix it'",
		},
		{
			name: "stored backend without prompt flag uses family default",
			cfg: func() config.Config {
				cfg := config.DefaultConfig()
				cfg.Backends["antigravity"] = config.Backend{Command: "agy"}
				return cfg
			},
			task: model.Task{Backend: "antigravity", Prompt: "-starts with dash"},
			want: "agy --dangerously-skip-permissions --prompt-interactive '-starts with dash'",
		},
		{
			name:   "resume with conversation id drops prompt",
			cfg:    config.DefaultConfig,
			task:   model.Task{Backend: "antigravity", Prompt: "fix it", SessionID: "c0ffee"},
			resume: true,
			want:   "agy --dangerously-skip-permissions --conversation 'c0ffee'",
		},
		{
			name:   "resume without id starts fresh",
			cfg:    config.DefaultConfig,
			task:   model.Task{Backend: "antigravity", Prompt: "fix it"},
			resume: true,
			want:   "agy --dangerously-skip-permissions",
		},
		{
			name: "model flag",
			cfg:  config.DefaultConfig,
			task: model.Task{Backend: "antigravity", Prompt: "p", Model: "gemini-3.1-pro"},
			want: "agy --dangerously-skip-permissions --model 'gemini-3.1-pro' --prompt-interactive 'p'",
		},
		{
			name: "hand-edited mode wins",
			cfg:  func() config.Config { return agyConfig("agy --mode plan") },
			task: model.Task{Backend: "antigravity", Prompt: "p"},
			want: "agy --mode plan --prompt-interactive 'p'",
		},
		{
			name: "accept-edits mode mapped",
			cfg: func() config.Config {
				cfg := config.DefaultConfig()
				cfg.Defaults.PermissionMode = config.PermissionModeAcceptEdits
				return cfg
			},
			task: model.Task{Backend: "antigravity", Prompt: "p"},
			want: "agy --mode accept-edits --prompt-interactive 'p'",
		},
		{
			name: "mcp port adds no per-launch flag",
			cfg: func() config.Config {
				cfg := config.DefaultConfig()
				cfg.MCPPort = 7742
				return cfg
			},
			task: model.Task{Backend: "antigravity", Prompt: "p"},
			want: "agy --dangerously-skip-permissions --prompt-interactive 'p'",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			task := tc.task
			task.Worktree = t.TempDir()
			cmd, _, err := BuildCmd(&task, tc.cfg(), tc.resume)
			testutil.NoError(t, err)
			testutil.Equal(t, cmd.Args[2], tc.want)
		})
	}
}

func TestBuildCmd_AntigravityMaterializesSkillsWithoutFlag(t *testing.T) {
	called := false
	old := ensureSessionSkillsFn
	ensureSessionSkillsFn = func() (string, error) { called = true; return "/argus/skills", nil }
	defer func() { ensureSessionSkillsFn = old }()

	task := &model.Task{Backend: "antigravity", Prompt: "p", Worktree: t.TempDir()}
	cmd, _, err := BuildCmd(task, config.DefaultConfig(), false)
	testutil.NoError(t, err)
	testutil.Equal(t, called, true)
	if strings.Contains(cmd.Args[2], "/argus/skills") {
		t.Fatalf("SkillsGlobal must not add a per-launch flag: %q", cmd.Args[2])
	}
}

func TestBackendSandboxRules(t *testing.T) {
	rules := backendSandboxRules()
	for _, want := range []string{
		`(allow file-write* (subpath (string-append (param "HOME") "/.gemini")))`,
		`(allow file-write* (prefix (string-append (param "HOME") "/.claude.json")))`,
		`(allow file-write* (subpath (string-append (param "HOME") "/.codex")))`,
		`(allow file-write* (subpath (string-append (param "HOME") "/.pi")))`,
		"; Antigravity: ",
	} {
		testutil.Contains(t, rules, want)
	}
}

func TestCaptureAndRecapture_Antigravity(t *testing.T) {
	wt := t.TempDir()
	seedAntigravityDB(t, agyRow{"conv-1", "2026-10-02 10:00:00", `["file://` + wt + `"]`, ""})
	cfg := config.DefaultConfig()
	task := &model.Task{ID: "t1", Backend: "antigravity", Worktree: wt, SessionID: "older"}

	testutil.Equal(t, NeedsSessionRecapture(task, cfg), true)
	id, err := CaptureSessionID(task, cfg)
	testutil.NoError(t, err)
	testutil.Equal(t, id, "conv-1")
}

func TestCaptureSessionID_CustomCapturesNothing(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Backends["custom"] = config.Backend{Command: "my-agent"}
	task := &model.Task{Backend: "custom", Worktree: t.TempDir(), SessionID: "s"}
	id, err := CaptureSessionID(task, cfg)
	testutil.NoError(t, err)
	testutil.Equal(t, id, "")
	testutil.Equal(t, NeedsSessionRecapture(task, cfg), false)
}

func TestCaptureSessionID_FailureReturnsError(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	task := &model.Task{Backend: "antigravity", Worktree: t.TempDir()}
	_, err := CaptureSessionID(task, config.DefaultConfig())
	if err == nil {
		t.Fatal("expected capture error with no conversation DB")
	}
}

func TestRefreshResumeSessionID_AntigravityRefreshesToNewest(t *testing.T) {
	d := newResumeTestDB(t)
	wt := t.TempDir()
	seedAntigravityDB(t,
		agyRow{"first", "2026-10-01 10:00:00", `["file://` + wt + `"]`, ""},
		agyRow{"second", "2026-10-02 10:00:00", `["file://` + wt + `"]`, ""},
	)
	task := &model.Task{Name: "t", Project: "p", Worktree: wt, Backend: "antigravity", SessionID: "first"}
	testutil.NoError(t, d.Add(task))

	RefreshResumeSessionID(d, task)

	testutil.Equal(t, task.SessionID, "second")
	got, err := d.Get(task.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, got.SessionID, "second")
}

func TestProfileBackendNameAndModels_Antigravity(t *testing.T) {
	testutil.Equal(t, profileBackendName("my-agy", config.Backend{Command: "agy"}), "antigravity")
	testutil.Equal(t, profileBackendName("mine", config.Backend{Command: "custom"}), "mine")
	testutil.Equal(t, len(KnownModels("agy")), 0)
	models := KnownModels("claude")
	models[0] = "mutated"
	testutil.Equal(t, KnownModels("claude")[0], "opus")
}

func TestEnsurePrelaunch_FamilyHook(t *testing.T) {
	cfg := config.DefaultConfig()
	task := &model.Task{Backend: "antigravity"}
	testutil.NoError(t, EnsurePrelaunch(context.Background(), task, cfg))

	called := false
	pi := FamilyForKind(backendkind.Pi)
	old := pi.Prelaunch
	pi.Prelaunch = func(ctx context.Context, _ *model.Task, _ config.Backend) error {
		called = true
		if _, ok := ctx.Deadline(); !ok {
			t.Error("prelaunch ctx should carry the prelaunch timeout")
		}
		return nil
	}
	defer func() { pi.Prelaunch = old }()
	testutil.NoError(t, EnsurePrelaunch(context.Background(), &model.Task{Backend: "pi"}, cfg))
	testutil.Equal(t, called, true)
	testutil.Equal(t, pi.StartTimeout, 20*time.Second)
}

func TestInjectGlobalHooks(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	old := antigravityInstalledFn
	defer func() { antigravityInstalledFn = old }()

	antigravityInstalledFn = func() bool { return false }
	testutil.NoError(t, injectAntigravityGlobal(7742))
	if _, err := os.Stat(filepath.Join(home, ".gemini")); !os.IsNotExist(err) {
		t.Fatalf("agy not installed: ~/.gemini must not be created (stat err=%v)", err)
	}

	antigravityInstalledFn = func() bool { return true }
	testutil.NoError(t, injectAntigravityGlobal(7742))
	raw, err := os.ReadFile(filepath.Join(home, ".gemini", "config", "skills.json"))
	testutil.NoError(t, err)
	testutil.Contains(t, string(raw), filepath.Join(home, ".argus", "skills", ".claude", "skills"))

	testutil.NoError(t, injectOpencodeGlobal(7742))
	raw, err = os.ReadFile(filepath.Join(home, ".config", "opencode", "opencode.json"))
	testutil.NoError(t, err)
	testutil.Contains(t, string(raw), "http://localhost:7742/mcp")
}

func TestInjectAntigravityGlobal_UsesMaterializedSkillsDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	oldInstalled, oldSkills := antigravityInstalledFn, ensureSessionSkillsFn
	defer func() { antigravityInstalledFn, ensureSessionSkillsFn = oldInstalled, oldSkills }()
	antigravityInstalledFn = func() bool { return true }
	ensureSessionSkillsFn = func() (string, error) { return "/materialized", nil }

	testutil.NoError(t, injectAntigravityGlobal(1))
	raw, err := os.ReadFile(filepath.Join(home, ".gemini", "config", "skills.json"))
	testutil.NoError(t, err)
	testutil.Contains(t, string(raw), filepath.Join("/materialized", ".claude", "skills"))
}

func TestAntigravityInstalledFn(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", t.TempDir())
	testutil.Equal(t, antigravityInstalledFn(), false)
	// Bare ~/.gemini belongs to Gemini CLI too; only agy's own dir counts.
	testutil.NoError(t, os.MkdirAll(filepath.Join(home, ".gemini"), 0o755))
	testutil.Equal(t, antigravityInstalledFn(), false)
	testutil.NoError(t, os.MkdirAll(filepath.Join(home, ".gemini", "antigravity-cli"), 0o755))
	testutil.Equal(t, antigravityInstalledFn(), true)

	bin := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", bin)
	testutil.NoError(t, os.WriteFile(filepath.Join(bin, "agy"), []byte("#!/bin/sh\n"), 0o755))
	testutil.Equal(t, antigravityInstalledFn(), true)
}

func TestBuildCmd_InjectsGlobalConfigAtSpawn(t *testing.T) {
	var got []string
	old := injectGlobalAtSpawnFn
	injectGlobalAtSpawnFn = func(f *Family, port int) { got = append(got, string(f.Kind)+":"+strconv.Itoa(port)) }
	defer func() { injectGlobalAtSpawnFn = old }()

	cfg := config.DefaultConfig()
	for _, backend := range []string{"antigravity", "opencode", "claude", "codex"} {
		task := &model.Task{Backend: backend, Prompt: "p", Worktree: t.TempDir()}
		_, _, err := BuildCmd(task, cfg, false)
		testutil.NoError(t, err)
	}
	testutil.Equal(t, len(got), 0) // no MCP port, nothing to register

	cfg.MCPPort = 7742
	for _, backend := range []string{"antigravity", "opencode", "claude", "codex"} {
		task := &model.Task{Backend: backend, Prompt: "p", Worktree: t.TempDir()}
		_, _, err := BuildCmd(task, cfg, false)
		testutil.NoError(t, err)
	}
	testutil.DeepEqual(t, got, []string{"antigravity:7742", "opencode:7742"})
}

func TestInjectGlobalAtSpawnFn_InertUnderTest(t *testing.T) {
	called := false
	injectGlobalAtSpawnFn(&Family{InjectGlobal: func(int) error { called = true; return nil }}, 1)
	testutil.Equal(t, called, false)
}
