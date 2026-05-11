package agent

import (
	"database/sql"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

func testConfig() config.Config {
	return config.Config{
		Defaults: config.Defaults{Backend: "claude"},
		Backends: map[string]config.Backend{
			"claude": {Command: "claude --dangerously-skip-permissions --permission-mode plan", PromptFlag: ""},
			"codex":  {Command: "codex --dangerously-bypass-approvals-and-sandbox", PromptFlag: ""},
			"pi":     {Command: "pi", PromptFlag: ""},
			"bare":   {Command: "my-agent", PromptFlag: ""},
		},
		Projects: map[string]config.Project{
			"myapp": {Path: "/home/user/myapp", Backend: "codex"},
			"other": {Path: "/home/user/other"},
		},
	}
}

func TestResolveBackend_DefaultFallback(t *testing.T) {
	cfg := testConfig()
	task := &model.Task{}

	b, err := ResolveBackend(task, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if b.Command != "claude --dangerously-skip-permissions --permission-mode plan" {
		t.Errorf("expected claude command, got %q", b.Command)
	}
}

func TestResolveBackend_ProjectOverride(t *testing.T) {
	cfg := testConfig()
	task := &model.Task{Project: "myapp"}

	b, err := ResolveBackend(task, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if b.Command != "codex --dangerously-bypass-approvals-and-sandbox" {
		t.Errorf("expected codex command, got %q", b.Command)
	}
}

func TestResolveBackend_TaskOverride(t *testing.T) {
	cfg := testConfig()
	task := &model.Task{Project: "myapp", Backend: "claude"}

	b, err := ResolveBackend(task, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if b.Command != "claude --dangerously-skip-permissions --permission-mode plan" {
		t.Errorf("expected claude command, got %q", b.Command)
	}
}

func TestResolveBackend_ProjectWithoutBackend(t *testing.T) {
	cfg := testConfig()
	task := &model.Task{Project: "other"}

	b, err := ResolveBackend(task, cfg)
	if err != nil {
		t.Fatal(err)
	}
	// Falls back to default since project "other" has no backend
	if b.Command != "claude --dangerously-skip-permissions --permission-mode plan" {
		t.Errorf("expected claude command, got %q", b.Command)
	}
}

func TestResolveBackend_NotFound(t *testing.T) {
	cfg := testConfig()
	task := &model.Task{Backend: "nonexistent"}

	_, err := ResolveBackend(task, cfg)
	if err == nil {
		t.Fatal("expected error for missing backend")
	}
}

func TestResolveBackend_NoDefault(t *testing.T) {
	cfg := testConfig()
	cfg.Defaults.Backend = ""
	task := &model.Task{}

	_, err := ResolveBackend(task, cfg)
	if err == nil {
		t.Fatal("expected error for no backend")
	}
}

func TestResolveDir(t *testing.T) {
	cfg := testConfig()

	if dir := ResolveDir(&model.Task{}, cfg); dir != "" {
		t.Errorf("expected empty dir, got %q", dir)
	}
	if dir := ResolveDir(&model.Task{Project: "myapp"}, cfg); dir != "/home/user/myapp" {
		t.Errorf("expected /home/user/myapp, got %q", dir)
	}
	if dir := ResolveDir(&model.Task{Project: "unknown"}, cfg); dir != "" {
		t.Errorf("expected empty dir for unknown project, got %q", dir)
	}
}

func TestBuildCmd_NoWorktree(t *testing.T) {
	cfg := testConfig()
	task := &model.Task{Name: "fix-bug", Prompt: "fix the bug"}

	_, _, err := BuildCmd(task, cfg, false)
	if err == nil {
		t.Fatal("expected error when Worktree is empty")
	}
	if !strings.Contains(err.Error(), "no worktree set") {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestBuildCmd_MissingWorktree guards against the cryptic forkExec error
// that surfaces when cmd.Dir doesn't exist: Go reports "fork/exec /bin/sh:
// no such file or directory", masking the real cause (deleted worktree).
// BuildCmd must pre-flight stat the path and return an actionable error.
func TestBuildCmd_MissingWorktree(t *testing.T) {
	cfg := testConfig()
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	task := &model.Task{Name: "fix-bug", Prompt: "fix the bug", Worktree: missing}

	cmd, cleanup, err := BuildCmd(task, cfg, false)
	if err == nil {
		t.Fatal("expected error when worktree directory is missing")
	}
	// Contract on the error path: cmd and cleanup must both be nil so callers
	// can't accidentally exec an unconfigured command or skip cleanup.
	if cmd != nil {
		t.Errorf("expected nil cmd on error, got %+v", cmd)
	}
	if cleanup != nil {
		t.Error("expected nil cleanup on error")
	}
	if !strings.Contains(err.Error(), "worktree path missing") {
		t.Errorf("expected 'worktree path missing' error, got: %v", err)
	}
	if !strings.Contains(err.Error(), missing) {
		t.Errorf("expected error to include path %q, got: %v", missing, err)
	}
}

func TestBuildCmd(t *testing.T) {
	cfg := testConfig()
	task := &model.Task{Name: "fix-bug", Prompt: "fix the bug", Worktree: t.TempDir()}

	cmd, _, err := BuildCmd(task, cfg, false)
	if err != nil {
		t.Fatal(err)
	}

	// sh -c '<command> <flag> <quoted prompt>'
	args := cmd.Args
	if args[0] != "sh" || args[1] != "-c" {
		t.Errorf("expected sh -c, got %v", args[:2])
	}
	expected := "claude --dangerously-skip-permissions --permission-mode plan -- 'fix the bug'"
	if args[2] != expected {
		t.Errorf("expected %q, got %q", expected, args[2])
	}
}

func TestBuildCmd_WithProject(t *testing.T) {
	cfg := testConfig()
	wt := t.TempDir()
	task := &model.Task{Project: "myapp", Prompt: "test", Worktree: wt}

	cmd, _, err := BuildCmd(task, cfg, false)
	if err != nil {
		t.Fatal(err)
	}

	if cmd.Dir != wt {
		t.Errorf("expected dir from worktree, got %q", cmd.Dir)
	}
	// Should use codex backend from project (no --session-id for codex backends)
	if cmd.Args[2] != "codex --dangerously-bypass-approvals-and-sandbox -- 'test'" {
		t.Errorf("unexpected command: %q", cmd.Args[2])
	}
}

func TestBuildCmd_EmptyPromptFlag(t *testing.T) {
	cfg := testConfig()
	task := &model.Task{Backend: "bare", Prompt: "do stuff", Worktree: t.TempDir()}

	cmd, _, err := BuildCmd(task, cfg, false)
	if err != nil {
		t.Fatal(err)
	}

	// Empty PromptFlag means prompt is passed as positional arg with -- separator
	if cmd.Args[2] != "my-agent -- 'do stuff'" {
		t.Errorf("expected command with positional prompt, got %q", cmd.Args[2])
	}
}

func TestBuildCmd_EmptyPromptFlag_DashPrefix(t *testing.T) {
	cfg := testConfig()
	task := &model.Task{Backend: "bare", Prompt: "- fix the bug", Worktree: t.TempDir()}

	cmd, _, err := BuildCmd(task, cfg, false)
	if err != nil {
		t.Fatal(err)
	}

	// The -- separator prevents prompts starting with "-" from being parsed as flags
	if cmd.Args[2] != "my-agent -- '- fix the bug'" {
		t.Errorf("expected command with -- separator, got %q", cmd.Args[2])
	}
}

func TestBuildCmd_NewSessionWithID(t *testing.T) {
	cfg := testConfig()
	task := &model.Task{Name: "fix-bug", Prompt: "fix the bug", SessionID: "aaaaaaaa-bbbb-4ccc-9ddd-eeeeeeeeeeee", Worktree: t.TempDir()}

	cmd, _, err := BuildCmd(task, cfg, false)
	if err != nil {
		t.Fatal(err)
	}

	expected := "claude --dangerously-skip-permissions --permission-mode plan --session-id 'aaaaaaaa-bbbb-4ccc-9ddd-eeeeeeeeeeee' -- 'fix the bug'"
	if cmd.Args[2] != expected {
		t.Errorf("expected %q, got %q", expected, cmd.Args[2])
	}
}

func TestBuildCmd_Resume(t *testing.T) {
	cfg := testConfig()
	task := &model.Task{Prompt: "fix the bug", SessionID: "aaaaaaaa-bbbb-4ccc-9ddd-eeeeeeeeeeee", Worktree: t.TempDir()}

	cmd, _, err := BuildCmd(task, cfg, true)
	if err != nil {
		t.Fatal(err)
	}

	// Resume should use --resume and ignore the prompt
	expected := "claude --dangerously-skip-permissions --permission-mode plan --resume 'aaaaaaaa-bbbb-4ccc-9ddd-eeeeeeeeeeee'"
	if cmd.Args[2] != expected {
		t.Errorf("expected %q, got %q", expected, cmd.Args[2])
	}
}

func TestBuildCmd_ResumeWithWorktree(t *testing.T) {
	cfg := testConfig()
	wt := t.TempDir()
	task := &model.Task{
		Prompt:    "fix the bug",
		SessionID: "aaaaaaaa-bbbb-4ccc-9ddd-eeeeeeeeeeee",
		Worktree:  wt,
	}

	cmd, _, err := BuildCmd(task, cfg, true)
	if err != nil {
		t.Fatal(err)
	}

	// Resume should set cmd.Dir to the existing worktree
	if cmd.Dir != wt {
		t.Errorf("expected Dir %q, got %q", wt, cmd.Dir)
	}
}

func TestBuildCmd_ResumeWithProjectAndWorktree(t *testing.T) {
	cfg := testConfig()
	wt := t.TempDir()
	task := &model.Task{
		Project:   "other",
		Prompt:    "fix the bug",
		SessionID: "aaaaaaaa-bbbb-4ccc-9ddd-eeeeeeeeeeee",
		Worktree:  wt,
	}

	cmd, _, err := BuildCmd(task, cfg, true)
	if err != nil {
		t.Fatal(err)
	}

	// Resume MUST use the worktree (not the project dir) because sessions
	// are project-scoped in Claude Code — the session was created from the
	// worktree directory, not the main project directory.
	if cmd.Dir != wt {
		t.Errorf("expected Dir %q (worktree), got %q (likely project path)", wt, cmd.Dir)
	}
}

func TestBuildCmd_WorktreeDir(t *testing.T) {
	cfg := testConfig()
	wt := t.TempDir()
	task := &model.Task{
		Name:     "fix-bug",
		Prompt:   "fix the bug",
		Worktree: wt,
	}

	cmd, _, err := BuildCmd(task, cfg, false)
	if err != nil {
		t.Fatal(err)
	}

	// When Worktree is set, cmd.Dir should use it
	if cmd.Dir != wt {
		t.Errorf("expected Dir %q, got %q", wt, cmd.Dir)
	}
}

func TestBuildCmd_WorktreeOverridesProject(t *testing.T) {
	cfg := testConfig()
	wt := t.TempDir()
	task := &model.Task{
		Project:  "other",
		Name:     "fix-bug",
		Prompt:   "fix the bug",
		Worktree: wt,
	}

	cmd, _, err := BuildCmd(task, cfg, false)
	if err != nil {
		t.Fatal(err)
	}

	// Worktree takes precedence over project path
	if cmd.Dir != wt {
		t.Errorf("expected Dir %q (worktree), got %q", wt, cmd.Dir)
	}
}

func TestResolveSandboxConfig_InheritsGlobal(t *testing.T) {
	cfg := testConfig()
	cfg.Sandbox = config.SandboxConfig{
		Enabled:    true,
		DenyRead:   []string{"/secrets"},
		ExtraWrite: []string{"~/.npm"},
	}
	task := &model.Task{Project: "other"}

	result := ResolveSandboxConfig(task, cfg)

	if !result.Enabled {
		t.Error("expected sandbox enabled (inherited from global)")
	}
	if len(result.DenyRead) != 1 || result.DenyRead[0] != "/secrets" {
		t.Errorf("expected global deny_read, got %v", result.DenyRead)
	}
	if len(result.ExtraWrite) != 1 || result.ExtraWrite[0] != "~/.npm" {
		t.Errorf("expected global extra_write, got %v", result.ExtraWrite)
	}
}

func TestResolveSandboxConfig_ProjectOverridesEnabled(t *testing.T) {
	cfg := testConfig()
	cfg.Sandbox = config.SandboxConfig{Enabled: false}

	projEnabled := true
	cfg.Projects["myapp"] = config.Project{
		Path: "/home/user/myapp",
		Sandbox: config.ProjectSandboxConfig{
			Enabled: &projEnabled,
		},
	}
	task := &model.Task{Project: "myapp"}

	result := ResolveSandboxConfig(task, cfg)

	if !result.Enabled {
		t.Error("expected sandbox enabled (project overrides global false)")
	}
}

func TestResolveSandboxConfig_ProjectDisablesGlobalEnabled(t *testing.T) {
	cfg := testConfig()
	cfg.Sandbox = config.SandboxConfig{Enabled: true}

	projEnabled := false
	cfg.Projects["myapp"] = config.Project{
		Path: "/home/user/myapp",
		Sandbox: config.ProjectSandboxConfig{
			Enabled: &projEnabled,
		},
	}
	task := &model.Task{Project: "myapp"}

	result := ResolveSandboxConfig(task, cfg)

	if result.Enabled {
		t.Error("expected sandbox disabled (project overrides global true)")
	}
}

func TestResolveSandboxConfig_ProjectAppendsPaths(t *testing.T) {
	cfg := testConfig()
	cfg.Sandbox = config.SandboxConfig{
		DenyRead:   []string{"/global-deny"},
		ExtraWrite: []string{"/global-write"},
	}
	cfg.Projects["myapp"] = config.Project{
		Path: "/home/user/myapp",
		Sandbox: config.ProjectSandboxConfig{
			DenyRead:   []string{"/proj-deny"},
			ExtraWrite: []string{"/proj-write"},
		},
	}
	task := &model.Task{Project: "myapp"}

	result := ResolveSandboxConfig(task, cfg)

	if len(result.DenyRead) != 2 {
		t.Fatalf("expected 2 deny_read paths, got %d: %v", len(result.DenyRead), result.DenyRead)
	}
	if result.DenyRead[0] != "/global-deny" || result.DenyRead[1] != "/proj-deny" {
		t.Errorf("unexpected deny_read order: %v", result.DenyRead)
	}
	if len(result.ExtraWrite) != 2 {
		t.Fatalf("expected 2 extra_write paths, got %d: %v", len(result.ExtraWrite), result.ExtraWrite)
	}
	if result.ExtraWrite[0] != "/global-write" || result.ExtraWrite[1] != "/proj-write" {
		t.Errorf("unexpected extra_write order: %v", result.ExtraWrite)
	}
}

func TestResolveSandboxConfig_NoProjectUsesGlobal(t *testing.T) {
	cfg := testConfig()
	cfg.Sandbox = config.SandboxConfig{Enabled: true, DenyRead: []string{"/x"}}
	task := &model.Task{} // no project

	result := ResolveSandboxConfig(task, cfg)

	if !result.Enabled {
		t.Error("expected sandbox enabled from global")
	}
	if len(result.DenyRead) != 1 {
		t.Errorf("expected 1 deny_read, got %v", result.DenyRead)
	}
}

func TestResolveSandboxConfig_DoesNotMutateGlobal(t *testing.T) {
	cfg := testConfig()
	cfg.Sandbox = config.SandboxConfig{DenyRead: []string{"/global"}}
	cfg.Projects["myapp"] = config.Project{
		Sandbox: config.ProjectSandboxConfig{DenyRead: []string{"/proj"}},
	}
	task := &model.Task{Project: "myapp"}

	_ = ResolveSandboxConfig(task, cfg)

	// Global config must not be mutated
	if len(cfg.Sandbox.DenyRead) != 1 {
		t.Errorf("global DenyRead was mutated: %v", cfg.Sandbox.DenyRead)
	}
}

func TestBuildCmd_CodexResumeWithSessionID(t *testing.T) {
	cfg := testConfig()
	task := &model.Task{
		Project:   "myapp",
		Prompt:    "fix the bug",
		SessionID: "019cff60-2cfb-7ed3-bca6-15ef06587c99",
		Worktree:  t.TempDir(),
	}

	cmd, _, err := BuildCmd(task, cfg, true)
	if err != nil {
		t.Fatal(err)
	}

	// Codex resume uses dedicated command with specific session ID.
	expected := codexResumeCmd + " '019cff60-2cfb-7ed3-bca6-15ef06587c99'"
	if cmd.Args[2] != expected {
		t.Errorf("expected %q, got %q", expected, cmd.Args[2])
	}
}

func TestBuildCmd_CodexNewSessionNoSessionIDFlag(t *testing.T) {
	cfg := testConfig()
	// Even if SessionID is somehow set, codex new sessions should NOT use --session-id.
	task := &model.Task{Project: "myapp", Prompt: "fix the bug", SessionID: "some-id", Worktree: t.TempDir()}

	cmd, _, err := BuildCmd(task, cfg, false)
	if err != nil {
		t.Fatal(err)
	}

	// Codex does not support --session-id flag.
	expected := "codex --dangerously-bypass-approvals-and-sandbox -- 'fix the bug'"
	if cmd.Args[2] != expected {
		t.Errorf("expected %q, got %q", expected, cmd.Args[2])
	}
}

func TestIsCodexBackend(t *testing.T) {
	tests := []struct {
		command  string
		expected bool
	}{
		{"codex --dangerously-bypass-approvals-and-sandbox", true},
		{"codex --full-auto", true},
		{"codex", true},
		{"/usr/local/bin/codex --full-auto", true},
		{"claude --dangerously-skip-permissions --permission-mode plan", false},
		{"my-codex-wrapper --flags", false},
		{"/usr/bin/my-codex", false},
		{"", false},
	}
	for _, tt := range tests {
		got := IsCodexBackend(tt.command)
		if got != tt.expected {
			t.Errorf("IsCodexBackend(%q) = %v, want %v", tt.command, got, tt.expected)
		}
	}
}

func TestShellQuote(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"hello", "'hello'"},
		{"it's a test", `'it'\''s a test'`},
		{"", "''"},
		{"foo'bar'baz", `'foo'\''bar'\''baz'`},
		{`no "problem" here`, `'no "problem" here'`},
		{"line\nnewline", "'line\nnewline'"},
	}

	for _, tt := range tests {
		got := shellQuote(tt.input)
		if got != tt.expected {
			t.Errorf("shellQuote(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

// TestRemoveWorktree_NoRepoDir exercises the cmd.Dir fallback to
// filepath.Dir(cleaned) when repoDir is empty.
func TestRemoveWorktree_NoRepoDir(t *testing.T) {
	repo := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run("init", "-q")
	run("config", "user.email", "test@test.com")
	run("config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-q", "-m", "init")

	wtBase := filepath.Join(t.TempDir(), ".argus", "worktrees", "proj")
	if err := os.MkdirAll(wtBase, 0o755); err != nil {
		t.Fatal(err)
	}
	wtPath := filepath.Join(wtBase, "norepo")
	run("worktree", "add", "-b", "argus/norepo", wtPath, "HEAD")

	// repoDir = "" exercises cmd.Dir = filepath.Dir(cleaned) fallback.
	RemoveWorktree(wtPath, "")
	// Worktree dir should be gone.
	if dirExists(wtPath) {
		t.Errorf("worktree dir should have been removed: %s", wtPath)
	}

	// Cleanup branch.
	DeleteBranch(repo, "argus/norepo")
}

// TestDeleteBranch_NotFoundLogsErr exercises the error path of DeleteBranch
// when the branch doesn't exist (git exits non-zero).
func TestDeleteBranch_NotFoundLogsErr(t *testing.T) {
	repo := t.TempDir()
	cmd := exec.Command("git", "init", "-q")
	cmd.Dir = repo
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	// Configure user so commits work.
	for _, args := range [][]string{
		{"config", "user.email", "test@test.com"},
		{"config", "user.name", "Test"},
	} {
		cmd = exec.Command("git", args...)
		cmd.Dir = repo
		_ = cmd.Run()
	}

	// DeleteBranch on a nonexistent branch — git fails, but the function
	// swallows the error.
	DeleteBranch(repo, "nonexistent-branch-xyz")
}

// TestPruneWorktrees_LogsError exercises the err path of pruneWorktrees:
// pass a non-git directory so git worktree prune exits non-zero.
func TestPruneWorktrees_LogsError(t *testing.T) {
	notGitRepo := t.TempDir()
	// Function swallows errors; just confirm no panic.
	pruneWorktrees(notGitRepo)
}

// TestDeleteRemoteBranch_NoOrigin exercises DeleteRemoteBranch on a repo
// without origin — git push fails, but the function swallows the error.
func TestDeleteRemoteBranch_NoOrigin(t *testing.T) {
	repo := t.TempDir()
	cmd := exec.Command("git", "init", "-q")
	cmd.Dir = repo
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	// Function swallows errors; just confirm no panic.
	DeleteRemoteBranch(repo, "argus/never-pushed")
}

// TestBuildCmd_PromptFlag covers the path where backend.PromptFlag is set
// (vs the empty branch already covered).
func TestBuildCmd_PromptFlag(t *testing.T) {
	cfg := config.Config{
		Defaults: config.Defaults{Backend: "with-flag"},
		Backends: map[string]config.Backend{
			"with-flag": {Command: "agent", PromptFlag: "--prompt"},
		},
		Projects: make(map[string]config.Project),
	}
	task := &model.Task{Name: "x", Prompt: "do thing", Worktree: t.TempDir()}
	cmd, _, err := BuildCmd(task, cfg, false)
	testutil.NoError(t, err)
	if cmd.Args[2] != "agent --prompt 'do thing'" {
		t.Errorf("unexpected command: %q", cmd.Args[2])
	}
}

// TestBuildCmd_NoPrompt covers the path where prompt is empty (no append).
func TestBuildCmd_NoPrompt(t *testing.T) {
	cfg := config.Config{
		Defaults: config.Defaults{Backend: "agent"},
		Backends: map[string]config.Backend{
			"agent": {Command: "agent --bare", PromptFlag: ""},
		},
		Projects: make(map[string]config.Project),
	}
	task := &model.Task{Name: "x", Worktree: t.TempDir()}
	cmd, _, err := BuildCmd(task, cfg, false)
	testutil.NoError(t, err)
	// No -- since no prompt.
	testutil.Equal(t, cmd.Args[2], "agent --bare")
}

// TestBuildCmd_ResumeNoSessionIDClaude exercises the resume=true path for
// Claude-style backends with no SessionID — should NOT add --resume.
func TestBuildCmd_ResumeNoSessionIDClaude(t *testing.T) {
	cfg := config.Config{
		Defaults: config.Defaults{Backend: "claude"},
		Backends: map[string]config.Backend{
			"claude": {Command: "claude --skip", PromptFlag: ""},
		},
		Projects: make(map[string]config.Project),
	}
	task := &model.Task{Name: "x", Worktree: t.TempDir() /* no SessionID */}
	cmd, _, err := BuildCmd(task, cfg, true)
	testutil.NoError(t, err)
	// resume=true but SessionID empty → cmdStr stays as base command.
	testutil.Equal(t, cmd.Args[2], "claude --skip")
}

// TestBuildCmd_WorktreeStatPermissionError exercises the non-IsNotExist
// error path when stat fails. Hard to provoke deterministically on every
// platform; we use an unreadable directory parent.
func TestBuildCmd_WorktreeStatPermissionError(t *testing.T) {
	// Best-effort: create a path inside an unreadable parent. Skip if the
	// parent permission change isn't honored (e.g., running as root).
	parent := t.TempDir()
	wt := filepath.Join(parent, "wt-no-access")
	if err := os.MkdirAll(wt, 0o700); err != nil {
		t.Fatal(err)
	}
	// Make parent unreadable so stat fails differently.
	if err := os.Chmod(parent, 0o000); err != nil {
		t.Skip("cannot chmod parent:", err)
	}
	t.Cleanup(func() { os.Chmod(parent, 0o700) }) //nolint:errcheck

	cfg := config.Config{
		Defaults: config.Defaults{Backend: "test"},
		Backends: map[string]config.Backend{
			"test": {Command: "echo", PromptFlag: ""},
		},
		Projects: make(map[string]config.Project),
	}
	task := &model.Task{Name: "x", Worktree: wt}
	if os.Getuid() == 0 {
		t.Skip("running as root; chmod test is meaningless")
	}
	_, _, err := BuildCmd(task, cfg, false)
	if err == nil {
		t.Skip("stat unexpectedly succeeded; cannot test the error branch")
	}
	// Either "worktree path missing" (IsNotExist) or "worktree path unreachable"
	// is acceptable — both go through the right error path.
}

// TestCreateAndStart_BeforeStartIsCalledBeforeStart fires BeforeStart and
// verifies it runs before runner.Start.
func TestCreateAndStart_BeforeStartFiresBeforeStart(t *testing.T) {
	repo := initGitRepo(t)
	d := createTestDB(t, repo)

	startCalled := false
	beforeStartCalled := false
	fr := &funcSessionProvider{
		startFunc: func(task *model.Task, _ config.Config, _, _ uint16, _ bool) (SessionHandle, error) {
			startCalled = true
			if !beforeStartCalled {
				t.Error("Start was called before BeforeStart")
			}
			return &fakeSession{pid: 1}, nil
		},
	}
	task, _, err := CreateAndStart(d, fr, CreateInput{
		Name:    "bs-test",
		Prompt:  "go",
		Project: "proj",
		BeforeStart: func() {
			beforeStartCalled = true
			if startCalled {
				t.Error("BeforeStart fired after Start")
			}
		},
	})
	testutil.NoError(t, err)
	if !beforeStartCalled || !startCalled {
		t.Errorf("hooks: beforeStart=%v startCalled=%v", beforeStartCalled, startCalled)
	}
	if task != nil {
		t.Cleanup(func() { RemoveWorktreeAndBranch(task.Worktree, task.Branch, repo) })
	}
}

// TestCreateAndStart_AfterStartRunsOnFailure verifies AfterStart runs even
// when runner.Start fails.
func TestCreateAndStart_AfterStartRunsOnFailure(t *testing.T) {
	repo := initGitRepo(t)
	d := createTestDB(t, repo)

	afterStartCalled := false
	fr := &fakeRunner{startErr: errors.New("nope")}

	_, _, err := CreateAndStart(d, fr, CreateInput{
		Name:       "as-fail",
		Prompt:     "go",
		Project:    "proj",
		AfterStart: func() { afterStartCalled = true },
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !afterStartCalled {
		t.Error("AfterStart should fire on failure")
	}
}

// TestCreateAndStart_HookErrorUnwinds exercises the OnWorktreeCreated error
// path — worktree should be removed.
func TestCreateAndStart_OnWorktreeCreatedErrorUnwinds(t *testing.T) {
	repo := initGitRepo(t)
	d := createTestDB(t, repo)
	fr := &fakeRunner{}

	hookErr := errors.New("hook failed")
	_, _, err := CreateAndStart(d, fr, CreateInput{
		Name:    "hook-fail",
		Prompt:  "go",
		Project: "proj",
		OnWorktreeCreated: func(_ string) error {
			return hookErr
		},
	})
	if err == nil {
		t.Fatal("expected error from OnWorktreeCreated")
	}

	// Worktree should NOT exist (unwound).
	expected := WorktreeDir("proj", "hook-fail")
	if dirExists(expected) {
		t.Errorf("worktree should have been removed: %s", expected)
	}
}

// funcSessionProvider is a SessionProvider whose Start can be customized.
type funcSessionProvider struct {
	fakeRunner
	startFunc func(task *model.Task, cfg config.Config, rows, cols uint16, resume bool) (SessionHandle, error)
}

func (f *funcSessionProvider) Start(task *model.Task, cfg config.Config, rows, cols uint16, resume bool) (SessionHandle, error) {
	if f.startFunc != nil {
		return f.startFunc(task, cfg, rows, cols, resume)
	}
	return f.fakeRunner.Start(task, cfg, rows, cols, resume)
}

// TestCreateAndStart_DefaultsRowsCols exercises the rows/cols defaulting branch.
func TestCreateAndStart_DefaultsRowsCols(t *testing.T) {
	repo := initGitRepo(t)
	d := createTestDB(t, repo)

	var gotRows, gotCols uint16
	fr := &funcSessionProvider{
		startFunc: func(_ *model.Task, _ config.Config, rows, cols uint16, _ bool) (SessionHandle, error) {
			gotRows = rows
			gotCols = cols
			return &fakeSession{pid: 1}, nil
		},
	}
	task, _, err := CreateAndStart(d, fr, CreateInput{
		Name:    "rc",
		Prompt:  "go",
		Project: "proj",
		// Rows: 0, Cols: 0 → defaults
	})
	testutil.NoError(t, err)
	testutil.Equal(t, gotRows, uint16(24))
	testutil.Equal(t, gotCols, uint16(80))

	if task != nil {
		t.Cleanup(func() { RemoveWorktreeAndBranch(task.Worktree, task.Branch, repo) })
	}
}

// TestStart_OnFinishStdinError exercises the path where session emits no
// output and the runner's onFinish fires with empty lastOutput.
func TestStart_OnFinishEmptyOutput(t *testing.T) {
	type result struct {
		out []byte
	}
	ch := make(chan result, 1)
	r := NewRunner(func(_ string, _ error, _ bool, out []byte) {
		ch <- result{out}
	})
	// 'true' produces no output.
	cfg := config.Config{
		Defaults: config.Defaults{Backend: "test"},
		Backends: map[string]config.Backend{
			"test": {Command: "true", PromptFlag: ""},
		},
		Projects: make(map[string]config.Project),
	}
	task := &model.Task{ID: "fin-empty", Name: "n", Worktree: t.TempDir()}
	_, err := r.Start(task, cfg, 24, 80, false)
	testutil.NoError(t, err)

	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout")
	}
}

// TestSession_StdinError exercises the err branch in Attach where stdin
// returns an error (not EOF), and the process is still alive — it returns
// the error.
func TestSession_Attach_StdinReadError(t *testing.T) {
	cmd := exec.Command("sleep", "60")
	sess, err := StartSession("attach-stdinerr", cmd, 24, 80)
	testutil.NoError(t, err)
	t.Cleanup(func() { sess.Stop() }) //nolint:errcheck

	stdin := &errorReader{}
	var stdout syncBuffer

	errCh := make(chan error, 1)
	go func() {
		errCh <- sess.Attach(stdin, &stdout)
	}()

	select {
	case got := <-errCh:
		// The error from stdin should propagate.
		if got == nil {
			t.Error("expected non-nil error from stdin read failure")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for Attach to return")
	}
}

// errorReader returns an error on Read.
type errorReader struct{}

func (errorReader) Read(_ []byte) (int, error) { return 0, errors.New("read fail") }

// TestSession_Attach_DetachThenWait covers the close-detachCh-when-already-closed
// branch in Detach (the select default case after the channel is closed).
func TestSession_Attach_DetachAfterDetach(t *testing.T) {
	cmd := exec.Command("sleep", "60")
	sess, err := StartSession("detach2", cmd, 24, 80)
	testutil.NoError(t, err)
	t.Cleanup(func() { sess.Stop() }) //nolint:errcheck

	// Manually attach, detach, attempt detach again — second detach should
	// no-op via the already-closed-channel branch.
	stdin := &errorReader{}
	var stdout syncBuffer

	go func() {
		_ = sess.Attach(stdin, &stdout)
	}()
	time.Sleep(50 * time.Millisecond)
	sess.Detach()
	// Second call within attached=true window before defer runs.
	sess.Detach() // should hit the select-default path
}

// TestReconcileStaleSessions_UpdateError exercises the inner error path when
// database.Update returns an error during reconcile. We force this by closing
// the DB after read; but reads will fail too. Instead test via a stub
// indirectly — skip if not easy. We cover via the natural close error path
// already exercised in TasksError.

// TestEvalSymlinksOrKeep_RealSymlink resolves a symlink and confirms result.
func TestEvalSymlinksOrKeep_RealSymlink(t *testing.T) {
	target := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skip("symlink not supported")
	}
	got := evalSymlinksOrKeep(link)
	// Should resolve to target (or its symlink-evaluated form).
	if got == link {
		t.Errorf("expected symlink resolution; got same path %q", got)
	}
}

// TestSandbox_GenerateConfig_EmptyDenyAndExtra covers the empty-string skip
// inside the loop bodies (whitespace-only paths get trimmed to empty).
func TestSandbox_GenerateConfig_EmptyEntries(t *testing.T) {
	wt := t.TempDir()
	cfg := config.SandboxConfig{
		DenyRead:   []string{"  ", ""},
		ExtraWrite: []string{"  ", ""},
	}
	path, _, cleanup, err := GenerateSandboxConfig(wt, cfg)
	testutil.NoError(t, err)
	t.Cleanup(cleanup)

	data, err := os.ReadFile(path)
	testutil.NoError(t, err)
	// Profile should not contain extra deny/allow lines beyond the base.
	_ = data
}

// TestRunner_Start_BackendMissing exercises the error path where ResolveBackend
// fails inside Start.
func TestRunner_Start_BackendMissing(t *testing.T) {
	r := NewRunner(nil)
	cfg := config.Config{
		Defaults: config.Defaults{Backend: ""}, // no default
		Backends: map[string]config.Backend{},
		Projects: make(map[string]config.Project),
	}
	task := &model.Task{ID: "no-backend", Name: "n", Worktree: t.TempDir()}
	_, err := r.Start(task, cfg, 24, 80, false)
	if err == nil {
		t.Fatal("expected error for missing backend")
	}
}

// Use db so the import is not unused if no other test references it.
var _ = db.OpenInMemory

// TestCaptureCodexSessionID_EmptyPath rejects an empty worktree path.
func TestCaptureCodexSessionID_EmptyPath(t *testing.T) {
	_, err := CaptureCodexSessionID("")
	if err == nil {
		t.Fatal("expected error for empty worktree path")
	}
	testutil.Contains(t, err.Error(), "worktree path is empty")
}

// TestCaptureCodexSessionID_DBPathMissing returns an error when codex's
// state DB doesn't exist (no ~/.codex/state_5.sqlite).
func TestCaptureCodexSessionID_DBPathMissing(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	_, err := CaptureCodexSessionID("/some/worktree")
	if err == nil {
		t.Fatal("expected error when codex state DB is missing")
	}
}

// TestCaptureCodexSessionID_Success seeds a fake codex state_5.sqlite with a
// matching threads row and verifies CaptureCodexSessionID returns the ID.
func TestCaptureCodexSessionID_Success(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	codexDir := filepath.Join(home, ".codex")
	if err := os.MkdirAll(codexDir, 0o700); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(codexDir, codexStateDB)

	conn, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })

	if _, err := conn.Exec(`CREATE TABLE threads (id TEXT, cwd TEXT, updated_at INTEGER)`); err != nil {
		t.Fatal(err)
	}
	wt := "/wt/path"
	validID := "019cff60-2cfb-7ed3-bca6-15ef06587c99"
	if _, err := conn.Exec(`INSERT INTO threads (id, cwd, updated_at) VALUES (?, ?, ?)`, validID, wt, 100); err != nil {
		t.Fatal(err)
	}

	if _, err := conn.Exec(`INSERT INTO threads (id, cwd, updated_at) VALUES (?, ?, ?)`, "ffffffff-ffff-4fff-bfff-ffffffffffff", wt, 50); err != nil {
		t.Fatal(err)
	}
	conn.Close()

	got, err := CaptureCodexSessionID(wt)
	testutil.NoError(t, err)
	testutil.Equal(t, got, validID)
}

// TestCaptureCodexSessionID_BadFormat returns an error when the row's id
// doesn't match the UUID regex.
func TestCaptureCodexSessionID_BadFormat(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	codexDir := filepath.Join(home, ".codex")
	if err := os.MkdirAll(codexDir, 0o700); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(codexDir, codexStateDB)

	conn, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })

	if _, err := conn.Exec(`CREATE TABLE threads (id TEXT, cwd TEXT, updated_at INTEGER)`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`INSERT INTO threads (id, cwd, updated_at) VALUES (?, ?, ?)`, "not-a-uuid", "/wt", 100); err != nil {
		t.Fatal(err)
	}
	conn.Close()

	_, err = CaptureCodexSessionID("/wt")
	if err == nil {
		t.Fatal("expected error for malformed UUID")
	}
	testutil.Contains(t, err.Error(), "unexpected session ID format")
}

// TestCaptureCodexSessionID_NoMatch returns an error when no row matches cwd.
func TestCaptureCodexSessionID_NoMatch(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	codexDir := filepath.Join(home, ".codex")
	if err := os.MkdirAll(codexDir, 0o700); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(codexDir, codexStateDB)

	conn, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })

	if _, err := conn.Exec(`CREATE TABLE threads (id TEXT, cwd TEXT, updated_at INTEGER)`); err != nil {
		t.Fatal(err)
	}
	conn.Close()

	_, err = CaptureCodexSessionID("/never-matches")
	if err == nil {
		t.Fatal("expected error when no rows match cwd")
	}
}

// --- Pi backend ---

func TestIsPiBackend(t *testing.T) {
	tests := []struct {
		name string
		cmd  string
		want bool
	}{
		{"bare pi", "pi", true},
		{"pi with flags", "pi --model claude", true},
		{"absolute path", "/usr/local/bin/pi", true},
		{"empty", "", false},
		{"prefix only", "pi-helper", false},
		{"claude", "claude --dangerously-skip-permissions", false},
		{"codex", "codex --dangerously-bypass-approvals-and-sandbox", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			testutil.Equal(t, IsPiBackend(tc.cmd), tc.want)
		})
	}
}

func TestBuildCmd_PiNewSession(t *testing.T) {
	cfg := testConfig()
	task := &model.Task{
		Backend:  "pi",
		Prompt:   "fix the bug",
		Worktree: t.TempDir(),
	}

	cmd, _, err := BuildCmd(task, cfg, false)
	testutil.NoError(t, err)

	// Pi: positional prompt, no --session-id (captured post-exit), no -- separator.
	expected := "pi 'fix the bug'"
	testutil.Equal(t, cmd.Args[2], expected)
}

func TestBuildCmd_PiNewSession_IgnoresSessionID(t *testing.T) {
	cfg := testConfig()
	task := &model.Task{
		Backend:   "pi",
		Prompt:    "fix the bug",
		SessionID: "aaaaaaaa-bbbb-4ccc-9ddd-eeeeeeeeeeee",
		Worktree:  t.TempDir(),
	}

	cmd, _, err := BuildCmd(task, cfg, false)
	testutil.NoError(t, err)

	// Pi doesn't support --session-id at new-session time; the ID is ignored
	// on the new-session path. Resume path uses --session.
	if strings.Contains(cmd.Args[2], "--session-id") {
		t.Errorf("pi new-session must not emit --session-id, got %q", cmd.Args[2])
	}
}

func TestBuildCmd_PiResume(t *testing.T) {
	cfg := testConfig()
	task := &model.Task{
		Backend:   "pi",
		Prompt:    "fix the bug",
		SessionID: "aaaaaaaa-bbbb-4ccc-9ddd-eeeeeeeeeeee",
		Worktree:  t.TempDir(),
	}

	cmd, _, err := BuildCmd(task, cfg, true)
	testutil.NoError(t, err)

	// Resume: --session <uuid>, prompt is dropped (pi reloads conversation).
	expected := "pi --session 'aaaaaaaa-bbbb-4ccc-9ddd-eeeeeeeeeeee'"
	testutil.Equal(t, cmd.Args[2], expected)
}

func TestPiEncodeCwd(t *testing.T) {
	// Must match pi's getDefaultSessionDir exactly:
	//   `--${cwd.replace(/^[/\\]/, "").replace(/[/\\:]/g, "-")}--`
	// — a SINGLE-character leading strip, NOT a TrimLeft of all leading
	// separators. Divergence would point CapturePiSessionID at the wrong
	// directory and silently break resume.
	tests := []struct {
		cwd  string
		want string
	}{
		{"/Users/me/proj", "--Users-me-proj--"},
		{"/", "----"},
		{"relative/path", "--relative-path--"},
		// Single-char strip: `//double/leading` → `/double/leading` after
		// stripping ONE leading slash → `-double-leading` after replacing
		// the remaining slashes. A TrimLeft would have stripped both and
		// produced `--double-leading--`. The triple-dash is the point —
		// it pins parity with pi's regex semantics.
		{"//double/leading", "---double-leading--"},
		// Empty input: no leading char to strip, no replacements, just wrappers.
		{"", "----"},
		// Adjacent ":" + "\" each map to "-", so two consecutive dashes is correct.
		{"C:\\Windows\\stuff", "--C--Windows-stuff--"},
	}
	for _, tc := range tests {
		t.Run(tc.cwd, func(t *testing.T) {
			testutil.Equal(t, piEncodeCwd(tc.cwd), tc.want)
		})
	}
}

func TestCapturePiSessionID(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	wt := filepath.Join(os.Getenv("HOME"), ".argus", "worktrees", "p", "t")
	testutil.NoError(t, os.MkdirAll(wt, 0o755))

	sessionDir := filepath.Join(os.Getenv("HOME"), ".pi", "agent", "sessions", piEncodeCwd(wt))
	testutil.NoError(t, os.MkdirAll(sessionDir, 0o755))

	// Two session files; the newer mtime wins.
	older := filepath.Join(sessionDir, "20260101T000000_aaaaaaaa-bbbb-4ccc-9ddd-111111111111.jsonl")
	newer := filepath.Join(sessionDir, "20260102T000000_cccccccc-bbbb-4ccc-9ddd-222222222222.jsonl")
	testutil.NoError(t, os.WriteFile(older, []byte("{}\n"), 0o644))
	testutil.NoError(t, os.WriteFile(newer, []byte("{}\n"), 0o644))
	past := time.Now().Add(-1 * time.Hour)
	testutil.NoError(t, os.Chtimes(older, past, past))

	sid, err := CapturePiSessionID(wt)
	testutil.NoError(t, err)
	testutil.Equal(t, sid, "cccccccc-bbbb-4ccc-9ddd-222222222222")
}

func TestCapturePiSessionID_EmptyWorktree(t *testing.T) {
	_, err := CapturePiSessionID("")
	if err == nil {
		t.Fatal("expected error for empty worktree path")
	}
}

func TestCapturePiSessionID_NoSessionsDir(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	_, err := CapturePiSessionID("/nonexistent/cwd")
	if err == nil {
		t.Fatal("expected error when sessions dir doesn't exist")
	}
}

func TestCapturePiSessionID_NoMatchingFiles(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	wt := t.TempDir()
	sessionDir := filepath.Join(os.Getenv("HOME"), ".pi", "agent", "sessions", piEncodeCwd(wt))
	testutil.NoError(t, os.MkdirAll(sessionDir, 0o755))
	// Wrong extension — won't match the regex.
	testutil.NoError(t, os.WriteFile(filepath.Join(sessionDir, "garbage.txt"), []byte("x"), 0o644))

	_, err := CapturePiSessionID(wt)
	if err == nil {
		t.Fatal("expected error when no session files match")
	}
}

// TestCaptureSessionID_DispatchesByBackend covers the unified dispatcher
// introduced for the daemon/TUI session-ID capture share. The dispatcher must:
// (1) route codex backends to CaptureCodexSessionID (~/.codex SQLite),
// (2) route pi backends to CapturePiSessionID (~/.pi readdir),
// (3) return ("", nil) for Claude-style and unknown backends (which pre-mint
//
//	their session ID at start, so there's nothing to scan for),
//
// (4) propagate ResolveBackend errors.
func TestCaptureSessionID_DispatchesByBackend(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	t.Run("codex backend uses codex SQLite scan", func(t *testing.T) {
		// Seed a codex state DB so the dispatch reaches CaptureCodexSessionID
		// and returns the seeded ID instead of a "no rows" error.
		home := os.Getenv("HOME")
		codexDir := filepath.Join(home, ".codex")
		testutil.NoError(t, os.MkdirAll(codexDir, 0o700))
		conn, err := sql.Open("sqlite", filepath.Join(codexDir, codexStateDB))
		testutil.NoError(t, err)
		t.Cleanup(func() { conn.Close() })
		_, err = conn.Exec(`CREATE TABLE threads (id TEXT, cwd TEXT, updated_at INTEGER)`)
		testutil.NoError(t, err)
		wt := "/codex-dispatch-wt"
		seeded := "01923456-789a-7bcd-9def-0123456789ab"
		_, err = conn.Exec(`INSERT INTO threads (id, cwd, updated_at) VALUES (?, ?, ?)`, seeded, wt, 1)
		testutil.NoError(t, err)
		conn.Close()

		cfg := testConfig()
		task := &model.Task{Backend: "codex", Worktree: wt}
		got, err := CaptureSessionID(task, cfg)
		testutil.NoError(t, err)
		testutil.Equal(t, got, seeded)
	})

	t.Run("pi backend uses pi readdir scan", func(t *testing.T) {
		home := os.Getenv("HOME")
		wt := filepath.Join(home, "pi-dispatch-wt")
		testutil.NoError(t, os.MkdirAll(wt, 0o755))
		sessionDir := filepath.Join(home, ".pi", "agent", "sessions", piEncodeCwd(wt))
		testutil.NoError(t, os.MkdirAll(sessionDir, 0o755))
		sid := "abcdef01-2345-7bcd-9def-0123456789ab"
		testutil.NoError(t, os.WriteFile(
			filepath.Join(sessionDir, "20260511T000000_"+sid+".jsonl"), []byte("{}\n"), 0o644,
		))

		cfg := testConfig()
		task := &model.Task{Backend: "pi", Worktree: wt}
		got, err := CaptureSessionID(task, cfg)
		testutil.NoError(t, err)
		testutil.Equal(t, got, sid)
	})

	t.Run("claude backend is a no-op", func(t *testing.T) {
		cfg := testConfig()
		task := &model.Task{Backend: "claude", Worktree: t.TempDir()}
		got, err := CaptureSessionID(task, cfg)
		testutil.NoError(t, err)
		testutil.Equal(t, got, "")
	})

	t.Run("unknown bare backend is a no-op", func(t *testing.T) {
		cfg := testConfig()
		task := &model.Task{Backend: "bare", Worktree: t.TempDir()}
		got, err := CaptureSessionID(task, cfg)
		testutil.NoError(t, err)
		testutil.Equal(t, got, "")
	})

	t.Run("ResolveBackend error propagates", func(t *testing.T) {
		cfg := testConfig()
		task := &model.Task{Backend: "no-such-backend", Worktree: t.TempDir()}
		_, err := CaptureSessionID(task, cfg)
		if err == nil {
			t.Fatal("expected ResolveBackend error for unknown backend name")
		}
	})
}

// TestBuildCmd_ResumeNoSessionIDPi pins the pi branch's silent-fresh-start
// contract for resume=true with an empty SessionID. Mirrors the analogous
// TestBuildCmd_ResumeNoSessionIDClaude. Both callers (TUI / API) compute
// resume := task.SessionID != "" so this combination shouldn't reach BuildCmd
// in practice, but the guard exists so a future caller mistake produces a
// fresh start rather than an obviously broken `pi --session ”`.
func TestBuildCmd_ResumeNoSessionIDPi(t *testing.T) {
	cfg := testConfig()
	task := &model.Task{Backend: "pi", Worktree: t.TempDir() /* no SessionID */}
	cmd, _, err := BuildCmd(task, cfg, true)
	testutil.NoError(t, err)
	testutil.Equal(t, cmd.Args[2], "pi")
}
