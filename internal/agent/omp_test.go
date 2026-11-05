package agent

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/drn/argus/internal/backendkind"
	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

func stubOmpSeams(t *testing.T, plugin, overlay string, pluginErr, overlayErr error) {
	t.Helper()
	oldPlugin, oldOverlay := ensureOmpMCPPluginFn, ensureOmpConfigOverlayFn
	ensureOmpMCPPluginFn = func(string) (string, error) { return plugin, pluginErr }
	ensureOmpConfigOverlayFn = func(string) (string, error) { return overlay, overlayErr }
	t.Cleanup(func() { ensureOmpMCPPluginFn, ensureOmpConfigOverlayFn = oldPlugin, oldOverlay })
}

func TestBuildCmd_Omp(t *testing.T) {
	withMode := func(mode string) func() config.Config {
		return func() config.Config {
			cfg := config.DefaultConfig()
			cfg.Defaults.PermissionMode = mode
			return cfg
		}
	}
	withCmd := func(command string) func() config.Config {
		return func() config.Config {
			cfg := config.DefaultConfig()
			cfg.Backends["omp"] = config.Backend{Command: command}
			return cfg
		}
	}
	cases := []struct {
		name   string
		cfg    func() config.Config
		task   model.Task
		resume bool
		want   string
	}{
		{
			name: "default config new session ignores pinned id",
			cfg:  config.DefaultConfig,
			task: model.Task{Backend: "omp", Prompt: "-fix it", SessionID: "ignored"},
			want: "omp --approval-mode yolo -- '-fix it'",
		},
		{
			name:   "resume drops prompt",
			cfg:    config.DefaultConfig,
			task:   model.Task{Backend: "omp", Prompt: "fix it", SessionID: "01a1a82e"},
			resume: true,
			want:   "omp --approval-mode yolo --resume '01a1a82e'",
		},
		{
			name:   "resume without id starts fresh",
			cfg:    config.DefaultConfig,
			task:   model.Task{Backend: "omp", Prompt: "fix it"},
			resume: true,
			want:   "omp --approval-mode yolo",
		},
		{
			name: "model flag",
			cfg:  config.DefaultConfig,
			task: model.Task{Backend: "omp", Prompt: "p", Model: "opus"},
			want: "omp --approval-mode yolo --model 'opus' -- 'p'",
		},
		{name: "accept edits", cfg: withMode(config.PermissionModeAcceptEdits), task: model.Task{Backend: "omp", Prompt: "p"}, want: "omp --approval-mode write -- 'p'"},
		{name: "plan", cfg: withMode(config.PermissionModePlan), task: model.Task{Backend: "omp", Prompt: "p"}, want: "omp --approval-mode always-ask -- 'p'"},
		{name: "bypass allow", cfg: withMode(config.PermissionModeBypassAllow), task: model.Task{Backend: "omp", Prompt: "p"}, want: "omp --approval-mode always-ask -- 'p'"},
		{name: "default mode asks instead of omp's yolo default", cfg: withMode(config.PermissionModeDefault), task: model.Task{Backend: "omp", Prompt: "p"}, want: "omp --approval-mode always-ask -- 'p'"},
		{name: "unknown mode injects nothing", cfg: withMode("bogus"), task: model.Task{Backend: "omp", Prompt: "p"}, want: "omp -- 'p'"},
		{name: "hand-edited auto-approve wins", cfg: withCmd("omp --auto-approve"), task: model.Task{Backend: "omp", Prompt: "p"}, want: "omp --auto-approve -- 'p'"},
		{name: "hand-edited approval mode wins", cfg: withCmd("omp --approval-mode=write"), task: model.Task{Backend: "omp", Prompt: "p"}, want: "omp --approval-mode=write -- 'p'"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stubOmpSeams(t, "", "", nil, nil)
			task := tc.task
			task.Worktree = t.TempDir()
			cmd, _, err := BuildCmd(&task, tc.cfg(), tc.resume)
			testutil.NoError(t, err)
			testutil.Equal(t, cmd.Args[2], tc.want)
		})
	}
}

