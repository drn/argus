package agent

import (
	"strings"
	"testing"

	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/model"
)

func testConfig() config.Config {
	return config.Config{
		Defaults: config.Defaults{Backend: "claude"},
		Backends: map[string]config.Backend{
			"claude": {Command: "claude --dangerously-skip-permissions", PromptFlag: ""},
			"codex":  {Command: "codex --dangerously-bypass-approvals-and-sandbox", PromptFlag: ""},
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
	if b.Command != "claude --dangerously-skip-permissions" {
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
	if b.Command != "claude --dangerously-skip-permissions" {
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
	if b.Command != "claude --dangerously-skip-permissions" {
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
	expected := "claude --dangerously-skip-permissions 'fix the bug'"
	if args[2] != expected {
		t.Errorf("expected %q, got %q", expected, args[2])
	}
}

func TestBuildCmd_WithProject(t *testing.T) {
	cfg := testConfig()
	task := &model.Task{Project: "myapp", Prompt: "test", Worktree: "/home/user/.argus/worktrees/myapp/fix-bug"}

	cmd, _, err := BuildCmd(task, cfg, false)
	if err != nil {
		t.Fatal(err)
	}

	if cmd.Dir != "/home/user/.argus/worktrees/myapp/fix-bug" {
		t.Errorf("expected dir from worktree, got %q", cmd.Dir)
	}
	// Should use codex backend from project (no --session-id for codex backends)
	if cmd.Args[2] != "codex --dangerously-bypass-approvals-and-sandbox 'test'" {
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

	// Empty PromptFlag means prompt is passed as positional arg
	if cmd.Args[2] != "my-agent 'do stuff'" {
		t.Errorf("expected command with positional prompt, got %q", cmd.Args[2])
	}
}

func TestBuildCmd_NewSessionWithID(t *testing.T) {
	cfg := testConfig()
	task := &model.Task{Name: "fix-bug", Prompt: "fix the bug", SessionID: "aaaaaaaa-bbbb-4ccc-9ddd-eeeeeeeeeeee", Worktree: t.TempDir()}

	cmd, _, err := BuildCmd(task, cfg, false)
	if err != nil {
		t.Fatal(err)
	}

	expected := "claude --dangerously-skip-permissions --session-id 'aaaaaaaa-bbbb-4ccc-9ddd-eeeeeeeeeeee' 'fix the bug'"
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
	expected := "claude --dangerously-skip-permissions --resume 'aaaaaaaa-bbbb-4ccc-9ddd-eeeeeeeeeeee'"
	if cmd.Args[2] != expected {
		t.Errorf("expected %q, got %q", expected, cmd.Args[2])
	}
}

func TestBuildCmd_ResumeWithWorktree(t *testing.T) {
	cfg := testConfig()
	task := &model.Task{
		Prompt:    "fix the bug",
		SessionID: "aaaaaaaa-bbbb-4ccc-9ddd-eeeeeeeeeeee",
		Worktree:  "/tmp/worktree-test",
	}

	cmd, _, err := BuildCmd(task, cfg, true)
	if err != nil {
		t.Fatal(err)
	}

	// Resume should set cmd.Dir to the existing worktree
	if cmd.Dir != "/tmp/worktree-test" {
		t.Errorf("expected Dir %q, got %q", "/tmp/worktree-test", cmd.Dir)
	}
}

func TestBuildCmd_ResumeWithProjectAndWorktree(t *testing.T) {
	cfg := testConfig()
	task := &model.Task{
		Project:   "other",
		Prompt:    "fix the bug",
		SessionID: "aaaaaaaa-bbbb-4ccc-9ddd-eeeeeeeeeeee",
		Worktree:  "/tmp/worktree-test",
	}

	cmd, _, err := BuildCmd(task, cfg, true)
	if err != nil {
		t.Fatal(err)
	}

	// Resume MUST use the worktree (not the project dir) because sessions
	// are project-scoped in Claude Code — the session was created from the
	// worktree directory, not the main project directory.
	if cmd.Dir != "/tmp/worktree-test" {
		t.Errorf("expected Dir %q (worktree), got %q (likely project path)", "/tmp/worktree-test", cmd.Dir)
	}
}

func TestBuildCmd_WorktreeDir(t *testing.T) {
	cfg := testConfig()
	task := &model.Task{
		Name:     "fix-bug",
		Prompt:   "fix the bug",
		Worktree: "/tmp/test-worktree",
	}

	cmd, _, err := BuildCmd(task, cfg, false)
	if err != nil {
		t.Fatal(err)
	}

	// When Worktree is set, cmd.Dir should use it
	if cmd.Dir != "/tmp/test-worktree" {
		t.Errorf("expected Dir %q, got %q", "/tmp/test-worktree", cmd.Dir)
	}
}

func TestBuildCmd_WorktreeOverridesProject(t *testing.T) {
	cfg := testConfig()
	task := &model.Task{
		Project:  "other",
		Name:     "fix-bug",
		Prompt:   "fix the bug",
		Worktree: "/tmp/test-worktree",
	}

	cmd, _, err := BuildCmd(task, cfg, false)
	if err != nil {
		t.Fatal(err)
	}

	// Worktree takes precedence over project path
	if cmd.Dir != "/tmp/test-worktree" {
		t.Errorf("expected Dir %q (worktree), got %q", "/tmp/test-worktree", cmd.Dir)
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
	expected := "codex --dangerously-bypass-approvals-and-sandbox 'fix the bug'"
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
		{"claude --dangerously-skip-permissions", false},
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
