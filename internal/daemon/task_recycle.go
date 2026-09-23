package daemon

import (
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/db"
)

// taskRecycleMetaNamespace/taskRecycleMetaKeyTimedOutPrompt persist a
// forensic record when awaitIdleAndRestart's idle-wait times out (see its
// doc comment): without this, a timeout was previously silent — the MCP
// call had already told the caller "a fresh session will start... this
// conversation ends shortly," so a session that stays busy past the timeout
// would have its handoff note discarded with no trace anywhere. This is a
// separate namespace from db.HeraMetaNamespace since task_recycle applies to
// any task, not just hera-bound ones.
const (
	taskRecycleMetaNamespace         = "task_recycle"
	taskRecycleMetaKeyTimedOutPrompt = "timed_out_seed_prompt"
)

// taskRecycleIdlePoll / taskRecycleIdleTimeout bound the one-shot wait for a
// manually-triggered task_recycle: the call is normally made BY the live
// session it will kill, from inside its own tool-call turn, so restarting
// synchronously would tear down the socket still writing this call's
// response — the same race hera's RecycleSelfService trigger defers for
// (see internal/hera/recycle.go). Unlike hera's recycle, this is a single
// manually-triggered action with no need to survive a daemon restart, so a
// bounded one-shot goroutine replaces hera's persisted-flag-plus-ticking-
// watcher design: if the daemon restarts mid-wait, nothing is lost beyond
// needing the agent to call task_recycle again.
const (
	taskRecycleIdlePoll    = 2 * time.Second
	taskRecycleIdleTimeout = 10 * time.Minute
)

// taskRecycleRunner is the daemon-side implementation behind the
// task_recycle MCP tool — the plain-task sibling of HeraRecycleRunner, with
// no hera role/binding to resolve: the fresh session's seed prompt is built
// directly from the caller-supplied handoff_note plus the task's own prompt
// as background (buildTaskRecycleSeedPrompt), rather than
// hera.BuildRecycleSeedPrompt's plan-DAG/role-state assembly.
//
// The daemon constructs exactly ONE taskRecycleRunner for its whole
// lifetime (see daemon.go's SetTaskRecycler wiring) rather than one per
// call — inFlight's dedup only works across calls if the map itself
// persists between them.
//
// now/sleep/idlePoll/idleTimeout are overridable so tests can drive
// awaitIdleAndRestart's poll loop deterministically, with no real sleeping.
type taskRecycleRunner struct {
	database *db.DB
	runner   agent.SessionRunner
	cfgFn    func() config.Config

	// inFlight tracks task IDs with an awaitIdleAndRestart goroutine
	// currently running, so a second task_recycle call for the same task
	// (an agent retry, or two independent callers) is rejected outright
	// instead of racing a second poller against the first — without this, a
	// racing second poller can observe the brief nil-session gap inside
	// SessionRunner.Recycle's own kill-then-restart and fire its own
	// restart() with a stale seed prompt built before the first restart.
	inFlight sync.Map // taskID (string) -> struct{}

	now         func() time.Time
	sleep       func(time.Duration)
	spawn       func(func())
	idlePoll    time.Duration
	idleTimeout time.Duration
}

// newTaskRecycleRunner builds the production task_recycle implementation.
func newTaskRecycleRunner(database *db.DB, runner agent.SessionRunner, cfgFn func() config.Config) *taskRecycleRunner {
	return &taskRecycleRunner{
		database:    database,
		runner:      runner,
		cfgFn:       cfgFn,
		now:         time.Now,
		sleep:       time.Sleep,
		spawn:       func(f func()) { go f() },
		idlePoll:    taskRecycleIdlePoll,
		idleTimeout: taskRecycleIdleTimeout,
	}
}