func TestBuildCmd_OmpMCPAndSkills(t *testing.T) {
	oldSkills := ensureSessionSkillsFn
	ensureSessionSkillsFn = func() (string, error) { return "/argus/skills", nil }
	t.Cleanup(func() { ensureSessionSkillsFn = oldSkills })
	restoreRouting := SetEnsureBuiltinRoutingForTest(func() (string, error) { return "/argus/routing.md", nil })
	t.Cleanup(restoreRouting)

	var gotURL, gotSkills string
	oldPlugin, oldOverlay := ensureOmpMCPPluginFn, ensureOmpConfigOverlayFn
	ensureOmpMCPPluginFn = func(url string) (string, error) { gotURL = url; return "/cache/omp-mcp/abc", nil }
	ensureOmpConfigOverlayFn = func(dir string) (string, error) { gotSkills = dir; return "/cache/omp-config/def/argus.yml", nil }
	t.Cleanup(func() { ensureOmpMCPPluginFn, ensureOmpConfigOverlayFn = oldPlugin, oldOverlay })

	cfg := config.DefaultConfig()
	cfg.MCPPort = 7742
	task := model.Task{Backend: "omp", Prompt: "p", Worktree: t.TempDir()}
	cmd, _, err := BuildCmd(&task, cfg, false)
	testutil.NoError(t, err)
	got := cmd.Args[2]
	testutil.Equal(t, gotURL, "http://localhost:7742/mcp")
	testutil.Equal(t, gotSkills, filepath.Join("/argus/skills", ".claude", "skills"))
	testutil.Contains(t, got, "omp --plugin-dir '/cache/omp-mcp/abc'")
	testutil.Contains(t, got, " --config '/cache/omp-config/def/argus.yml'")
	testutil.Contains(t, got, " --append-system-prompt '/argus/routing.md'")
	testutil.Equal(t, strings.HasSuffix(got, " -- 'p'"), true)

	t.Run("materialize failures fail open", func(t *testing.T) {
		stubOmpSeams(t, "", "", errors.New("boom"), errors.New("boom"))
		task := model.Task{Backend: "omp", Prompt: "p", Worktree: t.TempDir()}
		cmd, _, err := BuildCmd(&task, cfg, false)
		testutil.NoError(t, err)
		testutil.Equal(t, strings.Contains(cmd.Args[2], "--plugin-dir"), false)
		testutil.Equal(t, strings.Contains(cmd.Args[2], "--config"), false)
	})
}

func TestOmpEncodeCwd(t *testing.T) {
	root := canonPath(t.TempDir())
	home := filepath.Join(root, "home")
	tmp := filepath.Join(root, "tmp")
	for _, d := range []string{home, tmp} {
		testutil.NoError(t, os.MkdirAll(d, 0o755))
	}
	cases := []struct {
		name, cwd, want string
	}{
		{"under home", filepath.Join(home, ".argus", "worktrees", "argus", "foo"), "-.argus-worktrees-argus-foo"},
		{"home itself", home, "-"},
		{"under tmp", filepath.Join(tmp, "x", "y"), "-tmp-x-y"},
		{"tmp itself", tmp, "-tmp"},
		{"dotdot-named dir is not inside", filepath.Join(home, "..sneaky"), ompAbsEncoded(filepath.Join(home, "..sneaky"))},
		{"elsewhere", "/opt/work:repo", "--opt-work-repo--"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			testutil.Equal(t, ompEncodeCwd(tc.cwd, home, tmp), tc.want)
		})
	}
	t.Run("no home or tmp", func(t *testing.T) {
		testutil.Equal(t, ompEncodeCwd("/a/b", "", ""), "--a-b--")
	})
	t.Run("symlinked cwd resolves", func(t *testing.T) {
		real := filepath.Join(home, "real")
		testutil.NoError(t, os.MkdirAll(real, 0o755))
		link := filepath.Join(root, "link")
		testutil.NoError(t, os.Symlink(real, link))
		testutil.Equal(t, ompEncodeCwd(link, home, tmp), "-real")
	})
}

