package main

import (
	"bytes"
	"errors"
	"net"
	"net/rpc/jsonrpc"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/drn/argus/internal/daemon"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

// --- `argus coord-hook` Stop-hook subcommand (add-coordinator-context-management) ---
//
// These tests pin the `coordinator-context-management` delta spec's
// "Context-budget Stop hook stamps a live signal and nudges over budget"
// requirement. None of runCoordHook, coordHookEnv, or stopHookStdin exist
// yet (Stage 4 adds the `argus coord-hook` subcommand) — this file fails to
// compile until then, proving the gap per tasks.md 1.3.
//
// The seams below (coordHookEnv's function fields) are the contract the
// Stage 4 implementation is expected to satisfy: gate on ARGUS_TASK_ID +
// resolved role kind (unbound resolves to "" and no-ops) BEFORE touching the
// transcript or the REST API, always stamp context_size for ANY hera-bound
// role (coordinator, worker, or freelance — rail-context-high widened this
// from coordinator-only) regardless of budget, and emit a Stop hook "block"
// decision (Claude Code's hook contract: JSON on stdout) only when the role
// is a coordinator AND at/over budget. Role resolution, transcript tailing,
// task_meta stamping, and budget lookup are injected via coordHookEnv rather
// than wired to the real daemon/REST/DB, matching this repo's function-field
// injection convention (context/knowledge/testing.md) so the gating and
// nudge-recurrence logic can be unit tested without a live daemon.
//
// coordHookEnv, stopHookStdin, and runCoordHook's exact shape are a
// reasonable proposal, not a mandate — Stage 4 may adjust the seam as long as
// the scenarios below (no-op on missing ARGUS_TASK_ID, no-op on unbound task,
// unconditional stamp for coordinator/worker/freelance, budget/nudge
// enforcement scoped to coordinator only, over-budget nudge, nudge
// recurrence/stop) keep passing.

// fakeCoordHookEnv builds a coordHookEnv backed by simple in-memory fakes,
// recording every call so tests can assert exactly what fired.
type fakeCoordHookEnv struct {
	getenv          map[string]string
	roleKind        string // "" means "no role bound" (also covers ARGUS_TASK_ID unset)
	roleKindErr     error
	contextSize     int
	readContextErr  error
	budget          int
	budgetErr       error
	stampedTaskID   string
	stampedSize     int
	stampCalled     bool
	stampErr        error
	resolveCalled   bool
	resolvedTaskID  string
	readCalled      bool
	readTranscript  string
	budgetCalled    bool
	budgetForTaskID string

	// pendingRecycle (Part A: idempotent hook) — whether the coordinator has
	// already requested a self-service recycle (task_meta pending_recycle=true).
	pendingRecycle       bool
	pendingRecycleErr    error
	pendingRecycleCalled bool

	// forceRecycle (Part B: hard-stop escalation) — the daemon RPC call that
	// immediately kills+restarts a wedged coordinator once 1.5x over budget.
	forceRecycleErr    error
	forceRecycleCalled bool
	forceRecycleTaskID string

	// lastNudged / nudgeIncrement (throttle-coord-hook-nudge) — throttles the
	// over-budget nudge to only re-fire after context_size has grown by a
	// configurable increment past the size at which it last fired.
	lastNudged            int
	hadLastNudged         bool
	lastNudgedErr         error
	stampedLastNudgedSize int
	stampLastNudgedCalled bool
	stampLastNudgedErr    error
	nudgeIncrement        int
	nudgeIncrementErr     error
}

func (f *fakeCoordHookEnv) env() coordHookEnv {
	return coordHookEnv{
		Getenv: func(key string) string { return f.getenv[key] },
		ResolveRoleKind: func(taskID string) (string, error) {
			f.resolveCalled = true
			f.resolvedTaskID = taskID
			return f.roleKind, f.roleKindErr
		},
		PendingRecycleAlready: func(taskID string) (bool, error) {
			f.pendingRecycleCalled = true
			return f.pendingRecycle, f.pendingRecycleErr
		},
		ReadContextSize: func(transcriptPath string) (int, error) {
			f.readCalled = true
			f.readTranscript = transcriptPath
			return f.contextSize, f.readContextErr
		},
		StampContextSize: func(taskID string, size int) error {
			f.stampCalled = true
			f.stampedTaskID = taskID
			f.stampedSize = size
			return f.stampErr
		},
		Budget: func(taskID string) (int, error) {
			f.budgetCalled = true
			f.budgetForTaskID = taskID
			return f.budget, f.budgetErr
		},
		ForceRecycle: func(taskID string) error {
			f.forceRecycleCalled = true
			f.forceRecycleTaskID = taskID
			return f.forceRecycleErr
		},
		ReadLastNudgedContextSize: func(taskID string) (int, bool, error) {
			return f.lastNudged, f.hadLastNudged, f.lastNudgedErr
		},
		StampLastNudgedContextSize: func(taskID string, size int) error {
			f.stampLastNudgedCalled = true
			f.stampedLastNudgedSize = size
			return f.stampLastNudgedErr
		},
		NudgeIncrement: func(taskID string) (int, error) {
			return f.nudgeIncrement, f.nudgeIncrementErr
		},
	}
}

func stopHookStdin(transcriptPath string) *bytes.Reader {
	return bytes.NewReader([]byte(`{"transcript_path":"` + transcriptPath + `"}`))
}

// TestCoordHook_NoTaskID_NoOp pins "Non-hera session is a no-op": with no
// ARGUS_TASK_ID set, the hook must exit with no side effects — no role
// resolution, no transcript read, no task_meta write, no blocking decision.
func TestCoordHook_NoTaskID_NoOp(t *testing.T) {
	f := &fakeCoordHookEnv{getenv: map[string]string{}}
	var out, errOut bytes.Buffer

	runCoordHook(stopHookStdin("/tmp/transcript.jsonl"), &out, &errOut, f.env())

	testutil.Equal(t, f.resolveCalled, false)
	testutil.Equal(t, f.readCalled, false)
	testutil.Equal(t, f.stampCalled, false)
	testutil.Equal(t, f.budgetCalled, false)
	testutil.Equal(t, f.pendingRecycleCalled, false)
	testutil.Equal(t, f.forceRecycleCalled, false)
	if strings.Contains(out.String(), "block") {
		t.Errorf("no-op path must not emit a block decision; got stdout=%q", out.String())
	}
}

// TestCoordHook_UnboundRole_NoOp pins "Non-hera session is a no-op" for the
// case where ARGUS_TASK_ID is set but the task carries no hera role binding
// at all (ResolveRoleKind resolves to ""): the hook must still exit with no
// side effects — it is not enough to gate on ARGUS_TASK_ID alone.
func TestCoordHook_UnboundRole_NoOp(t *testing.T) {
	f := &fakeCoordHookEnv{
		getenv:   map[string]string{"ARGUS_TASK_ID": "task-1"},
		roleKind: "",
	}
	var out, errOut bytes.Buffer

	runCoordHook(stopHookStdin("/tmp/transcript.jsonl"), &out, &errOut, f.env())

	testutil.Equal(t, f.resolveCalled, true)
	testutil.Equal(t, f.readCalled, false)
	testutil.Equal(t, f.stampCalled, false)
	testutil.Equal(t, f.budgetCalled, false)
	testutil.Equal(t, f.pendingRecycleCalled, false)
	testutil.Equal(t, f.forceRecycleCalled, false)
	if strings.Contains(out.String(), "block") {
		t.Errorf("unbound no-op path must not emit a block decision; got stdout=%q", out.String())
	}
}

// TestCoordHook_WorkerRole_StampsButSkipsBudgetEnforcement pins the widened
// "Context size is stamped for any hera-bound role" behavior
// (rail-context-high): a worker-kind role now gets its context_size stamped
// exactly like a coordinator, since the rail's context-pressure indicator
// needs a live signal for workers too, but it must never reach the
// budget/nudge/hard-stop/recycle machinery below — that stays coordinator-only.
func TestCoordHook_WorkerRole_StampsButSkipsBudgetEnforcement(t *testing.T) {
	f := &fakeCoordHookEnv{
		getenv:      map[string]string{"ARGUS_TASK_ID": "task-1"},
		roleKind:    "worker",
		contextSize: 999999, // deliberately far over any plausible budget
	}
	var out, errOut bytes.Buffer

	runCoordHook(stopHookStdin("/tmp/transcript.jsonl"), &out, &errOut, f.env())

	testutil.Equal(t, f.resolveCalled, true)
	testutil.Equal(t, f.readCalled, true)
	testutil.Equal(t, f.stampCalled, true)
	testutil.Equal(t, f.stampedTaskID, "task-1")
	testutil.Equal(t, f.stampedSize, 999999)
	testutil.Equal(t, f.budgetCalled, false)
	testutil.Equal(t, f.pendingRecycleCalled, false)
	testutil.Equal(t, f.forceRecycleCalled, false)
	if strings.Contains(out.String(), "block") {
		t.Errorf("worker path must never emit a block decision; got stdout=%q", out.String())
	}
}

// TestCoordHook_FreelanceRole_StampsButSkipsBudgetEnforcement mirrors the
// worker test for a freelance-kind role — the third non-coordinator hera
// kind — to confirm the gate change is "any bound role" and not accidentally
// scoped to "worker" specifically.
func TestCoordHook_FreelanceRole_StampsButSkipsBudgetEnforcement(t *testing.T) {
	f := &fakeCoordHookEnv{
		getenv:      map[string]string{"ARGUS_TASK_ID": "task-1"},
		roleKind:    "freelance",
		contextSize: 5000,
	}
	var out, errOut bytes.Buffer

	runCoordHook(stopHookStdin("/tmp/transcript.jsonl"), &out, &errOut, f.env())

	testutil.Equal(t, f.stampCalled, true)
	testutil.Equal(t, f.stampedSize, 5000)
	testutil.Equal(t, f.budgetCalled, false)
	if strings.Contains(out.String(), "block") {
		t.Errorf("freelance path must never emit a block decision; got stdout=%q", out.String())
	}
}

// TestCoordHook_Coordinator_AlwaysStampsContextSize pins "Context size is
// always stamped for a coordinator session": task_meta(hera, context_size)
// must be overwritten regardless of whether the budget is exceeded.
func TestCoordHook_Coordinator_AlwaysStampsContextSize(t *testing.T) {
	f := &fakeCoordHookEnv{
		getenv:      map[string]string{"ARGUS_TASK_ID": "task-1"},
		roleKind:    "coordinator",
		contextSize: 1000, // well under budget
		budget:      200000,
	}
	var out, errOut bytes.Buffer

	runCoordHook(stopHookStdin("/tmp/transcript.jsonl"), &out, &errOut, f.env())

	testutil.Equal(t, f.readCalled, true)
	testutil.Equal(t, f.readTranscript, "/tmp/transcript.jsonl")
	testutil.Equal(t, f.stampCalled, true)
	testutil.Equal(t, f.stampedTaskID, "task-1")
	testutil.Equal(t, f.stampedSize, 1000)
	testutil.Equal(t, f.pendingRecycleCalled, false)
	testutil.Equal(t, f.forceRecycleCalled, false)
	if strings.Contains(out.String(), "block") {
		t.Errorf("under-budget coordinator must not emit a block decision; got stdout=%q", out.String())
	}
}

// TestCoordHook_Coordinator_OverBudget_EmitsNudge pins "Nudge fires on the
// first over-budget turn": at/over budget must emit a Stop hook block
// decision instructing the coordinator to reach a safe seam and recycle.
// (TestCoordHook_Nudge_FiresOnFirstOverBudgetTurn additionally pins the
// last_nudged_context_size stamp this test doesn't check.)
func TestCoordHook_Coordinator_OverBudget_EmitsNudge(t *testing.T) {
	f := &fakeCoordHookEnv{
		getenv:      map[string]string{"ARGUS_TASK_ID": "task-1"},
		roleKind:    "coordinator",
		contextSize: 250000,
		budget:      200000,
	}
	var out, errOut bytes.Buffer

	runCoordHook(stopHookStdin("/tmp/transcript.jsonl"), &out, &errOut, f.env())

	testutil.Equal(t, f.stampCalled, true)
	testutil.Equal(t, f.stampedSize, 250000)
	testutil.Equal(t, f.pendingRecycleCalled, true)
	testutil.Equal(t, f.forceRecycleCalled, false) // 250000 is under the 1.5x (300000) hard-stop threshold
	testutil.Contains(t, out.String(), "block")
	testutil.Contains(t, out.String(), "seam")
}

// TestCoordHook_OverBudgetNudge_RecursThenStops pins both "Nudge is suppressed
// within the same increment window" (recurrence is throttled across two
// independent invocations, since each Stop event is a fresh process, once the
// prior turn already stamped last_nudged_context_size) and "Nudge stops the
// turn context drops back below budget".
func TestCoordHook_OverBudgetNudge_RecursThenStops(t *testing.T) {
	env := map[string]string{"ARGUS_TASK_ID": "task-1"}

	// Turn 1: over budget, no prior nudge stamped yet — fires and stamps 250000.
	f1 := &fakeCoordHookEnv{getenv: env, roleKind: "coordinator", contextSize: 250000, budget: 200000}
	var out1, errOut1 bytes.Buffer
	runCoordHook(stopHookStdin("/tmp/transcript.jsonl"), &out1, &errOut1, f1.env())
	testutil.Contains(t, out1.String(), "block")

	// Turn 2: still over budget, but only 10000 of the 50000 default increment
	// has elapsed since turn 1's nudge at 250000 — the nudge must be suppressed.
	f2 := &fakeCoordHookEnv{
		getenv: env, roleKind: "coordinator", contextSize: 260000, budget: 200000,
		hadLastNudged: true, lastNudged: 250000, nudgeIncrement: 50000,
	}
	var out2, errOut2 bytes.Buffer
	runCoordHook(stopHookStdin("/tmp/transcript.jsonl"), &out2, &errOut2, f2.env())
	if strings.Contains(out2.String(), "block") {
		t.Errorf("nudge must be suppressed within the increment window; got stdout=%q", out2.String())
	}

	// Turn 3: a recycle reset context_size back under budget — the nudge must
	// stop the very turn the condition becomes false.
	f3 := &fakeCoordHookEnv{getenv: env, roleKind: "coordinator", contextSize: 500, budget: 200000}
	var out3, errOut3 bytes.Buffer
	runCoordHook(stopHookStdin("/tmp/transcript.jsonl"), &out3, &errOut3, f3.env())
	if strings.Contains(out3.String(), "block") {
		t.Errorf("nudge must stop once context_size drops below budget; got stdout=%q", out3.String())
	}
	testutil.Equal(t, f3.stampCalled, true) // still stamps, just doesn't block
}

// --- Nudge increment throttle (throttle-coord-hook-nudge) -------------------
//
// The over-budget nudge used to re-fire on EVERY Stop event once context_size
// was at/above budget. These tests pin the throttle: the nudge only re-fires
// once context_size has grown by at least the configured increment past the
// size at which it last fired.

// TestCoordHook_Nudge_FiresOnFirstOverBudgetTurn pins the very first over-budget
// turn (no last-nudged size recorded yet): the nudge must fire and stamp
// last_nudged_context_size with the current size.
func TestCoordHook_Nudge_FiresOnFirstOverBudgetTurn(t *testing.T) {
	f := &fakeCoordHookEnv{
		getenv:        map[string]string{"ARGUS_TASK_ID": "task-1"},
		roleKind:      "coordinator",
		contextSize:   250000,
		budget:        200000,
		hadLastNudged: false,
	}
	var out, errOut bytes.Buffer

	runCoordHook(stopHookStdin("/tmp/transcript.jsonl"), &out, &errOut, f.env())

	testutil.Contains(t, out.String(), "block")
	testutil.Equal(t, f.stampLastNudgedCalled, true)
	testutil.Equal(t, f.stampedLastNudgedSize, 250000)
}

// TestCoordHook_Nudge_SuppressedWithinIncrementWindow pins the throttle's core
// scenario: context_size has grown, but not by the full increment past the
// size at which the nudge last fired — no nudge, and no re-stamp.
func TestCoordHook_Nudge_SuppressedWithinIncrementWindow(t *testing.T) {
	f := &fakeCoordHookEnv{
		getenv:         map[string]string{"ARGUS_TASK_ID": "task-1"},
		roleKind:       "coordinator",
		contextSize:    260000,
		budget:         200000,
		hadLastNudged:  true,
		lastNudged:     250000,
		nudgeIncrement: 50000,
	}
	var out, errOut bytes.Buffer

	runCoordHook(stopHookStdin("/tmp/transcript.jsonl"), &out, &errOut, f.env())

	if strings.Contains(out.String(), "block") {
		t.Errorf("nudge must be suppressed within the increment window; got stdout=%q", out.String())
	}
	testutil.Equal(t, f.stampLastNudgedCalled, false)
}

// TestCoordHook_Nudge_RepeatsAfterFullIncrement pins both sides of the
// increment boundary: exactly the increment past last_nudged fires again (and
// re-stamps), one token under does not. Uses budget=250000 (rather than the
// 200000 used elsewhere in this file) so contextSize=300000 stays clear of the
// UNRELATED 1.5x hard-stop threshold (which would be exactly 300000 at
// budget=200000, per TestCoordHook_HardStop_AtThreshold_ForcesRecycle) —
// this test is exercising the increment gate, not the hard-stop escalation.
func TestCoordHook_Nudge_RepeatsAfterFullIncrement(t *testing.T) {
	t.Run("at exactly the increment", func(t *testing.T) {
		f := &fakeCoordHookEnv{
			getenv:         map[string]string{"ARGUS_TASK_ID": "task-1"},
			roleKind:       "coordinator",
			contextSize:    300000,
			budget:         250000,
			hadLastNudged:  true,
			lastNudged:     250000,
			nudgeIncrement: 50000,
		}
		var out, errOut bytes.Buffer

		runCoordHook(stopHookStdin("/tmp/transcript.jsonl"), &out, &errOut, f.env())

		testutil.Contains(t, out.String(), "block")
		testutil.Equal(t, f.stampLastNudgedCalled, true)
		testutil.Equal(t, f.stampedLastNudgedSize, 300000)
	})

	t.Run("one under the increment", func(t *testing.T) {
		f := &fakeCoordHookEnv{
			getenv:         map[string]string{"ARGUS_TASK_ID": "task-1"},
			roleKind:       "coordinator",
			contextSize:    299999,
			budget:         250000,
			hadLastNudged:  true,
			lastNudged:     250000,
			nudgeIncrement: 50000,
		}
		var out, errOut bytes.Buffer

		runCoordHook(stopHookStdin("/tmp/transcript.jsonl"), &out, &errOut, f.env())

		if strings.Contains(out.String(), "block") {
			t.Errorf("one token under the increment must still be suppressed; got stdout=%q", out.String())
		}
		testutil.Equal(t, f.stampLastNudgedCalled, false)
	})
}

// TestCoordHook_Nudge_FiresImmediatelyOnFreshEpisode is the regression test for
// the "fresh episode" trap: a coordinator recycles, context_size resets low,
// but the stale last_nudged_context_size from the PRIOR session is still
// large. The gating logic must include a `size >= lastNudged` guard — without
// it, `size < lastNudged+increment` alone would wrongly suppress the nudge on
// this fresh over-budget episode, since a stale-and-larger lastNudged makes
// that comparison trivially true.
func TestCoordHook_Nudge_FiresImmediatelyOnFreshEpisode(t *testing.T) {
	f := &fakeCoordHookEnv{
		getenv:         map[string]string{"ARGUS_TASK_ID": "task-1"},
		roleKind:       "coordinator",
		contextSize:    210000, // fresh session, back over budget=200000
		budget:         200000,
		hadLastNudged:  true,
		lastNudged:     300000, // stale, from a prior session
		nudgeIncrement: 50000,
	}
	var out, errOut bytes.Buffer

	runCoordHook(stopHookStdin("/tmp/transcript.jsonl"), &out, &errOut, f.env())

	testutil.Contains(t, out.String(), "block")
	testutil.Equal(t, f.stampLastNudgedCalled, true)
	testutil.Equal(t, f.stampedLastNudgedSize, 210000)
}

// TestCoordHook_Nudge_ReadLastNudgedError_StillBlocks pins the fail-open
// behavior when the last-nudged read errors: the hook must log the error but
// still fall back to emitting the graceful block (treating an unreadable
// value as "no prior nudge") rather than silently dropping the nudge.
func TestCoordHook_Nudge_ReadLastNudgedError_StillBlocks(t *testing.T) {
	f := &fakeCoordHookEnv{
		getenv:        map[string]string{"ARGUS_TASK_ID": "task-1"},
		roleKind:      "coordinator",
		contextSize:   250000,
		budget:        200000,
		lastNudgedErr: errors.New("meta unavailable"),
	}
	var out, errOut bytes.Buffer

	runCoordHook(stopHookStdin("/tmp/transcript.jsonl"), &out, &errOut, f.env())

	testutil.Contains(t, errOut.String(), "meta unavailable")
	testutil.Contains(t, out.String(), "block")
}

// TestCoordHook_Nudge_ReadIncrementError_StillBlocks pins the fail-open
// behavior when the increment read errors: the hook must log the error but
// still fall back to emitting the graceful block.
func TestCoordHook_Nudge_ReadIncrementError_StillBlocks(t *testing.T) {
	f := &fakeCoordHookEnv{
		getenv:            map[string]string{"ARGUS_TASK_ID": "task-1"},
		roleKind:          "coordinator",
		contextSize:       250000,
		budget:            200000,
		nudgeIncrementErr: errors.New("config unavailable"),
	}
	var out, errOut bytes.Buffer

	runCoordHook(stopHookStdin("/tmp/transcript.jsonl"), &out, &errOut, f.env())

	testutil.Contains(t, errOut.String(), "config unavailable")
	testutil.Contains(t, out.String(), "block")
}

// --- Part A: idempotent coord-hook (fix-coordhook-idle-deadlock) -----------
//
// Regression coverage for the live incident: an over-budget coordinator that
// has already requested a self-service recycle (hera_status
// request_recycle=true, mirrored into task_meta pending_recycle="true") must
// NOT be re-blocked on the next Stop event. Blocking forces immediate
// re-engagement, which means the PTY never accumulates the 3s of silence
// IsIdle needs, so RecycleWatcher never sees the session idle and the
// recycle never actually fires — an infinite loop with the budget climbing
// every turn (observed for real: 15+ iterations, ~221K -> ~267K).

// TestCoordHook_Coordinator_PendingRecycleAlready_DoesNotBlock is the
// regression test for the incident: once pending_recycle is already "true",
// a subsequent over-budget Stop event must return with no block decision so
// Claude Code's Stop genuinely goes through and RecycleWatcher gets a real
// idle window.
func TestCoordHook_Coordinator_PendingRecycleAlready_DoesNotBlock(t *testing.T) {
	f := &fakeCoordHookEnv{
		getenv:         map[string]string{"ARGUS_TASK_ID": "task-1"},
		roleKind:       "coordinator",
		contextSize:    250000,
		budget:         200000,
		pendingRecycle: true,
	}
	var out, errOut bytes.Buffer

	runCoordHook(stopHookStdin("/tmp/transcript.jsonl"), &out, &errOut, f.env())

	testutil.Equal(t, f.stampCalled, true) // still stamps, unconditionally
	testutil.Equal(t, f.pendingRecycleCalled, true)
	testutil.Equal(t, f.forceRecycleCalled, false) // 250000 is under the hard-stop threshold
	if strings.Contains(out.String(), "block") {
		t.Errorf("must not re-block once recycle is already pending; got stdout=%q", out.String())
	}
}

// TestCoordHook_PendingRecycleAlready_ReadError_StillBlocks pins the
// fail-safe behavior when the pending-recycle read itself errors: the hook
// must log the error but still fall back to the pre-existing graceful block
// (treating an unreadable flag as "not yet pending") rather than silently
// dropping the nudge.
func TestCoordHook_PendingRecycleAlready_ReadError_StillBlocks(t *testing.T) {
	f := &fakeCoordHookEnv{
		getenv:            map[string]string{"ARGUS_TASK_ID": "task-1"},
		roleKind:          "coordinator",
		contextSize:       250000,
		budget:            200000,
		pendingRecycleErr: errors.New("meta unavailable"),
	}
	var out, errOut bytes.Buffer

	runCoordHook(stopHookStdin("/tmp/transcript.jsonl"), &out, &errOut, f.env())

	testutil.Contains(t, errOut.String(), "meta unavailable")
	testutil.Contains(t, out.String(), "block")
}

// --- Part B: hard-stop escalation at 1.5x budget (fix-coordhook-idle-deadlock) ---
//
// Safety net for when Part A's graceful path is still stuck waiting for
// idleness that never naturally occurs (e.g. a human replying fast enough
// that the PTY never accumulates 3s of silence). Once context_size crosses
// 1.5x the configured budget, the hook forces an immediate recycle via the
// daemon's ForceRecycleCoordinator RPC — unconditionally, regardless of
// whether pending_recycle is already set.

// TestCoordHook_HardStop_JustUnderThreshold_DoesNotForceRecycle pins the
// integer-safe 1.5x boundary math: one token under threshold must still take
// the graceful (Part A) path, not the hard-stop escalation.
func TestCoordHook_HardStop_JustUnderThreshold_DoesNotForceRecycle(t *testing.T) {
	f := &fakeCoordHookEnv{
		getenv:      map[string]string{"ARGUS_TASK_ID": "task-1"},
		roleKind:    "coordinator",
		contextSize: 299999, // one token under 1.5x of 200000 (300000)
		budget:      200000,
	}
	var out, errOut bytes.Buffer

	runCoordHook(stopHookStdin("/tmp/transcript.jsonl"), &out, &errOut, f.env())

	testutil.Equal(t, f.forceRecycleCalled, false)
	testutil.Contains(t, out.String(), "block")
}

// TestCoordHook_HardStop_AtThreshold_ForcesRecycle pins the other side of the
// boundary: exactly 1.5x budget must trigger the hard-stop escalation instead
// of (not in addition to) the graceful block decision.
func TestCoordHook_HardStop_AtThreshold_ForcesRecycle(t *testing.T) {
	f := &fakeCoordHookEnv{
		getenv:      map[string]string{"ARGUS_TASK_ID": "task-1"},
		roleKind:    "coordinator",
		contextSize: 300000, // exactly 1.5x of 200000
		budget:      200000,
	}
	var out, errOut bytes.Buffer

	runCoordHook(stopHookStdin("/tmp/transcript.jsonl"), &out, &errOut, f.env())

	testutil.Equal(t, f.forceRecycleCalled, true)
	testutil.Equal(t, f.forceRecycleTaskID, "task-1")
	if strings.Contains(out.String(), "block") {
		t.Errorf("hard-stop escalation must not also emit the graceful block decision; got stdout=%q", out.String())
	}
}

// TestCoordHook_HardStop_FiresRegardlessOfPendingRecycle pins that the
// hard-stop escalation is unconditional: pending_recycle already being
// "true" (the graceful path already fired) must not suppress it, and the
// pending-recycle read is skipped entirely since it can't change the outcome.
func TestCoordHook_HardStop_FiresRegardlessOfPendingRecycle(t *testing.T) {
	f := &fakeCoordHookEnv{
		getenv:         map[string]string{"ARGUS_TASK_ID": "task-1"},
		roleKind:       "coordinator",
		contextSize:    350000,
		budget:         200000,
		pendingRecycle: true,
	}
	var out, errOut bytes.Buffer

	runCoordHook(stopHookStdin("/tmp/transcript.jsonl"), &out, &errOut, f.env())

	testutil.Equal(t, f.forceRecycleCalled, true)
	testutil.Equal(t, f.pendingRecycleCalled, false)
}

// TestCoordHook_HardStop_ForceRecycleError_LogsToStderr confirms an RPC
// failure is logged but does not crash the hook or fall back to the graceful
// block (a fallback would defeat the point of an unconditional escalation).
func TestCoordHook_HardStop_ForceRecycleError_LogsToStderr(t *testing.T) {
	f := &fakeCoordHookEnv{
		getenv:          map[string]string{"ARGUS_TASK_ID": "task-1"},
		roleKind:        "coordinator",
		contextSize:     300000,
		budget:          200000,
		forceRecycleErr: errors.New("boom"),
	}
	var out, errOut bytes.Buffer

	runCoordHook(stopHookStdin("/tmp/transcript.jsonl"), &out, &errOut, f.env())

	testutil.Contains(t, errOut.String(), "boom")
	if strings.Contains(out.String(), "block") {
		t.Errorf("hard-stop path must not fall back to the graceful block on RPC failure; got stdout=%q", out.String())
	}
}

// --- realCoordHookEnv's production wiring ---
//
// The tests above pin runCoordHook's pure gating/nudge logic via injected
// fakes. These exercise the real implementations those fakes stand in for —
// transcript tailing (pure file I/O), token-file discovery, and the actual
// REST round trip against a live daemon — so the wiring itself (paths,
// header names, JSON field/key names) is proven, not just the contract.

// TestReadContextSizeReal_TailsLatestAssistantUsage confirms the scan finds
// the LAST assistant message's usage, tolerating blank and non-JSON lines
// and non-assistant event types interleaved in a real transcript, and sums
// cache_read + cache_creation + input tokens rather than reading
// cache_read_input_tokens alone.
func TestReadContextSizeReal_TailsLatestAssistantUsage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	lines := []string{
		`{"type":"user","message":{}}`,
		`{"type":"assistant","message":{"usage":{"cache_read_input_tokens":100}}}`,
		"",
		"not json",
		`{"type":"assistant","message":{"usage":{"input_tokens":2,"cache_creation_input_tokens":3000,"cache_read_input_tokens":42000}}}`,
	}
	testutil.NoError(t, os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o600))

	size, err := readContextSizeReal(path)
	testutil.NoError(t, err)
	testutil.Equal(t, size, 45002)
}

