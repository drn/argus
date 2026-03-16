package agent

import (
	"testing"

	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/model"
)

func testConfig() config.Config {
	return config.Config{
		Defaults: config.Defaults{Backend: "claude"},
		Backends: map[string]config.Backend{
			"claude": {Command: "claude --dangerously-skip-permissions", PromptFlag: ""},
			"codex":  {Command: "codex", PromptFlag: "--prompt"},
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
	if b.Command != "codex" {
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

func TestBuildCmd(t *testing.T) {
	cfg := testConfig()
	task := &model.Task{Name: "fix-bug", Prompt: "fix the bug"}

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
	// Should use codex backend from project
	if cmd.Args[2] != "codex --prompt 'test'" {
		t.Errorf("unexpected command: %q", cmd.Args[2])
	}
}

func TestBuildCmd_EmptyPromptFlag(t *testing.T) {
	cfg := testConfig()
	task := &model.Task{Backend: "bare", Prompt: "do stuff"}

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
	task := &model.Task{Name: "fix-bug", Prompt: "fix the bug", SessionID: "aaaaaaaa-bbbb-4ccc-9ddd-eeeeeeeeeeee"}

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
	task := &model.Task{Prompt: "fix the bug", SessionID: "aaaaaaaa-bbbb-4ccc-9ddd-eeeeeeeeeeee"}

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