func TestCaptureOmpSessionID(t *testing.T) {
	home := canonPath(t.TempDir())
	t.Setenv("HOME", home)
	// t.TempDir lives under TMPDIR, and omp's temp-dir rule outranks $HOME.
	t.Setenv("TMPDIR", "/nonexistent-omp-tmp")
	wt := filepath.Join(home, ".argus", "worktrees", "proj", "task")
	testutil.NoError(t, os.MkdirAll(wt, 0o755))
	dir := filepath.Join(home, ".omp", "agent", "sessions", "-.argus-worktrees-proj-task")
	testutil.NoError(t, os.MkdirAll(dir, 0o755))

	t.Run("empty worktree", func(t *testing.T) {
		_, err := CaptureOmpSessionID("")
		testutil.Equal(t, err != nil, true)
	})
	t.Run("no sessions", func(t *testing.T) {
		_, err := CaptureOmpSessionID(wt)
		testutil.Equal(t, err != nil, true)
	})

	write := func(name string, age time.Duration) {
		p := filepath.Join(dir, name)
		testutil.NoError(t, os.WriteFile(p, []byte("{}\n"), 0o644))
		ts := time.Now().Add(-age)
		testutil.NoError(t, os.Chtimes(p, ts, ts))
	}
	write("2026-11-04T20-00-00-000Z_01a1a82e-8305-7096-8943-a1e5b40fcb28.jsonl", 2*time.Hour)
	write("2026-11-04T21-00-00-000Z_019a0000-0000-7000-8000-000000000002.jsonl", time.Hour)
	write(".2026-11-04T22-00-00-000Z_019a0000-0000-7000-8000-000000000003.jsonl.lock.os", 0)
	write("notes.txt", 0)
	testutil.NoError(t, os.MkdirAll(filepath.Join(dir, "2026-11-04T23-00-00-000Z_019a0000-0000-7000-8000-000000000004"), 0o755))

	t.Run("newest top-level session", func(t *testing.T) {
		id, err := CaptureOmpSessionID(wt)
		testutil.NoError(t, err)
		testutil.Equal(t, id, "019a0000-0000-7000-8000-000000000002")
	})

	t.Run("legacy absolute encoding fallback", func(t *testing.T) {
		legacy := filepath.Join(home, ".omp", "agent", "sessions", ompAbsEncoded(canonPath(wt)))
		testutil.NoError(t, os.MkdirAll(legacy, 0o755))
		p := filepath.Join(legacy, "2026-11-05T00-00-00-000Z_019a0000-0000-7000-8000-000000000005.jsonl")
		testutil.NoError(t, os.WriteFile(p, []byte("{}\n"), 0o644))
		id, err := CaptureOmpSessionID(wt)
		testutil.NoError(t, err)
		testutil.Equal(t, id, "019a0000-0000-7000-8000-000000000005")
	})

	t.Run("family capture", func(t *testing.T) {
		fam := FamilyForKind(backendkind.Omp)
		id, err := fam.Capture(&model.Task{Worktree: wt}, config.Config{})
		testutil.NoError(t, err)
		testutil.Equal(t, id != "", true)
	})
}

func TestMaterializeOmpMCPPlugin(t *testing.T) {
	root := t.TempDir()
	dir, err := MaterializeOmpMCPPlugin(root, "http://localhost:7742/mcp")
	testutil.NoError(t, err)
	again, err := MaterializeOmpMCPPlugin(root, "http://localhost:7742/mcp")
	testutil.NoError(t, err)
	testutil.Equal(t, again, dir)
	other, err := MaterializeOmpMCPPlugin(root, "http://localhost:9000/mcp")
	testutil.NoError(t, err)
	testutil.Equal(t, other != dir, true)

	mcp, err := os.ReadFile(filepath.Join(dir, ".mcp.json"))
	testutil.NoError(t, err)
	testutil.Contains(t, string(mcp), `"url": "http://localhost:7742/mcp"`)
	testutil.Contains(t, string(mcp), `"type": "http"`)
	manifest, err := os.ReadFile(filepath.Join(dir, ".claude-plugin", "plugin.json"))
	testutil.NoError(t, err)
	testutil.Contains(t, string(manifest), `"name": "argus"`)

	entries, err := os.ReadDir(root)
	testutil.NoError(t, err)
	for _, e := range entries {
		testutil.Equal(t, strings.HasPrefix(e.Name(), ".tmp-"), false)
	}
}

func TestMaterializeOmpConfigOverlay(t *testing.T) {
	root := t.TempDir()
	path, err := MaterializeOmpConfigOverlay(root, "/a b/skills")
	testutil.NoError(t, err)
	body, err := os.ReadFile(path)
	testutil.NoError(t, err)
	testutil.Equal(t, string(body), "skills:\n  customDirectories:\n    - \"/a b/skills\"\n")

	t.Run("unwritable root errors", func(t *testing.T) {
		file := filepath.Join(t.TempDir(), "f")
		testutil.NoError(t, os.WriteFile(file, nil, 0o644))
		_, err := MaterializeOmpConfigOverlay(file, "/x")
		testutil.Equal(t, err != nil, true)
	})
}

func TestEnsureOmpSeamsInertUnderTest(t *testing.T) {
	dir, err := ensureOmpMCPPluginFn("http://localhost:1/mcp")
	testutil.NoError(t, err)
	testutil.Equal(t, dir, "")
	path, err := ensureOmpConfigOverlayFn("/x")
	testutil.NoError(t, err)
	testutil.Equal(t, path, "")
}

func TestOmpSandboxGrantLaunchOnly(t *testing.T) {
	fam := FamilyForKind(backendkind.Omp)
	testutil.Equal(t, fam.SandboxGrantsLaunchOnly, true)
	testutil.Equal(t, fam.SandboxGrants[0].HomeRel, "/.omp")
}

func TestBackendSandboxRules_OmpOwnLaunch(t *testing.T) {
	rules := backendSandboxRules(FamilyForKind(backendkind.Omp))
	testutil.Contains(t, rules, `(allow file-write* (subpath (string-append (param "HOME") "/.omp")))`)
}
