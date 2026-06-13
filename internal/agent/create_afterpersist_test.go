package agent

import (
	"errors"
	"testing"

	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

// TestCreateAndStart_AfterPersistRunsBeforeStartWithLiveID verifies the hook
// fires with a persisted (non-empty ID) task and BEFORE the session starts.
func TestCreateAndStart_AfterPersistRunsBeforeStartWithLiveID(t *testing.T) {
	repo := initGitRepo(t)
	d := createTestDB(t, repo)
	fr := &fakeRunner{}

	var sawID string
	var startCallsAtHook int
	task, _, err := CreateAndStart(d, fr, CreateInput{
		Name:    "ap-basic",
		Project: "proj",
		AfterPersist: func(t *model.Task) (func(), error) {
			sawID = t.ID
			startCallsAtHook = fr.startCalls // must be 0 — start happens after
			return nil, nil
		},
	})
	testutil.NoError(t, err)
	if sawID == "" {
		t.Fatal("AfterPersist saw empty task ID — must run after db.Add")
	}
	testutil.Equal(t, sawID, task.ID)
	testutil.Equal(t, startCallsAtHook, 0)
	testutil.Equal(t, fr.startCalls, 1)

	RemoveWorktreeAndBranch(task.Worktree, task.Branch, repo)
}

// TestCreateAndStart_AfterPersistErrorUnwinds verifies a hook error removes the
// persisted row + worktree and never starts the session.
func TestCreateAndStart_AfterPersistErrorUnwinds(t *testing.T) {
	repo := initGitRepo(t)
	d := createTestDB(t, repo)
	fr := &fakeRunner{} // must never be called

	_, _, err := CreateAndStart(d, fr, CreateInput{
		Name:    "ap-fail",
		Project: "proj",
		AfterPersist: func(*model.Task) (func(), error) {
			return nil, errors.New("binding insert failed")
		},
	})
	if err == nil {
		t.Fatal("expected error from AfterPersist")
	}
	testutil.Equal(t, fr.startCalls, 0)
	tasks, _ := d.Tasks()
	testutil.Equal(t, len(tasks), 0)
	if dirExists(WorktreeDir("proj", "ap-fail")) {
		t.Error("worktree should have been removed after AfterPersist failure")
	}
}

// TestCreateAndStart_AfterPersistCleanupRunsOnStartFailure verifies the hook's
// returned compensating cleanup is invoked (and the row removed) when a LATER
// step — runner.Start — fails. This is the born-bound LIFO guarantee.
func TestCreateAndStart_AfterPersistCleanupRunsOnStartFailure(t *testing.T) {
	repo := initGitRepo(t)
	d := createTestDB(t, repo)
	fr := &fakeRunner{startErr: errors.New("boom")}

	cleanupCalled := false
	_, _, err := CreateAndStart(d, fr, CreateInput{
		Name:    "ap-startfail",
		Project: "proj",
		AfterPersist: func(*model.Task) (func(), error) {
			return func() { cleanupCalled = true }, nil
		},
	})
	if err == nil {
		t.Fatal("expected error from runner.Start")
	}
	if !cleanupCalled {
		t.Error("AfterPersist cleanup must run when a later step fails")
	}
	tasks, _ := d.Tasks()
	testutil.Equal(t, len(tasks), 0)
	if dirExists(WorktreeDir("proj", "ap-startfail")) {
		t.Error("worktree should have been removed after start failure")
	}
}

// TestCreateAndStart_AfterPersistCleanupNotRunOnSuccess verifies the cleanup is
// NOT invoked on the happy path.
func TestCreateAndStart_AfterPersistCleanupNotRunOnSuccess(t *testing.T) {
	repo := initGitRepo(t)
	d := createTestDB(t, repo)
	fr := &fakeRunner{}

	cleanupCalled := false
	task, _, err := CreateAndStart(d, fr, CreateInput{
		Name:    "ap-ok",
		Project: "proj",
		AfterPersist: func(*model.Task) (func(), error) {
			return func() { cleanupCalled = true }, nil
		},
	})
	testutil.NoError(t, err)
	if cleanupCalled {
		t.Error("cleanup must not run on the success path")
	}
	RemoveWorktreeAndBranch(task.Worktree, task.Branch, repo)
}