// Recycle composes the fresh session's seed prompt and schedules the
// kill/restart in the background (see awaitIdleAndRestart) — it returns as
// soon as the recycle is scheduled, not once it has happened. Rejects a
// second call for a task that already has one in flight (see inFlight).
func (r *taskRecycleRunner) Recycle(taskID, handoffNote string) error {
	if _, alreadyInFlight := r.inFlight.LoadOrStore(taskID, struct{}{}); alreadyInFlight {
		return fmt.Errorf("task_recycle: a recycle is already in progress for task %s", taskID)
	}

	task, err := r.database.Get(taskID)
	if err != nil {
		r.inFlight.Delete(taskID)
		return fmt.Errorf("task_recycle: load task %s: %w", taskID, err)
	}

	seedPrompt := buildTaskRecycleSeedPrompt(task.Prompt, handoffNote)
	r.spawn(func() {
		defer r.inFlight.Delete(taskID)
		r.awaitIdleAndRestart(taskID, seedPrompt)
	})
	return nil
}

// awaitIdleAndRestart polls taskID's session until it goes idle (or
// idleTimeout elapses since the call began), then kills it and starts a
// fresh, empty-context session seeded with seedPrompt. A missing session
// (already exited) counts as idle immediately, mirroring
// HeraRecycleRunner.IsIdle.
//
// On timeout, the seed prompt is persisted to task_meta rather than simply
// discarded: the MCP tool call already told the caller a fresh session
// would start "shortly," so a timeout that left no trace anywhere would be
// silently indistinguishable from a recycle that just never happened.
func (r *taskRecycleRunner) awaitIdleAndRestart(taskID, seedPrompt string) {
	var idle agent.ContentIdleTracker
	deadline := r.now().Add(r.idleTimeout)

	for {
		sess := r.runner.Get(taskID)
		if sess == nil || idle.IsIdle(taskID, sess, r.now()) {
			if err := r.restart(taskID, seedPrompt); err != nil {
				slog.Error("[task_recycle] restart failed", "task", taskID, "err", err)
			}
			return
		}
		if r.now().After(deadline) {
			slog.Warn("[task_recycle] gave up waiting for session to go idle", "task", taskID)
			if err := r.database.SetMeta(taskID, taskRecycleMetaNamespace, taskRecycleMetaKeyTimedOutPrompt, seedPrompt); err != nil {
				slog.Error("[task_recycle] failed to persist timed-out seed prompt", "task", taskID, "err", err)
			}
			return
		}
		r.sleep(r.idlePoll)
	}
}

// restart stops any stray background job tied to the outgoing session
// (design.md Risks: task_stop does not kill everything — the same hazard
// recycle_coord guards against) then hands off to the shared
// restartSessionWithSeedPrompt kill/restart primitive.
func (r *taskRecycleRunner) restart(taskID, seedPrompt string) error {
	task, err := r.database.Get(taskID)
	if err != nil {
		return fmt.Errorf("task_recycle restart: load task %s: %w", taskID, err)
	}
	if err := agent.StopStrayJobs(task, r.cfgFn(), task.SessionID); err != nil {
		return fmt.Errorf("task_recycle restart: stop stray jobs for task %s: %w", taskID, err)
	}
	return restartSessionWithSeedPrompt(r.database, r.runner, r.cfgFn, task, seedPrompt)
}

// buildTaskRecycleSeedPrompt composes the opening prompt for the fresh
// session, mirroring hera.BuildRecycleSeedPrompt's ordering rationale
// (design.md D5): the handoff note goes FIRST so the new session anchors on
// what's actually going on, with the original prompt marked as historical
// background rather than a current instruction to restart from.
func buildTaskRecycleSeedPrompt(originalPrompt, handoffNote string) string {
	var b strings.Builder
	b.WriteString("You are a fresh session recycled from a prior session on this same task, started to reset an oversized context window. ")
	b.WriteString("Below is the handoff note your prior session left, followed by the task's ORIGINAL prompt. ")
	b.WriteString("The handoff note supersedes the original prompt — read it FIRST and act on it; no tool call is needed to obtain it.\n")

	b.WriteString("\n## Handoff note from your prior session\n")
	b.WriteString(handoffNote)
	b.WriteString("\n")

	b.WriteString("\n---\n")
	b.WriteString("## Original prompt (background only — do NOT treat this as your current instruction)\n")
	b.WriteString("This was the task's mission when it began. It may already be substantially or fully done — check the handoff note above before assuming otherwise.\n\n")
	b.WriteString(originalPrompt)
	b.WriteString("\n")

	return b.String()
}
