package agent

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
)

// initGitRepo creates a git repo with one commit in a fresh temp dir and
// returns its path. Used as a project root for CreateAndStart tests.
// Also redirects HOME to a test temp dir so db.DataDir() / WorktreeDir()
// never touch the real ~/.argus/.
func initGitRepo(t *testing.T) string {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	repo := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run("init", "-q")
	run("config", "user.email", "test@test.com")
	run("config", "user.name", "Test")
	os.WriteFile(filepath.Join(repo, "README.md"), []byte("hi"), 0o644) //nolint:errcheck
	run("add", ".")
	run("commit", "-q", "-m", "init")
	return repo
}

// createTestDB returns an in-memory DB with a "proj" project pointing to
// repoPath and a "test" backend. Callers can override before use.
func createTestDB(t *testing.T, repoPath string) *db.DB {
	t.Helper()
	d, err := db.OpenInMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })

	// Seed config: backend that exits immediately (echo) and a project.
	_ = d.SetConfigValue("defaults.backend", "test")
	_ = d.SetBackend("test", config.Backend{Command: "echo hello", PromptFlag: ""})
	_ = d.SetProject("proj", config.Project{Path: repoPath, Branch: "HEAD"})
	return d
}

// fakeRunner is a SessionProvider that records Start calls and returns a
// configurable error. Used to inject failures at the runner.Start step.
type fakeRunner struct {
	startErr    error
	startCalls  int
	startedTask *model.Task
	sessionPID  int
}

func (f *fakeRunner) Start(task *model.Task, _ config.Config, _, _ uint16, _ bool) (SessionHandle, error) {
	f.startCalls++
	f.startedTask = task
	if f.startErr != nil {
		return nil, f.startErr
	}
	return &fakeSession{pid: f.sessionPID}, nil
}

func (f *fakeRunner) Stop(string) error                   { return nil }
func (f *fakeRunner) StopAll()                            {}
func (f *fakeRunner) Get(string) SessionHandle            { return nil }
func (f *fakeRunner) Running() []string                   { return nil }
func (f *fakeRunner) Idle() []string                      { return nil }
func (f *fakeRunner) RunningAndIdle() ([]string, []string) { return nil, nil }
func (f *fakeRunner) HasSession(string) bool              { return false }
func (f *fakeRunner) WorkDir(string) string               { return "" }

type fakeSession struct{ pid int }

func (s *fakeSession) PID() int                         { return s.pid }
func (s *fakeSession) WriteInput([]byte) (int, error)   { return 0, nil }
func (s *fakeSession) Resize(uint16, uint16) error      { return nil }
func (s *fakeSession) RecentOutput() []byte             { return nil }
func (s *fakeSession) RecentOutputTail(int) []byte      { return nil }
func (s *fakeSession) TotalWritten() uint64             { return 0 }
func (s *fakeSession) IsIdle() bool                     { return false }
func (s *fakeSession) Alive() bool                      { return true }
func (s *fakeSession) PTYSize() (int, int)              { return 80, 24 }
func (s *fakeSession) Done() <-chan struct{}            { ch := make(chan struct{}); return ch }
func (s *fakeSession) Err() error                       { return nil }
func (s *fakeSession) WorkDir() string                  { return "" }
func (s *fakeSession) Stop() error                      { return nil }
func (s *fakeSession) AddWriter(io.Writer)              {}
func (s *fakeSession) RemoveWriter(io.Writer)           {}

// hookLog records the order of pre/post/unwind hook invocations so tests can
// assert sequencing.
type hookLog struct{ events []string }

func (h *hookLog) record(s string) { h.events = append(h.events, s) }

