package agent

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/llm"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

// stubAutoRename swaps autoRenameFn for the duration of t and restores it
// at cleanup time.
func stubAutoRename(t *testing.T, fn func(ctx context.Context, prompt string) (string, error)) {
	t.Helper()
	prev := autoRenameFn
	autoRenameFn = fn
	t.Cleanup(func() { autoRenameFn = prev })
}

// addTask seeds a task and returns its ID. Bypasses CreateAndStart so we
// can test runAutoRename in isolation.
func addTask(t *testing.T, d *db.DB, name string) string {
	t.Helper()
	task := &model.Task{Name: name, Status: model.StatusInProgress, Project: "proj"}
	if err := d.Add(task); err != nil {
		t.Fatalf("db.Add: %v", err)
	}
	return task.ID
}

func TestRunAutoRename_Renames(t *testing.T) {
	d, err := db.OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { d.Close() }) //nolint:errcheck

	id := addTask(t, d, "fix-the-thing")

	stubAutoRename(t, func(_ context.Context, prompt string) (string, error) {
		return "auth-token-refresh", nil
	})

	runAutoRename(d, id, "fix-the-thing", "Refactor the auth token refresh flow")

	got, err := d.Get(id)
	testutil.NoError(t, err)
	testutil.Equal(t, got.Name, "auth-token-refresh")
}

func TestRunAutoRename_NoOp_SameName(t *testing.T) {
	d, err := db.OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { d.Close() }) //nolint:errcheck

	id := addTask(t, d, "auth-token-refresh")

	stubAutoRename(t, func(_ context.Context, _ string) (string, error) {
		return "auth-token-refresh", nil
	})

	runAutoRename(d, id, "auth-token-refresh", "anything")

	got, err := d.Get(id)
	testutil.NoError(t, err)
	testutil.Equal(t, got.Name, "auth-token-refresh")
}

func TestRunAutoRename_FailOpen_OnError(t *testing.T) {
	d, err := db.OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { d.Close() }) //nolint:errcheck

	id := addTask(t, d, "fix-the-thing")

	stubAutoRename(t, func(_ context.Context, _ string) (string, error) {
		return "", errors.New("haiku exploded")
	})

	runAutoRename(d, id, "fix-the-thing", "anything")

	got, err := d.Get(id)
	testutil.NoError(t, err)
	testutil.Equal(t, got.Name, "fix-the-thing")
}

func TestRunAutoRename_SkipsWhenUnavailable(t *testing.T) {
	d, err := db.OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { d.Close() }) //nolint:errcheck

	id := addTask(t, d, "fix-the-thing")

	stubAutoRename(t, func(_ context.Context, _ string) (string, error) {
		return "", llm.ErrUnavailable
	})

	runAutoRename(d, id, "fix-the-thing", "anything")

	got, err := d.Get(id)
	testutil.NoError(t, err)
	testutil.Equal(t, got.Name, "fix-the-thing")
}

func TestRunAutoRename_RaceGuard_UserRenamed(t *testing.T) {
	d, err := db.OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { d.Close() }) //nolint:errcheck

	id := addTask(t, d, "fix-the-thing")

	// User renames before Haiku returns.
	if err := d.Rename(id, "user-typed-name"); err != nil {
		t.Fatalf("rename: %v", err)
	}

	stubAutoRename(t, func(_ context.Context, _ string) (string, error) {
		return "auto-generated", nil
	})

	runAutoRename(d, id, "fix-the-thing", "anything")

	got, err := d.Get(id)
	testutil.NoError(t, err)
	testutil.Equal(t, got.Name, "user-typed-name") // user's rename preserved
}

func TestRunAutoRename_TaskDeleted(t *testing.T) {
	d, err := db.OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { d.Close() }) //nolint:errcheck

	id := addTask(t, d, "fix-the-thing")
	if err := d.Delete(id); err != nil {
		t.Fatalf("delete: %v", err)
	}

	stubAutoRename(t, func(_ context.Context, _ string) (string, error) {
		return "auto-generated", nil
	})

	// Should not panic; should not write anything.
	runAutoRename(d, id, "fix-the-thing", "anything")
}

