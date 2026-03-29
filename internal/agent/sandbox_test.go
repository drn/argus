package agent

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/model"
)

func TestGenerateSandboxConfig_BasicPaths(t *testing.T) {
	worktree := "/home/user/.argus/worktrees/myapp/fix-bug"

	path, params, cleanup, err := GenerateSandboxConfig(worktree, config.SandboxConfig{})
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

	// Profile must allow read access to SSH non-secret files for git remote ops
	for _, allowed := range []string{"/.ssh/known_hosts\")", "/.ssh/known_hosts2\")", "/.ssh/config\")"} {
		if !strings.Contains(profile, allowed) {
			t.Errorf("profile missing SSH allow for %q:\n%s", allowed, profile)
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

	// Profile must allow PTY device access for pseudo-terminal allocation
	if !strings.Contains(profile, "/dev/ptmx") {
		t.Errorf("profile missing allow file-write* for /dev/ptmx:\n%s", profile)
	}
	if !strings.Contains(profile, "/dev/ttys") {
		t.Errorf("profile missing allow file-write* for /dev/ttys*:\n%s", profile)
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
	sandboxCfg := config.SandboxConfig{
		DenyRead:   []string{"/secrets"},
		ExtraWrite: []string{"~/.npm", "/var/cache"},
	}
	worktree := "/tmp/wt"

	path, _, cleanup, err := GenerateSandboxConfig(worktree, sandboxCfg)
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
	path, _, cleanup, err := GenerateSandboxConfig("/tmp/wt", config.SandboxConfig{})
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

func TestResolveGitDir(t *testing.T) {
	t.Run("valid worktree", func(t *testing.T) {
		// Create a fake worktree with a .git file
		wtDir := t.TempDir()
		mainRepo := t.TempDir()
		gitDir := mainRepo + "/.git"
		wtGitDir := gitDir + "/worktrees/my-task"
		os.MkdirAll(wtGitDir, 0o755)

		os.WriteFile(wtDir+"/.git", []byte("gitdir: "+wtGitDir+"\n"), 0o644)

		result := resolveGitDir(wtDir)
		if result != gitDir {
			t.Errorf("expected %q, got %q", gitDir, result)
		}
	})

	t.Run("real git dir", func(t *testing.T) {
		// A real repo has a .git directory, not a file
		dir := t.TempDir()
		os.MkdirAll(dir+"/.git", 0o755)

		result := resolveGitDir(dir)
		if result != "" {
			t.Errorf("expected empty for real .git dir, got %q", result)
		}
	})

	t.Run("no git", func(t *testing.T) {
		dir := t.TempDir()
		result := resolveGitDir(dir)
		if result != "" {
			t.Errorf("expected empty for no .git, got %q", result)
		}
	})

	t.Run("relative gitdir", func(t *testing.T) {
		wtDir := t.TempDir()
		// Simulate a relative gitdir path
		os.MkdirAll(wtDir+"/../../main/.git/worktrees/task", 0o755)
		os.WriteFile(wtDir+"/.git", []byte("gitdir: ../../main/.git/worktrees/task\n"), 0o644)

		result := resolveGitDir(wtDir)
		// Should resolve to an absolute path ending in .git
		if result == "" {
			t.Fatal("expected non-empty result for relative gitdir")
		}
		if !strings.HasSuffix(result, "/.git") {
			t.Errorf("expected result ending in /.git, got %q", result)
		}
	})
}

func TestGenerateSandboxConfig_GitDir(t *testing.T) {
	// Create a fake worktree with a .git file pointing to a main repo
	wtDir := t.TempDir()
	mainRepo := t.TempDir()
	gitDir := mainRepo + "/.git"
	wtGitDir := gitDir + "/worktrees/my-task"
	os.MkdirAll(wtGitDir, 0o755)
	os.WriteFile(wtDir+"/.git", []byte("gitdir: "+wtGitDir+"\n"), 0o644)

	cfg := config.SandboxConfig{}
	path, _, cleanup, err := GenerateSandboxConfig(wtDir, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	profile := string(data)

	// Profile must contain a write rule for the main repo's .git dir
	if !strings.Contains(profile, gitDir) {
		t.Errorf("profile missing .git dir write rule for %q:\n%s", gitDir, profile)
	}
}

func TestGenerateSandboxConfig_ProfileValid(t *testing.T) {
	if !IsSandboxAvailable() {
		t.Skip("sandbox-exec not available")
	}

	// Create a fake worktree with a .git file so the gitDir rule is exercised
	wtDir := t.TempDir()
	mainRepo := t.TempDir()
	gitDir := mainRepo + "/.git"
	wtGitDir := gitDir + "/worktrees/my-task"
	os.MkdirAll(wtGitDir, 0o755)
	os.WriteFile(wtDir+"/.git", []byte("gitdir: "+wtGitDir+"\n"), 0o644)

	sandboxCfg := config.SandboxConfig{
		DenyRead:   []string{"/secrets"},
		ExtraWrite: []string{"/var/cache"},
	}

	profilePath, params, cleanup, err := GenerateSandboxConfig(wtDir, sandboxCfg)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	// Run sandbox-exec with the generated profile — if any SBPL operation
	// names are invalid, sandbox-exec exits with a parse error before
	// executing the command.
	args := []string{}
	for _, p := range params {
		args = append(args, "-D", p)
	}
	args = append(args, "-f", profilePath, "/usr/bin/true")

	cmd := exec.Command(sandboxExecPath, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("sandbox-exec rejected generated profile: %v\n%s", err, out)
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
