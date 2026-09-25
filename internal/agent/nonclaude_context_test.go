package agent

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

// containsAny reports whether s contains any of substrs.
func containsAny(s string, substrs ...string) bool {
	for _, sub := range substrs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

func TestBuildCmd_PiAndOpencodeSkillsAreSessionScoped(t *testing.T) {
	root := filepath.Join(t.TempDir(), "argus-skills")
	previous := ensureSessionSkillsFn
	ensureSessionSkillsFn = func() (string, error) { return root, nil }
	defer func() { ensureSessionSkillsFn = previous }()
	t.Setenv("OPENCODE_CONFIG_CONTENT", `{"theme":"custom","skills":["/my/skill"]}`)
	cfg := nonClaudeContextConfig()
	skillsDir := filepath.Join(root, ".claude", "skills")

	piTask := &model.Task{Name: "pi", Backend: "pi", Prompt: "go", Worktree: t.TempDir()}
	piCmd, _, err := BuildCmd(piTask, cfg, false)
	testutil.NoError(t, err)
	testutil.Contains(t, piCmd.Args[2], "--skill '"+skillsDir+"'")
	if containsAny(piCmd.Args[2], "--add-dir") {
		t.Fatal("Pi must receive its own skill flag")
	}

	openTask := &model.Task{Name: "open", Backend: "opencode", Prompt: "go", Worktree: t.TempDir()}
	openCmd, _, err := BuildCmd(openTask, cfg, false)
	testutil.NoError(t, err)
	var inline string
	for _, entry := range openCmd.Env {
		if strings.HasPrefix(entry, "OPENCODE_CONFIG_CONTENT=") {
			inline = strings.TrimPrefix(entry, "OPENCODE_CONFIG_CONTENT=")
		}
	}
	var config map[string]any
	testutil.NoError(t, json.Unmarshal([]byte(inline), &config))
	testutil.Equal(t, config["theme"], any("custom"))
	testutil.DeepEqual(t, config["skills"], any([]any{"/my/skill", skillsDir}))
}

func TestOpencodeSkillsConfigContent_InvalidExistingContent(t *testing.T) {
	t.Setenv("OPENCODE_CONFIG_CONTENT", "not json")
	if _, err := opencodeSkillsConfigContent("/skills"); err == nil {
		t.Fatal("invalid existing inline config must not be replaced")
	}
}

func TestReadClaudeMDFile_Present(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "CLAUDE.md")
	if err := os.WriteFile(path, []byte("# hello\n"), 0644); err != nil {
		t.Fatal(err)
	}
	got, err := readClaudeMDFile(path)
	testutil.NoError(t, err)
	testutil.Equal(t, got, "# hello\n")
}

func TestReadClaudeMDFile_Absent(t *testing.T) {
	got, err := readClaudeMDFile(filepath.Join(t.TempDir(), "CLAUDE.md"))
	testutil.NoError(t, err)
	testutil.Equal(t, got, "")
}

func TestReadClaudeMDFile_ExceedsSizeCapSkipped(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "CLAUDE.md")
	oversized := make([]byte, maxClaudeMDBytes+1)
	for i := range oversized {
		oversized[i] = 'x'
	}
	if err := os.WriteFile(path, oversized, 0644); err != nil {
		t.Fatal(err)
	}
	got, err := readClaudeMDFile(path)
	testutil.NoError(t, err)
	testutil.Equal(t, got, "")
}

func TestReadClaudeMDFile_AtCapSizeStillReadInFull(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "CLAUDE.md")
	atCap := make([]byte, maxClaudeMDBytes)
	for i := range atCap {
		atCap[i] = 'x'
	}
	if err := os.WriteFile(path, atCap, 0644); err != nil {
		t.Fatal(err)
	}
	got, err := readClaudeMDFile(path)
	testutil.NoError(t, err)
	testutil.Equal(t, len(got), maxClaudeMDBytes)
}

