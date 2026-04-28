package agent

import (
	"context"
	"errors"
	"testing"
	"time"

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