// TestReadContextSizeReal_CacheMissStillCountsFullContext pins the exact
// production bug (rail-context-size-metric-fix): a prompt-cache miss (e.g. an
// idle gap crossing the cache TTL) rewrites the ENTIRE prior context as
// cache_creation_input_tokens instead of cache_read_input_tokens, so
// cache_read_input_tokens alone collapses to 0 even though the real context
// is unchanged or larger. Summing all three usage fields must still report
// the true total.
func TestReadContextSizeReal_CacheMissStillCountsFullContext(t *testing.T) {
	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	lines := []string{
		`{"type":"assistant","message":{"usage":{"input_tokens":2,"cache_creation_input_tokens":399236,"cache_read_input_tokens":0}}}`,
	}
	testutil.NoError(t, os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o600))

	size, err := readContextSizeReal(path)
	testutil.NoError(t, err)
	testutil.Equal(t, size, 399238)
}

func TestReadContextSizeReal_MissingFile_Errors(t *testing.T) {
	_, err := readContextSizeReal(filepath.Join(t.TempDir(), "missing.jsonl"))
	if err == nil {
		t.Fatal("expected an error for a missing transcript file")
	}
}

// TestDiscoverAPIToken_ReadsWellKnownPath pins the well-known path
// (db.DataDir()/api-token) the real daemon itself loads/creates at boot.
func TestDiscoverAPIToken_ReadsWellKnownPath(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	testutil.NoError(t, os.MkdirAll(db.DataDir(), 0o700))
	testutil.NoError(t, os.WriteFile(filepath.Join(db.DataDir(), "api-token"), []byte("deadbeef\n"), 0o600))

	token, err := discoverAPIToken()
	testutil.NoError(t, err)
	testutil.Equal(t, token, "deadbeef")
}

