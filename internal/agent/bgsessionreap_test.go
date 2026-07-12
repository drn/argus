package agent

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/drn/argus/internal/claudeagents"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

// stubBackgroundSessions swaps listBackgroundSessionsFn/stopBackgroundSessionFn
// for the duration of t and restores them at cleanup time.
func stubBackgroundSessions(t *testing.T,
	list func(ctx context.Context, cwd string) ([]claudeagents.Session, error),
	stop func(ctx context.Context, id string) error,
) {
	t.Helper()
	prevList, prevStop := listBackgroundSessionsFn, stopBackgroundSessionFn
	listBackgroundSessionsFn = list
	stopBackgroundSessionFn = stop
	t.Cleanup(func() {
		listBackgroundSessionsFn = prevList
		stopBackgroundSessionFn = prevStop
	})
}

func TestReapOrphanedClaudeSessions_EmptyWorktree(t *testing.T) {
	called := false
	stubBackgroundSessions(t,
		func(ctx context.Context, cwd string) ([]claudeagents.Session, error) {
			called = true
			return nil, nil
		},
		func(ctx context.Context, id string) error { return nil },
	)

	got := reapOrphanedClaudeSessions("t1", "")
	testutil.Nil(t, got)
	testutil.Equal(t, called, false)
}

func TestReapOrphanedClaudeSessions_NoSessions(t *testing.T) {
	stubBackgroundSessions(t,
		func(ctx context.Context, cwd string) ([]claudeagents.Session, error) {
			return nil, nil
		},
		func(ctx context.Context, id string) error {
			t.Fatal("Stop should not be called")
			return nil
		},
	)

	got := reapOrphanedClaudeSessions("t1", "/wt")
	testutil.Nil(t, got)
}

func TestReapOrphanedClaudeSessions_StopsBackgroundAlive(t *testing.T) {
	var stoppedIDs []string
	stubBackgroundSessions(t,
		func(ctx context.Context, cwd string) ([]claudeagents.Session, error) {
			testutil.Equal(t, cwd, "/wt")
			return []claudeagents.Session{
				{Kind: "background", ID: "bg1", PID: 111},
			}, nil
		},
		func(ctx context.Context, id string) error {
			stoppedIDs = append(stoppedIDs, id)
			return nil
		},
	)

	got := reapOrphanedClaudeSessions("t1", "/wt")
	testutil.DeepEqual(t, got, []string{"bg1"})
	testutil.DeepEqual(t, stoppedIDs, []string{"bg1"})
}

func TestReapOrphanedClaudeSessions_SkipsInteractive(t *testing.T) {
	stubBackgroundSessions(t,
		func(ctx context.Context, cwd string) ([]claudeagents.Session, error) {
			return []claudeagents.Session{
				{Kind: "interactive", PID: 222, SessionID: "own-session"},
			}, nil
		},
		func(ctx context.Context, id string) error {
			t.Fatal("Stop should not be called for an interactive session")
			return nil
		},
	)

	got := reapOrphanedClaudeSessions("t1", "/wt")
	testutil.Nil(t, got)
}

func TestReapOrphanedClaudeSessions_SkipsExitedBackground(t *testing.T) {
	stubBackgroundSessions(t,
		func(ctx context.Context, cwd string) ([]claudeagents.Session, error) {
			return []claudeagents.Session{
				{Kind: "background", ID: "bg1", State: "done"}, // no PID: already exited
			}, nil
		},
		func(ctx context.Context, id string) error {
			t.Fatal("Stop should not be called for an already-exited session")
			return nil
		},
	)

	got := reapOrphanedClaudeSessions("t1", "/wt")
	testutil.Nil(t, got)
}

func TestReapOrphanedClaudeSessions_MultipleEntries(t *testing.T) {
	var stoppedIDs []string
	stubBackgroundSessions(t,
		func(ctx context.Context, cwd string) ([]claudeagents.Session, error) {
			return []claudeagents.Session{
				{Kind: "interactive", PID: 1},
				{Kind: "background", ID: "bg1", PID: 2},
				{Kind: "background", ID: "bg2", State: "done"},
				{Kind: "background", ID: "bg3", PID: 3},
			}, nil
		},
		func(ctx context.Context, id string) error {
			stoppedIDs = append(stoppedIDs, id)
			return nil
		},
	)

	got := reapOrphanedClaudeSessions("t1", "/wt")
	testutil.DeepEqual(t, got, []string{"bg1", "bg3"})
	testutil.DeepEqual(t, stoppedIDs, []string{"bg1", "bg3"})
}

func TestReapOrphanedClaudeSessions_ListErrorSwallowed(t *testing.T) {
	stubBackgroundSessions(t,
		func(ctx context.Context, cwd string) ([]claudeagents.Session, error) {
			return nil, errors.New("boom")
		},
		func(ctx context.Context, id string) error {
			t.Fatal("Stop should not be called when List fails")
			return nil
		},
	)

	got := reapOrphanedClaudeSessions("t1", "/wt")
	testutil.Nil(t, got)
}

func TestReapOrphanedClaudeSessions_ClaudeUnavailableSwallowed(t *testing.T) {
	stubBackgroundSessions(t,
		func(ctx context.Context, cwd string) ([]claudeagents.Session, error) {
			return nil, claudeagents.ErrUnavailable
		},
		func(ctx context.Context, id string) error {
			t.Fatal("Stop should not be called when claude is unavailable")
			return nil
		},
	)

	got := reapOrphanedClaudeSessions("t1", "/wt")
	testutil.Nil(t, got)
}

func TestReapOrphanedClaudeSessions_StopErrorSwallowed_ContinuesOthers(t *testing.T) {
	stubBackgroundSessions(t,
		func(ctx context.Context, cwd string) ([]claudeagents.Session, error) {
			return []claudeagents.Session{
				{Kind: "background", ID: "bg-fails", PID: 1},
				{Kind: "background", ID: "bg-ok", PID: 2},
			}, nil
		},
		func(ctx context.Context, id string) error {
			if id == "bg-fails" {
				return errors.New("claude stop failed")
			}
			return nil
		},
	)

	got := reapOrphanedClaudeSessions("t1", "/wt")
	testutil.DeepEqual(t, got, []string{"bg-ok"})
}

// TestRunner_Stop_TriggersReap asserts Runner.Stop fires the reap check with
// the stopped task's worktree, and that Stop's own return is not delayed by
// it (the fake list call blocks briefly before signaling — Stop must return
// well before that).
func TestRunner_Stop_TriggersReap(t *testing.T) {
	seen := make(chan string, 1)
	stubBackgroundSessions(t,
		func(ctx context.Context, cwd string) ([]claudeagents.Session, error) {
			time.Sleep(50 * time.Millisecond)
			seen <- cwd
			return nil, nil
		},
		func(ctx context.Context, id string) error { return nil },
	)

	r := NewRunner(nil)
	worktree := t.TempDir()
	task := &model.Task{ID: "t-reap", Name: "test", Worktree: worktree}
	_, err := r.Start(task, runnerTestConfig(), 24, 80, false)
	testutil.NoError(t, err)

	stopStart := time.Now()
	err = r.Stop("t-reap")
	testutil.NoError(t, err)
	if elapsed := time.Since(stopStart); elapsed >= 50*time.Millisecond {
		t.Errorf("Stop took %v — the reap goroutine appears to be blocking it", elapsed)
	}

	select {
	case cwd := <-seen:
		testutil.Equal(t, cwd, worktree)
	case <-time.After(2 * time.Second):
		t.Fatal("reap goroutine never ran")
	}
}