func TestReadRepoClaudeMD_Present(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("repo content"), 0644); err != nil {
		t.Fatal(err)
	}
	got, err := readRepoClaudeMD(dir)
	testutil.NoError(t, err)
	testutil.Equal(t, got, "repo content")
}

func TestReadRepoClaudeMD_Absent(t *testing.T) {
	got, err := readRepoClaudeMD(t.TempDir())
	testutil.NoError(t, err)
	testutil.Equal(t, got, "")
}

func TestReadGlobalClaudeMD_InertUnderTest(t *testing.T) {
	// readGlobalClaudeMD always short-circuits under `go test`, regardless of
	// HOME content — see the isTestBinary rationale on the function itself.
	got, err := readGlobalClaudeMD()
	testutil.NoError(t, err)
	testutil.Equal(t, got, "")
}

func TestReadGlobalClaudeMDReal_RespectsHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".claude", "CLAUDE.md"), []byte("global content"), 0644); err != nil {
		t.Fatal(err)
	}
	got, err := readGlobalClaudeMDReal()
	testutil.NoError(t, err)
	testutil.Equal(t, got, "global content")
}

func TestReadGlobalClaudeMDReal_AbsentIsClean(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	got, err := readGlobalClaudeMDReal()
	testutil.NoError(t, err)
	testutil.Equal(t, got, "")
}

func TestNonClaudeRoutingContentReal_ReturnsEmbeddedContent(t *testing.T) {
	got, err := nonClaudeRoutingContentReal()
	testutil.NoError(t, err)
	if got == "" {
		t.Fatal("expected non-empty embedded routing content")
	}
}

func TestNonClaudeContextPrefix_NeitherBackendReturnsEmpty(t *testing.T) {
	got := nonClaudeContextPrefix(false, false, t.TempDir())
	testutil.Equal(t, got, "")
}

func TestNonClaudeContextPrefix_CodexIncludesClaudeMDAndRouting(t *testing.T) {
	restoreGlobal := SetReadGlobalClaudeMDForTest(func() (string, error) { return "GLOBAL", nil })
	defer restoreGlobal()
	restoreRouting := SetNonClaudeRoutingContentForTest(func() (string, error) { return "ROUTING", nil })
	defer restoreRouting()

	worktree := t.TempDir()
	if err := os.WriteFile(filepath.Join(worktree, "CLAUDE.md"), []byte("REPO"), 0644); err != nil {
		t.Fatal(err)
	}

	got := nonClaudeContextPrefix(true, false, worktree)
	testutil.Contains(t, got, "GLOBAL")
	testutil.Contains(t, got, "REPO")
	testutil.Contains(t, got, "ROUTING")
}

func TestNonClaudeContextPrefix_OpencodeExcludesClaudeMD(t *testing.T) {
	restoreGlobal := SetReadGlobalClaudeMDForTest(func() (string, error) { return "GLOBAL", nil })
	defer restoreGlobal()
	restoreRouting := SetNonClaudeRoutingContentForTest(func() (string, error) { return "ROUTING", nil })
	defer restoreRouting()

	worktree := t.TempDir()
	if err := os.WriteFile(filepath.Join(worktree, "CLAUDE.md"), []byte("REPO"), 0644); err != nil {
		t.Fatal(err)
	}

	got := nonClaudeContextPrefix(false, true, worktree)
	testutil.Contains(t, got, "ROUTING")
	if containsAny(got, "GLOBAL", "REPO") {
		t.Errorf("expected opencode prefix to exclude CLAUDE.md content, got %q", got)
	}
}

