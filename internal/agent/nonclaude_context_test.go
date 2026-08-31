package agent

import (
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
