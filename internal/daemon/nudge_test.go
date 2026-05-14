package daemon

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

// TestNudge_NoRunnerReturnsSentinel covers the nil-runner branch — defensive
// path mostly exercised by direct unit assertion, but worth pinning so a
// future refactor that swaps in a typed-nil runner pointer doesn't silently
// pass through.
func TestNudge_NoRunnerReturnsSentinel(t *testing.T) {
	n := runnerNudger{}
	err := n.Nudge("any-id", "line\n")
	if !errors.Is(err, ErrNudgeNoSession) {
		t.Fatalf("expected ErrNudgeNoSession, got %v", err)
	}
}

// TestNudge_UnknownTaskReturnsSentinel covers the no-live-session case: a
// real runner exists but nothing's started for the target task.
func TestNudge_UnknownTaskReturnsSentinel(t *testing.T) {
	r := agent.NewRunner(nil)
	n := runnerNudger{runner: r}
	err := n.Nudge("no-such-task", "line\n")
	if !errors.Is(err, ErrNudgeNoSession) {
		t.Fatalf("expected ErrNudgeNoSession, got %v", err)
	}
}

// TestNudge_LiveSessionWritesToPTY confirms the happy path: when a session
// exists, the nudge line lands as input to its PTY. We start a session
// running `cat`, nudge it, then read back the echoed output.
func TestNudge_LiveSessionWritesToPTY(t *testing.T) {
	if testing.Short() {
		t.Skip("uses real PTY")
	}
	r := agent.NewRunner(nil)
	cfg := config.Config{
		Defaults: config.Defaults{Backend: "test"},
		Backends: map[string]config.Backend{
			// `cat` echoes stdin back, so the nudge bytes appear in the
			// session's ring buffer where we can read them.
			"test": {Command: "sh -c 'cat'", PromptFlag: ""},
		},
		Projects: make(map[string]config.Project),
	}
	task := &model.Task{ID: "nudge-target", Name: "test", Worktree: t.TempDir()}
	_, err := r.Start(task, cfg, 24, 80, false)
	testutil.NoError(t, err)
	defer r.StopAll()

	n := runnerNudger{runner: r}
	if err := n.Nudge("nudge-target", "hello-nudge\n"); err != nil {
		t.Fatalf("nudge failed: %v", err)
	}

	// Give cat a moment to echo; poll the session's ring buffer for the
	// nudge text. Bounded by 2s so a stuck PTY doesn't hang CI.
	sess := r.Get("nudge-target")
	if sess == nil {
		t.Fatal("session disappeared after nudge")
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(string(sess.RecentOutput()), "hello-nudge") {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("nudge bytes never appeared in PTY output; got %q", string(sess.RecentOutput()))
}