func TestNonClaudeContextPrefix_MissingClaudeMDOmittedCleanlyForCodex(t *testing.T) {
	restoreGlobal := SetReadGlobalClaudeMDForTest(func() (string, error) { return "", nil })
	defer restoreGlobal()
	restoreRouting := SetNonClaudeRoutingContentForTest(func() (string, error) { return "ROUTING", nil })
	defer restoreRouting()

	got := nonClaudeContextPrefix(true, false, t.TempDir())
	testutil.Contains(t, got, "ROUTING")
	if containsAny(got, "Global CLAUDE.md", "Repository CLAUDE.md") {
		t.Errorf("expected no CLAUDE.md section headers when sources are absent, got %q", got)
	}
}

func TestNonClaudeContextPrefix_AllSourcesEmptyReturnsEmpty(t *testing.T) {
	restoreGlobal := SetReadGlobalClaudeMDForTest(func() (string, error) { return "", nil })
	defer restoreGlobal()
	restoreRouting := SetNonClaudeRoutingContentForTest(func() (string, error) { return "", nil })
	defer restoreRouting()

	got := nonClaudeContextPrefix(true, false, t.TempDir())
	testutil.Equal(t, got, "")
}

func TestNonClaudeContextPrefix_SourceErrorsSkippedNotFatal(t *testing.T) {
	restoreGlobal := SetReadGlobalClaudeMDForTest(func() (string, error) { return "", errors.New("boom") })
	defer restoreGlobal()
	restoreRouting := SetNonClaudeRoutingContentForTest(func() (string, error) { return "ROUTING", nil })
	defer restoreRouting()

	got := nonClaudeContextPrefix(true, false, t.TempDir())
	testutil.Contains(t, got, "ROUTING")
}

// TestNonClaudeContextPrefix_ExactStructureForCodex pins the exact block
// structure (section headers, ordering, separators) for a Codex backend,
// rather than only checking substring presence — the prior tests could pass
// even if section order, headers, or separators regressed.
func TestNonClaudeContextPrefix_ExactStructureForCodex(t *testing.T) {
	restoreGlobal := SetReadGlobalClaudeMDForTest(func() (string, error) { return "GLOBAL", nil })
	defer restoreGlobal()
	restoreRouting := SetNonClaudeRoutingContentForTest(func() (string, error) { return "ROUTING", nil })
	defer restoreRouting()

	worktree := t.TempDir()
	if err := os.WriteFile(filepath.Join(worktree, "CLAUDE.md"), []byte("REPO"), 0644); err != nil {
		t.Fatal(err)
	}

	got := nonClaudeContextPrefix(true, false, worktree)
	want := "# Global CLAUDE.md (~/.claude/CLAUDE.md)\n\nGLOBAL" +
		"\n\n---\n\n" +
		"# Repository CLAUDE.md\n\nREPO" +
		"\n\n---\n\n" +
		"ROUTING" +
		"\n\n---\n\n"
	testutil.Equal(t, got, want)
}

// TestNonClaudeContextPrefix_ExactStructureForOpencode is the opencode
// sibling of the Codex exact-structure test above: routing content only, no
// CLAUDE.md sections or their separators.
func TestNonClaudeContextPrefix_ExactStructureForOpencode(t *testing.T) {
	restoreGlobal := SetReadGlobalClaudeMDForTest(func() (string, error) { return "GLOBAL", nil })
	defer restoreGlobal()
	restoreRouting := SetNonClaudeRoutingContentForTest(func() (string, error) { return "ROUTING", nil })
	defer restoreRouting()

	worktree := t.TempDir()
	if err := os.WriteFile(filepath.Join(worktree, "CLAUDE.md"), []byte("REPO"), 0644); err != nil {
		t.Fatal(err)
	}

	got := nonClaudeContextPrefix(false, true, worktree)
	want := "ROUTING\n\n---\n\n"
	testutil.Equal(t, got, want)
}

// --- isCodex-gating seam: BuildCmd must call ensureCodexSkillsFn for codex
// only, never for claude/opencode/pi (which have no such equivalent). ---