func TestDiscoverAPIToken_MissingFile_Errors(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if _, err := discoverAPIToken(); err == nil {
		t.Fatal("expected an error when the token file is absent")
	}
}

// TestDiscoverAPIPort_NoDaemon_Errors covers coordHookDial's connect-failure
// branch without needing a live daemon.
func TestDiscoverAPIPort_NoDaemon_Errors(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if _, err := discoverAPIPort(); err == nil {
		t.Fatal("expected an error when no daemon is listening")
	}
}

// waitForCoordHookDaemon polls Daemon.Ports over the Unix socket until the
// REST API has finished binding — Serve accepts RPC connections before the
// API listener (started later, in its own branch) reports a nonzero port.
func waitForCoordHookDaemon(t *testing.T, sockPath string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if conn, err := net.Dial("unix", sockPath); err == nil {
			_, _ = conn.Write([]byte("R"))
			client := jsonrpc.NewClient(conn)
			var resp daemon.PortsResp
			callErr := client.Call("Daemon.Ports", &daemon.Empty{}, &resp)
			_ = client.Close()
			if callErr == nil && resp.APIPort != 0 {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("daemon REST API did not come up in time")
}

// TestCoordHookReal_REST runs a REAL daemon (under a fake HOME, so
// daemon.DefaultSocketPath()/db.DataDir() — neither injectable — resolve
// into a throwaway tree) and drives resolveRoleKindReal, stampContextSizeReal,
// and budgetReal against it end-to-end: RPC port discovery, the api-token
// file the daemon itself creates, and the actual PUT/GET /api/tasks/{id}/meta
// and GET /api/config routes.
//
// HOME must be a SHORT path from os.MkdirTemp, not t.TempDir(): the latter
// nests under the test name plus a run counter (".../TestCoordHookReal_REST/001"),
// and daemon.sock's full path then trips macOS's 104-byte sun_path limit —
// Serve's Listen fails with "bind: invalid argument" and the socket never
// appears (context/knowledge/gotchas/daemon-rpc.md).
func TestCoordHookReal_REST(t *testing.T) {
	home, err := os.MkdirTemp("", "ch")
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	t.Setenv("HOME", home)

	database, err := db.Open(db.DefaultPath())
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = database.Close() })
	testutil.NoError(t, database.SetConfigValue("api.enabled", "true"))

	d := daemon.New(database)
	sockPath := daemon.DefaultSocketPath()
	go d.Serve(sockPath) //nolint:errcheck
	t.Cleanup(d.Shutdown)
	waitForCoordHookDaemon(t, sockPath)

	task := &model.Task{ID: "coord-task-1", Name: "coord", Status: model.StatusInProgress, Project: "test"}
	testutil.NoError(t, database.Add(task))
	testutil.NoError(t, database.SetMeta(task.ID, db.HeraMetaNamespace, db.HeraMetaKeyRole, string(db.HeraKindCoordinator)))

	kind, err := resolveRoleKindReal(task.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, kind, string(db.HeraKindCoordinator))

	testutil.NoError(t, stampContextSizeReal(task.ID, 12345))
	entries, err := database.ListMeta(task.ID, db.HeraMetaNamespace)
	testutil.NoError(t, err)
	var stamped string
	for _, e := range entries {
		if e.Key == db.HeraMetaKeyContextSize {
			stamped = e.Value
		}
	}
	testutil.Equal(t, stamped, "12345")

	budget, err := budgetReal(task.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, budget, 300000)

	// throttle-coord-hook-nudge: round-trip last_nudged_context_size and the
	// configured nudge increment through the same REST endpoints, mirroring
	// the context_size/budget round trips above.
	_, hadLastNudged, err := readLastNudgedContextSizeReal(task.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, hadLastNudged, false) // never stamped yet — absent-key branch

	testutil.NoError(t, stampLastNudgedContextSizeReal(task.ID, 54321))
	lastNudged, hadLastNudged, err := readLastNudgedContextSizeReal(task.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, hadLastNudged, true)
	testutil.Equal(t, lastNudged, 54321)

	nudgeIncrement, err := nudgeIncrementReal(task.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, nudgeIncrement, 50000)

	if _, err := resolveRoleKindReal("no-such-task"); err == nil {
		t.Fatal("expected an error resolving role kind for an unknown task id")
	}
}