func TestCreateAndStart_Success(t *testing.T) {
	repo := initGitRepo(t)
	d := createTestDB(t, repo)
	fr := &fakeRunner{sessionPID: 4242}
	hl := &hookLog{}

	task, sess, err := CreateAndStart(d, fr, CreateInput{
		Name:        "widget-task",
		Prompt:      "do a thing",
		Project:     "proj",
		BeforeStart: func() { hl.record("before") },
		AfterStart:  func() { hl.record("after") },
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if task == nil || sess == nil {
		t.Fatal("expected task and session")
	}
	if task.Status != model.StatusInProgress {
		t.Errorf("status = %v, want InProgress", task.Status)
	}
	if task.AgentPID != 4242 {
		t.Errorf("AgentPID = %d, want 4242", task.AgentPID)
	}
	if task.Worktree == "" || !filepath.IsAbs(task.Worktree) {
		t.Errorf("Worktree not set: %q", task.Worktree)
	}
	if task.Branch != "argus/widget-task" {
		t.Errorf("Branch = %q, want argus/widget-task", task.Branch)
	}

	// DB row exists and matches.
	row, err := d.Get(task.ID)
	if err != nil {
		t.Fatalf("db.Get: %v", err)
	}
	if row.Worktree != task.Worktree {
		t.Errorf("db row Worktree mismatch")
	}
	if row.Status != model.StatusInProgress {
		t.Errorf("db row status = %v", row.Status)
	}

	// Worktree on disk.
	if !dirExists(task.Worktree) {
		t.Errorf("worktree dir missing: %s", task.Worktree)
	}

	// Hooks fired in order around runner.Start.
	if len(hl.events) != 2 || hl.events[0] != "before" || hl.events[1] != "after" {
		t.Errorf("hook order = %v, want [before after]", hl.events)
	}
	if fr.startCalls != 1 {
		t.Errorf("startCalls = %d, want 1", fr.startCalls)
	}

	// Explicit cleanup so the test doesn't leave a worktree around.
	RemoveWorktreeAndBranch(task.Worktree, task.Branch, repo)
}

func TestCreateAndStart_UnwindsOnStartFailure(t *testing.T) {
	repo := initGitRepo(t)
	d := createTestDB(t, repo)
	fr := &fakeRunner{startErr: errors.New("boom")}

	// Confirm no tasks before.
	before, _ := d.Tasks()
	if len(before) != 0 {
		t.Fatalf("expected empty DB, got %d tasks", len(before))
	}

	task, sess, err := CreateAndStart(d, fr, CreateInput{
		Name:     "doomed",
		Prompt:   "nope",
		Project:  "proj",
		TodoPath: "/vault/doomed.md",
	})
	if err == nil {
		t.Fatal("expected error from CreateAndStart")
	}
	if task != nil || sess != nil {
		t.Errorf("expected nil task and session on failure, got task=%v sess=%v", task, sess)
	}

	// INVARIANT: no DB row left behind — the TodoPath dedup check must be
	// free to retry the same vault file later.
	after, _ := d.Tasks()
	if len(after) != 0 {
		t.Errorf("expected DB unwound, got %d tasks: %+v", len(after), after)
	}
	byPath, _ := d.TasksByTodoPath()
	if _, found := byPath["/vault/doomed.md"]; found {
		t.Errorf("TasksByTodoPath should not contain the failed task")
	}

	// INVARIANT: no worktree directory left on disk.
	expectedWT := WorktreeDir("proj", "doomed")
	if dirExists(expectedWT) {
		entries, _ := os.ReadDir(expectedWT)
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("worktree should have been removed: %s (contents: %v)", expectedWT, names)
	}

	// INVARIANT: no argus/doomed branch lingering in the repo.
	checkBranch := exec.Command("git", "rev-parse", "--verify", "argus/doomed")
	checkBranch.Dir = repo
	if err := checkBranch.Run(); err == nil {
		t.Errorf("branch argus/doomed should have been deleted")
	}

	// Runner was called exactly once.
	if fr.startCalls != 1 {
		t.Errorf("startCalls = %d, want 1", fr.startCalls)
	}
}

func TestCreateAndStart_UnwindsOnHookFailure(t *testing.T) {
	repo := initGitRepo(t)
	d := createTestDB(t, repo)
	fr := &fakeRunner{} // should never be called

	_, _, err := CreateAndStart(d, fr, CreateInput{
		Name:    "hookfail",
		Project: "proj",
		OnWorktreeCreated: func(string) error {
			return errors.New("context write failed")
		},
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if fr.startCalls != 0 {
		t.Errorf("runner.Start should not have been called; got %d calls", fr.startCalls)
	}

	tasks, _ := d.Tasks()
	if len(tasks) != 0 {
		t.Errorf("expected no DB rows, got %d", len(tasks))
	}
	if dirExists(WorktreeDir("proj", "hookfail")) {
		t.Errorf("worktree should have been removed after hook failure")
	}
}

func TestCreateAndStart_RejectsMissingProject(t *testing.T) {
	// Redirect HOME for consistency with other CreateAndStart tests — this
	// test fails before any worktree op, but adding the guard now means
	// future assertions that touch WorktreeDir() can't regress to the real
	// ~/.argus/ path.
	t.Setenv("HOME", t.TempDir())
	d := createTestDB(t, t.TempDir())
	fr := &fakeRunner{}

	_, _, err := CreateAndStart(d, fr, CreateInput{
		Name:    "orphan",
		Project: "does-not-exist",
	})
	if err == nil {
		t.Fatal("expected error for missing project")
	}
	// Must fail before any side effects.
	tasks, _ := d.Tasks()
	if len(tasks) != 0 {
		t.Errorf("expected no DB rows on early failure, got %d", len(tasks))
	}
}

func TestCreateAndStart_HookReceivesWorktreePath(t *testing.T) {
	repo := initGitRepo(t)
	d := createTestDB(t, repo)
	fr := &fakeRunner{}

	var hookPath string
	task, _, err := CreateAndStart(d, fr, CreateInput{
		Name:    "ctxhook",
		Project: "proj",
		OnWorktreeCreated: func(wtPath string) error {
			hookPath = wtPath
			// Write a sentinel file — verifies the hook runs in the worktree.
			return os.WriteFile(filepath.Join(wtPath, ".sentinel"), []byte("ok"), 0o644)
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hookPath != task.Worktree {
		t.Errorf("hook got %q, task has %q", hookPath, task.Worktree)
	}
	if _, statErr := os.Stat(filepath.Join(task.Worktree, ".sentinel")); statErr != nil {
		t.Errorf("sentinel file should exist in worktree: %v", statErr)
	}

	RemoveWorktreeAndBranch(task.Worktree, task.Branch, repo)
}

// TestCreateAndStart_SessionIDPersistedForClaude verifies the Claude-backend
// SessionID generation path. Codex backends skip this (captured post-exit),
// which is exercised by CreateAndStart implicitly through IsCodexBackend.
func TestCreateAndStart_SessionIDPersistedForClaude(t *testing.T) {
	repo := initGitRepo(t)
	d := createTestDB(t, repo)
	// Swap the test backend to look like Claude (generates session ID).
	_ = d.SetBackend("test", config.Backend{Command: "claude", PromptFlag: ""})
	fr := &fakeRunner{sessionPID: 7}

	task, _, err := CreateAndStart(d, fr, CreateInput{
		Name:    "claude-task",
		Project: "proj",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if task.SessionID == "" {
		t.Error("expected SessionID to be generated for Claude backend")
	}
	// Must be persisted in DB too.
	row, _ := d.Get(task.ID)
	if row.SessionID != task.SessionID {
		t.Errorf("db row SessionID = %q, want %q", row.SessionID, task.SessionID)
	}

	RemoveWorktreeAndBranch(task.Worktree, task.Branch, repo)
}

// TestCreateAndStart_StartedAtSetOnSuccess verifies the StartedAt timestamp
// is recorded post-Start — callers depend on this for task display.
func TestCreateAndStart_StartedAtSetOnSuccess(t *testing.T) {
	repo := initGitRepo(t)
	d := createTestDB(t, repo)
	fr := &fakeRunner{sessionPID: 1}

	before := time.Now()
	task, _, err := CreateAndStart(d, fr, CreateInput{
		Name:    "timer",
		Project: "proj",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if task.StartedAt.Before(before) {
		t.Errorf("StartedAt %v should be >= %v", task.StartedAt, before)
	}
	RemoveWorktreeAndBranch(task.Worktree, task.Branch, repo)
}