func TestBuildCmd_EnsureCodexSkills_CalledForCodex(t *testing.T) {
	called := false
	isolatedHome := filepath.Join(t.TempDir(), "codex-home")
	restore := SetEnsureCodexSkillsForTest(func() (string, error) {
		called = true
		return isolatedHome, nil
	})
	defer restore()

	cfg := nonClaudeContextConfig()
	task := &model.Task{Name: "t", Backend: "codex", Prompt: "go", Worktree: t.TempDir()}
	cmd, _, err := BuildCmd(task, cfg, false)
	testutil.NoError(t, err)
	if !called {
		t.Error("expected ensureCodexSkillsFn to be called for a codex backend")
	}
	for _, entry := range []string{"CODEX_HOME=" + isolatedHome, "CODEX_SQLITE_HOME=" + filepath.Join(os.Getenv("HOME"), ".codex")} {
		if !containsEnvEntry(cmd.Env, entry) {
			t.Errorf("expected child env to contain %q", entry)
		}
	}
}

func TestBuildCmd_EnsureCodexSkills_FailureDoesNotBlockLaunch(t *testing.T) {
	t.Setenv("CODEX_HOME", "")
	restore := SetEnsureCodexSkillsForTest(func() (string, error) {
		return "", errors.New("cannot create isolated home")
	})
	defer restore()

	cfg := nonClaudeContextConfig()
	task := &model.Task{Name: "t", Backend: "codex", Prompt: "go", Worktree: t.TempDir()}
	cmd, cleanup, err := BuildCmd(task, cfg, false)
	if cleanup != nil {
		defer cleanup()
	}
	testutil.NoError(t, err)
	if containsEnvEntry(cmd.Env, "CODEX_HOME="+filepath.Join(os.Getenv("HOME"), ".local", "share", "argus", "codex-home")) {
		t.Fatal("failed preparation must not override CODEX_HOME")
	}
}

func containsEnvEntry(env []string, want string) bool {
	for _, entry := range env {
		if entry == want {
			return true
		}
	}
	return false
}

func TestBuildCmd_EnsureCodexSkills_NotCalledForOtherBackends(t *testing.T) {
	cfg := nonClaudeContextConfig()

	for _, backend := range []string{"claude", "opencode", "pi"} {
		t.Run(backend, func(t *testing.T) {
			called := false
			restore := SetEnsureCodexSkillsForTest(func() (string, error) {
				called = true
				return "", nil
			})
			defer restore()

			task := &model.Task{Name: "t", Backend: backend, Prompt: "go", Worktree: t.TempDir()}
			_, _, err := BuildCmd(task, cfg, false)
			testutil.NoError(t, err)
			if called {
				t.Errorf("expected ensureCodexSkillsFn NOT to be called for backend %q", backend)
			}
		})
	}
}

// --- BuildCmd integration: end-to-end wiring per the agent-execution delta spec ---

func nonClaudeContextConfig() config.Config {
	return config.Config{
		Defaults: config.Defaults{Backend: "claude"},
		Backends: map[string]config.Backend{
			"claude":   {Command: "claude"},
			"codex":    {Command: "codex --dangerously-bypass-approvals-and-sandbox"},
			"pi":       {Command: "pi"},
			"opencode": {Command: "opencode", PromptFlag: "--prompt"},
		},
	}
}

func TestBuildCmd_NonClaudeContextPrefix_Codex(t *testing.T) {
	restoreGlobal := SetReadGlobalClaudeMDForTest(func() (string, error) { return "GLOBAL", nil })
	defer restoreGlobal()
	restoreRouting := SetNonClaudeRoutingContentForTest(func() (string, error) { return "ROUTING", nil })
	defer restoreRouting()

	cfg := nonClaudeContextConfig()
	worktree := t.TempDir()
	if err := os.WriteFile(filepath.Join(worktree, "CLAUDE.md"), []byte("REPO"), 0644); err != nil {
		t.Fatal(err)
	}
	task := &model.Task{Name: "t", Backend: "codex", Prompt: "go", Worktree: worktree}

	cmd, _, err := BuildCmd(task, cfg, false)
	testutil.NoError(t, err)
	testutil.Contains(t, cmd.Args[2], "GLOBAL")
	testutil.Contains(t, cmd.Args[2], "REPO")
	testutil.Contains(t, cmd.Args[2], "ROUTING")
	testutil.Contains(t, cmd.Args[2], "go") // original prompt still present
}