func TestRunAutoRename_RespectsTimeout(t *testing.T) {
	d, err := db.OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { d.Close() }) //nolint:errcheck

	id := addTask(t, d, "fix-the-thing")

	stubAutoRename(t, func(ctx context.Context, _ string) (string, error) {
		// Verify caller passed a context with a deadline.
		if _, ok := ctx.Deadline(); !ok {
			t.Error("autoRenameFn called without deadline")
		}
		return "swift-name", nil
	})

	start := time.Now()
	runAutoRename(d, id, "fix-the-thing", "anything")
	if elapsed := time.Since(start); elapsed > llm.DefaultTimeout {
		t.Errorf("runAutoRename took %v, expected ≤ %v", elapsed, llm.DefaultTimeout)
	}
}

// TestCleanGitOutput_MultiSpaceCollapse exercises the multi-space-collapse
// branch in cleanGitOutput (line 225-227).
func TestCleanGitOutput_MultiSpaceCollapse(t *testing.T) {
	// No "fatal:" lines + leading/trailing/multiple spaces → take the
	// fall-back path and exercise the multi-space collapse loop.
	got := cleanGitOutput([]byte("foo  bar   baz\n  many   spaces  "))
	// All double+ spaces collapsed to single spaces.
	if strings.Contains(got, "  ") {
		t.Errorf("expected double spaces collapsed, got %q", got)
	}
	testutil.Contains(t, got, "foo bar baz")
	testutil.Contains(t, got, "many spaces")
}

// TestRunAutoRename_EmptyPrompt covers the ErrEmptyPrompt branch.
func TestRunAutoRename_EmptyPrompt(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	d, err := db.OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { d.Close() }) //nolint:errcheck

	task := &model.Task{Name: "tname", Project: "p", Status: model.StatusPending}
	if err := d.Add(task); err != nil {
		t.Fatal(err)
	}

	prev := autoRenameFn
	autoRenameFn = func(_ context.Context, _ string) (string, error) {
		return "", llm.ErrEmptyPrompt
	}
	t.Cleanup(func() { autoRenameFn = prev })

	runAutoRename(d, task.ID, "tname", "")
	// Verify name was NOT changed — the function returned early.
	got, err := d.Get(task.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, got.Name, "tname")
}

// TestBuildCmd_SandboxEnabledNoSandboxBin: when sandbox enabled but
// sandbox-exec not available, command falls through unsandboxed.
func TestBuildCmd_SandboxEnabledNoSandboxBin(t *testing.T) {
	cfg := config.Config{
		Defaults: config.Defaults{Backend: "test"},
		Backends: map[string]config.Backend{
			"test": {Command: "agent", PromptFlag: ""},
		},
		Projects: make(map[string]config.Project),
		Sandbox:  config.SandboxConfig{Enabled: true},
	}
	task := &model.Task{Name: "x", Prompt: "hi", Worktree: t.TempDir()}
	cmd, _, err := BuildCmd(task, cfg, false)
	testutil.NoError(t, err)
	// On a system where sandbox-exec is available (macOS), the cmd will
	// include sandbox-exec wrapping. On other systems, no wrapping. Just
	// confirm BuildCmd succeeds either way.
	_ = cmd
}

// TestBuildCmd_SandboxEnabledExpectWrapping verifies that when sandbox is
// enabled and sandbox-exec is available, cleanup is non-nil and the wrapped
// command is used. Skipped if sandbox is not available on this platform.
func TestBuildCmd_SandboxEnabledExpectWrapping(t *testing.T) {
	if !IsSandboxAvailable() {
		t.Skip("sandbox-exec not available on this platform")
	}
	cfg := config.Config{
		Defaults: config.Defaults{Backend: "test"},
		Backends: map[string]config.Backend{
			"test": {Command: "agent", PromptFlag: ""},
		},
		Projects: make(map[string]config.Project),
		Sandbox:  config.SandboxConfig{Enabled: true},
	}
	task := &model.Task{Name: "x", Prompt: "hi", Worktree: t.TempDir()}
	cmd, cleanup, err := BuildCmd(task, cfg, false)
	testutil.NoError(t, err)
	if cleanup == nil {
		t.Error("expected non-nil sandbox cleanup")
	}
	t.Cleanup(cleanup)
	// Command should include sandbox-exec wrapping.
	if !strings.Contains(cmd.Args[2], "sandbox-exec") {
		t.Errorf("expected sandbox-exec in command, got %q", cmd.Args[2])
	}
}

// TestSession_Detach_NotAttachedYet is intentionally omitted: the
// "if s.attached" false branch in Detach is exercised by
// TestStartSession_Detach_NotAttached in session_test.go.

// Use errors so import is referenced if no other test does.
var _ = errors.Is
