package mcp

import (
	"errors"
	"testing"

	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

// These tests pin resolveTask's BUG-059 disambiguation: a cwd whose
// worktree_path is shared by 2+ argus tasks (a stale task reusing the
// worktree directory of a prior, now-archived/reviewed task) must resolve to
// the live task, not whichever task the DB happened to list first.

func serverWithMockTasks(tasks []*model.Task) *Server {
	s := testServer()
	s.SetTaskManager(nil, &mockTaskDB{tasks: tasks}, &mockStopper{})
	return s
}

func TestResolveTask_PrefersInProgressOverStaleArchived(t *testing.T) {
	// Stale task listed FIRST — pre-fix "first match wins" returned this one.
	s := serverWithMockTasks([]*model.Task{
		{ID: "stale", Worktree: "/wt", Status: model.StatusInReview, Archived: true},
		{ID: "live", Worktree: "/wt", Status: model.StatusInProgress},
	})

	got, err := s.resolveTask("", "/wt")
	testutil.NoError(t, err)
	testutil.Equal(t, got.ID, "live")
}

func TestResolveTask_AmbiguousWhenTwoInProgressShareWorktree(t *testing.T) {
	s := serverWithMockTasks([]*model.Task{
		{ID: "a", Worktree: "/wt", Status: model.StatusInProgress},
		{ID: "b", Worktree: "/wt", Status: model.StatusInProgress},
	})

	_, err := s.resolveTask("", "/wt")
	if err == nil {
		t.Fatal("expected ambiguity error, got nil")
	}
	var amb *CwdAmbiguousError
	if !errors.As(err, &amb) {
		t.Fatalf("want *CwdAmbiguousError, got %T: %v", err, err)
	}
	testutil.Equal(t, len(amb.Candidates), 2)
	testutil.Contains(t, amb.Error(), "multiple live argus tasks")
}

func TestResolveTask_AllArchivedIsUnknown(t *testing.T) {
	s := serverWithMockTasks([]*model.Task{
		{ID: "a", Worktree: "/wt", Status: model.StatusComplete, Archived: true},
		{ID: "b", Worktree: "/wt", Status: model.StatusInReview, Archived: true},
	})

	_, err := s.resolveTask("", "/wt")
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	testutil.Contains(t, err.Error(), "no task matches cwd")
}

func TestResolveTask_SingleMatchUnchanged(t *testing.T) {
	// The common case: exactly one task at this worktree — no status needed.
	s := serverWithMockTasks([]*model.Task{
		{ID: "only", Worktree: "/wt"},
	})

	got, err := s.resolveTask("", "/wt")
	testutil.NoError(t, err)
	testutil.Equal(t, got.ID, "only")
}

func TestResolveTask_TwoArchivedOneActiveSharesWorktree(t *testing.T) {
	// 2+ archived matches plus exactly one non-archived match (regardless of
	// its status) → the non-archived task wins without needing the
	// in_progress tiebreak.
	s := serverWithMockTasks([]*model.Task{
		{ID: "old1", Worktree: "/wt", Status: model.StatusComplete, Archived: true},
		{ID: "old2", Worktree: "/wt", Status: model.StatusInReview, Archived: true},
		{ID: "current", Worktree: "/wt", Status: model.StatusPending},
	})

	got, err := s.resolveTask("", "/wt")
	testutil.NoError(t, err)
	testutil.Equal(t, got.ID, "current")
}

func TestResolveTask_TwoActiveNeitherInProgressIsAmbiguous(t *testing.T) {
	// Two non-archived matches, neither in_progress → still genuinely
	// ambiguous; must not guess.
	s := serverWithMockTasks([]*model.Task{
		{ID: "a", Worktree: "/wt", Status: model.StatusPending},
		{ID: "b", Worktree: "/wt", Status: model.StatusInReview},
	})

	_, err := s.resolveTask("", "/wt")
	var amb *CwdAmbiguousError
	if !errors.As(err, &amb) {
		t.Fatalf("want *CwdAmbiguousError, got %T: %v", err, err)
	}
}

func TestResolveTask_UnrelatedWorktreesUnaffected(t *testing.T) {
	// Two DIFFERENT worktrees at the same length must not be treated as a
	// tie — only literally-identical worktree paths trigger disambiguation.
	s := serverWithMockTasks([]*model.Task{
		{ID: "a", Worktree: "/wt/aaa", Status: model.StatusInProgress},
		{ID: "b", Worktree: "/wt/bbb", Status: model.StatusInProgress},
	})

	got, err := s.resolveTask("", "/wt/aaa")
	testutil.NoError(t, err)
	testutil.Equal(t, got.ID, "a")
}