func TestBuildCmd_NonClaudeContextPrefix_OpencodeRoutingOnly(t *testing.T) {
	restoreGlobal := SetReadGlobalClaudeMDForTest(func() (string, error) { return "GLOBAL", nil })
	defer restoreGlobal()
	restoreRouting := SetNonClaudeRoutingContentForTest(func() (string, error) { return "ROUTING", nil })
	defer restoreRouting()

	cfg := nonClaudeContextConfig()
	worktree := t.TempDir()
	if err := os.WriteFile(filepath.Join(worktree, "CLAUDE.md"), []byte("REPO"), 0644); err != nil {
		t.Fatal(err)
	}
	task := &model.Task{Name: "t", Backend: "opencode", Prompt: "go", Worktree: worktree}

	cmd, _, err := BuildCmd(task, cfg, false)
	testutil.NoError(t, err)
	testutil.Contains(t, cmd.Args[2], "ROUTING")
	if containsAny(cmd.Args[2], "GLOBAL", "REPO") {
		t.Errorf("expected opencode command to exclude CLAUDE.md content, got %q", cmd.Args[2])
	}
}

func TestBuildCmd_NonClaudeContextPrefix_ClaudeUnaffected(t *testing.T) {
	restoreGlobal := SetReadGlobalClaudeMDForTest(func() (string, error) { return "GLOBAL", nil })
	defer restoreGlobal()
	restoreRouting := SetNonClaudeRoutingContentForTest(func() (string, error) { return "ROUTING", nil })
	defer restoreRouting()

	cfg := nonClaudeContextConfig()
	task := &model.Task{Name: "t", Backend: "claude", Prompt: "go", Worktree: t.TempDir()}

	cmd, _, err := BuildCmd(task, cfg, false)
	testutil.NoError(t, err)
	testutil.Equal(t, cmd.Args[2], "claude -- 'go'")
}

func TestBuildCmd_NonClaudeContextPrefix_PiUnaffected(t *testing.T) {
	restoreGlobal := SetReadGlobalClaudeMDForTest(func() (string, error) { return "GLOBAL", nil })
	defer restoreGlobal()
	restoreRouting := SetNonClaudeRoutingContentForTest(func() (string, error) { return "ROUTING", nil })
	defer restoreRouting()

	cfg := nonClaudeContextConfig()
	task := &model.Task{Name: "t", Backend: "pi", Prompt: "go", Worktree: t.TempDir()}

	cmd, _, err := BuildCmd(task, cfg, false)
	testutil.NoError(t, err)
	testutil.Equal(t, cmd.Args[2], "pi 'go'")
}

func TestBuildCmd_NonClaudeContextPrefix_MissingClaudeMDOmittedForCodex(t *testing.T) {
	restoreGlobal := SetReadGlobalClaudeMDForTest(func() (string, error) { return "", nil })
	defer restoreGlobal()
	restoreRouting := SetNonClaudeRoutingContentForTest(func() (string, error) { return "ROUTING", nil })
	defer restoreRouting()

	cfg := nonClaudeContextConfig()
	task := &model.Task{Name: "t", Backend: "codex", Prompt: "go", Worktree: t.TempDir()}

	cmd, _, err := BuildCmd(task, cfg, false)
	testutil.NoError(t, err)
	testutil.Contains(t, cmd.Args[2], "ROUTING")
	testutil.Contains(t, cmd.Args[2], "go")
}
