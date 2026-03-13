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
			"claude": {Command: "claude --dangerously-skip-permissions --worktree", PromptFlag: ""},
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
	if b.Command != "claude --dangerously-skip-permissions --worktree" {
		t.Errorf("expected claude --worktree command, got %q", b.Command)
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
	if b.Command != "claude --dangerously-skip-permissions --worktree" {
		t.Errorf("expected claude --worktree command, got %q", b.Command)
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
	if b.Command != "claude --dangerously-skip-permissions --worktree" {
		t.Errorf("expected claude --worktree command, got %q", b.Command)
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
	task := &model.Task{Prompt: "fix the bug"}

	cmd, err := BuildCmd(task, cfg)
	if err != nil {
		t.Fatal(err)
	}

	// sh -c '<command> <flag> <quoted prompt>'
	args := cmd.Args
	if args[0] != "sh" || args[1] != "-c" {
		t.Errorf("expected sh -c, got %v", args[:2])
	}
	expected := "claude --dangerously-skip-permissions --worktree 'fix the bug'"
	if args[2] != expected {
		t.Errorf("expected %q, got %q", expected, args[2])
	}
}

func TestBuildCmd_WithProject(t *testing.T) {
	cfg := testConfig()
	task := &model.Task{Project: "myapp", Prompt: "test"}

	cmd, err := BuildCmd(task, cfg)
	if err != nil {
		t.Fatal(err)
	}

	if cmd.Dir != "/home/user/myapp" {
		t.Errorf("expected dir /home/user/myapp, got %q", cmd.Dir)
	}
	// Should use codex backend from project
	if cmd.Args[2] != "codex --prompt 'test'" {
		t.Errorf("unexpected command: %q", cmd.Args[2])
	}
}

func TestBuildCmd_EmptyPromptFlag(t *testing.T) {
	cfg := testConfig()
	task := &model.Task{Backend: "bare", Prompt: "do stuff"}

	cmd, err := BuildCmd(task, cfg)
	if err != nil {
		t.Fatal(err)
	}

	// Empty PromptFlag means prompt is passed as positional arg
	if cmd.Args[2] != "my-agent 'do stuff'" {
		t.Errorf("expected command with positional prompt, got %q", cmd.Args[2])
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
