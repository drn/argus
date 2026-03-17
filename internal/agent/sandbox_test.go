package agent

import (
	"os"
	"strings"
	"testing"

	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/model"
)

func TestGenerateSandboxConfig_BasicPaths(t *testing.T) {
	cfg := config.Config{}
	worktree := "/home/user/.argus/worktrees/myapp/fix-bug"

	path, params, cleanup, err := GenerateSandboxConfig(worktree, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	if path == "" {
		t.Fatal("expected non-empty path")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	profile := string(data)

	// Profile must reference WORKTREE and HOME params
	if !strings.Contains(profile, `(param "WORKTREE")`) {
		t.Errorf("profile missing WORKTREE param reference:\n%s", profile)
	}
	if !strings.Contains(profile, `(param "HOME")`) {
		t.Errorf("profile missing HOME param reference:\n%s", profile)
	}

	// Profile must deny default credential dirs
	for _, expected := range []string{"/.ssh", "/.aws", "/.gnupg", "/.kube"} {
		if !strings.Contains(profile, expected) {
			t.Errorf("profile missing deny for %q:\n%s", expected, profile)
		}
	}

	// Profile must allow writes to ~/.claude.json and ~/.claude/ for Claude Code startup
	if !strings.Contains(profile, "/.claude.json") {
		t.Errorf("profile missing allow file-write* for ~/.claude.json:\n%s", profile)
	}
	if !strings.Contains(profile, `"/.claude"`) {
		t.Errorf("profile missing allow file-write* for ~/.claude/:\n%s", profile)
	}

	// Profile must allow writes to /var/folders for macOS temp/cache dirs
	if !strings.Contains(profile, "/var/folders") {
		t.Errorf("profile missing allow file-write* for /var/folders:\n%s", profile)
	}

	// Params must contain HOME and WORKTREE
	hasHome := false
	hasWorktree := false
	for _, p := range params {
		if strings.HasPrefix(p, "HOME=") {
			hasHome = true
		}
		if strings.HasPrefix(p, "WORKTREE=") {
			hasWorktree = true
		}
	}
	if !hasHome {
		t.Errorf("params missing HOME=..., got %v", params)
	}
	if !hasWorktree {
		t.Errorf("params missing WORKTREE=..., got %v", params)
	}
	found := false
	for _, p := range params {
		if p == "WORKTREE="+worktree {
			found = true
		}
	}
	if !found {
		t.Errorf("params WORKTREE mismatch, got %v", params)
	}
}

func TestGenerateSandboxConfig_CustomConfig(t *testing.T) {
	cfg := config.Config{
		Sandbox: config.SandboxConfig{
			DenyRead:   []string{"/secrets"},
			ExtraWrite: []string{"~/.npm", "/var/cache"},
		},
	}
	worktree := "/tmp/wt"

	path, _, cleanup, err := GenerateSandboxConfig(worktree, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	profile := string(data)

	// Custom deny read path should appear as deny line
	if !strings.Contains(profile, `(deny file-read*`) {
		t.Errorf("profile missing deny file-read* rule:\n%s", profile)
	}
	if !strings.Contains(profile, "/secrets") {
		t.Errorf("profile missing custom deny path /secrets:\n%s", profile)
	}

	// Custom extra write paths should appear as allow-write lines
	if !strings.Contains(profile, "/var/cache") {
		t.Errorf("profile missing extra write path /var/cache:\n%s", profile)
	}
	if !strings.Contains(profile, ".npm") {
		t.Errorf("profile missing extra write path ~/.npm:\n%s", profile)
	}
}

func TestGenerateSandboxConfig_Cleanup(t *testing.T) {
	cfg := config.Config{}
	path, _, cleanup, err := GenerateSandboxConfig("/tmp/wt", cfg)
	if err != nil {
		t.Fatal(err)
	}

	// File should exist before cleanup
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("settings file should exist: %v", err)
	}

	cleanup()

	// File should be gone after cleanup
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("settings file should be removed after cleanup")
	}
}

func TestWrapWithSandbox(t *testing.T) {
	params := []string{"HOME=/Users/testuser", "WORKTREE=/tmp/wt"}
	profilePath := "/tmp/argus-sandbox-12345.sb"
	cmdStr := "claude --dangerously-skip-permissions"

	result := WrapWithSandbox(cmdStr, profilePath, params)

	if !strings.HasPrefix(result, sandboxExecPath) {
		t.Errorf("expected prefix %q, got %q", sandboxExecPath, result)
	}
	if !strings.Contains(result, "-D 'HOME=/Users/testuser'") {
		t.Errorf("expected HOME param, got %q", result)
	}
	if !strings.Contains(result, "-D 'WORKTREE=/tmp/wt'") {
		t.Errorf("expected WORKTREE param, got %q", result)
	}
	if !strings.Contains(result, "-f '/tmp/argus-sandbox-12345.sb'") {
		t.Errorf("expected profile path, got %q", result)
	}
	if !strings.Contains(result, "sh -c") {
		t.Errorf("expected sh -c, got %q", result)
	}
	if !strings.Contains(result, cmdStr) {
		t.Errorf("expected original command in output, got %q", result)
	}
}

func TestBuildCmd_WithSandboxDisabled(t *testing.T) {
	cfg := testConfig()
	cfg.Sandbox.Enabled = false
	task := &model.Task{
		Name:     "test",
		Prompt:   "hello",
		Worktree: "/tmp/wt",
	}

	cmd, cleanup, err := BuildCmd(task, cfg, false)
	if err != nil {
		t.Fatal(err)
	}

	// No sandbox wrapping when disabled
	if strings.Contains(cmd.Args[2], "sandbox-exec") {
		t.Errorf("sandbox should not be in command when disabled: %q", cmd.Args[2])
	}
	if cleanup != nil {
		t.Error("cleanup should be nil when sandbox disabled")
	}
}
