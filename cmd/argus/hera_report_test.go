package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/drn/argus/internal/testutil"
)

// --- `argus hera-report` Stop-hook subcommand (add-coordinator-context-management) ---
//
// These tests pin the `coordinator-context-management` delta spec's
// "Context-budget Stop hook stamps a live signal and nudges over budget"
// requirement. None of runHeraReport, heraReportEnv, or heraReportStdin exist
// yet (Stage 4 adds the `argus hera-report` subcommand) — this file fails to
// compile until then, proving the gap per tasks.md 1.3.
//
// The seams below (heraReportEnv's function fields) are the contract the
// Stage 4 implementation is expected to satisfy: gate on ARGUS_TASK_ID +
// resolved role kind BEFORE touching the transcript or the REST API, always
// stamp context_size for a coordinator regardless of budget, and emit a Stop
// hook "block" decision (Claude Code's hook contract: JSON on stdout) only
// when at/over budget. Role resolution, transcript tailing, task_meta
// stamping, and budget lookup are injected via heraReportEnv rather than
// wired to the real daemon/REST/DB, matching this repo's function-field
// injection convention (context/knowledge/testing.md) so the gating and
// nudge-recurrence logic can be unit tested without a live daemon.
//
// heraReportEnv, heraReportStdin, and runHeraReport's exact shape are a
// reasonable proposal, not a mandate — Stage 4 may adjust the seam as long as
// the five scenarios below (no-op on missing ARGUS_TASK_ID, no-op on
// non-coordinator, unconditional stamp, over-budget nudge, nudge
// recurrence/stop) keep passing.

// fakeHeraReportEnv builds a heraReportEnv backed by simple in-memory fakes,
// recording every call so tests can assert exactly what fired.
type fakeHeraReportEnv struct {
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
}

func (f *fakeHeraReportEnv) env() heraReportEnv {
	return heraReportEnv{
		Getenv: func(key string) string { return f.getenv[key] },
		ResolveRoleKind: func(taskID string) (string, error) {
			f.resolveCalled = true
			f.resolvedTaskID = taskID
			return f.roleKind, f.roleKindErr
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
	}
}

func stopHookStdin(transcriptPath string) *bytes.Reader {
	return bytes.NewReader([]byte(`{"transcript_path":"` + transcriptPath + `"}`))
}

// TestHeraReport_NoTaskID_NoOp pins "Non-hera session is a no-op": with no
// ARGUS_TASK_ID set, the hook must exit with no side effects — no role
// resolution, no transcript read, no task_meta write, no blocking decision.
func TestHeraReport_NoTaskID_NoOp(t *testing.T) {
	f := &fakeHeraReportEnv{getenv: map[string]string{}}
	var out, errOut bytes.Buffer

	runHeraReport(stopHookStdin("/tmp/transcript.jsonl"), &out, &errOut, f.env())

	testutil.Equal(t, f.resolveCalled, false)
	testutil.Equal(t, f.readCalled, false)
	testutil.Equal(t, f.stampCalled, false)
	testutil.Equal(t, f.budgetCalled, false)
	if strings.Contains(out.String(), "block") {
		t.Errorf("no-op path must not emit a block decision; got stdout=%q", out.String())
	}
}

// TestHeraReport_NonCoordinatorRole_NoOp pins "Worker session is a no-op":
// with ARGUS_TASK_ID set but the bound role resolving to a non-coordinator
// kind, the hook must exit with no side effects.
func TestHeraReport_NonCoordinatorRole_NoOp(t *testing.T) {
	f := &fakeHeraReportEnv{
		getenv:   map[string]string{"ARGUS_TASK_ID": "task-1"},
		roleKind: "worker",
	}
	var out, errOut bytes.Buffer

	runHeraReport(stopHookStdin("/tmp/transcript.jsonl"), &out, &errOut, f.env())

	testutil.Equal(t, f.resolveCalled, true)
	testutil.Equal(t, f.readCalled, false)
	testutil.Equal(t, f.stampCalled, false)
	if strings.Contains(out.String(), "block") {
		t.Errorf("worker no-op path must not emit a block decision; got stdout=%q", out.String())
	}
}

// TestHeraReport_Coordinator_AlwaysStampsContextSize pins "Context size is
// always stamped for a coordinator session": task_meta(hera, context_size)
// must be overwritten regardless of whether the budget is exceeded.
func TestHeraReport_Coordinator_AlwaysStampsContextSize(t *testing.T) {
	f := &fakeHeraReportEnv{
		getenv:      map[string]string{"ARGUS_TASK_ID": "task-1"},
		roleKind:    "coordinator",
		contextSize: 1000, // well under budget
		budget:      200000,
	}
	var out, errOut bytes.Buffer

	runHeraReport(stopHookStdin("/tmp/transcript.jsonl"), &out, &errOut, f.env())

	testutil.Equal(t, f.readCalled, true)
	testutil.Equal(t, f.readTranscript, "/tmp/transcript.jsonl")
	testutil.Equal(t, f.stampCalled, true)
	testutil.Equal(t, f.stampedTaskID, "task-1")
	testutil.Equal(t, f.stampedSize, 1000)
	if strings.Contains(out.String(), "block") {
		t.Errorf("under-budget coordinator must not emit a block decision; got stdout=%q", out.String())
	}
}

// TestHeraReport_Coordinator_OverBudget_EmitsNudge pins "Over-budget nudge
// repeats every turn until resolved" (first-turn half): at/over budget must
// emit a Stop hook block decision instructing the coordinator to reach a safe
// seam and recycle.
func TestHeraReport_Coordinator_OverBudget_EmitsNudge(t *testing.T) {
	f := &fakeHeraReportEnv{
		getenv:      map[string]string{"ARGUS_TASK_ID": "task-1"},
		roleKind:    "coordinator",
		contextSize: 250000,
		budget:      200000,
	}
	var out, errOut bytes.Buffer

	runHeraReport(stopHookStdin("/tmp/transcript.jsonl"), &out, &errOut, f.env())

	testutil.Equal(t, f.stampCalled, true)
	testutil.Equal(t, f.stampedSize, 250000)
	testutil.Contains(t, out.String(), "block")
	testutil.Contains(t, out.String(), "seam")
}

// TestHeraReport_OverBudgetNudge_RecursThenStops pins both "Over-budget nudge
// repeats every turn until resolved" (recurrence across two independent
// invocations, since each Stop event is a fresh process) and "Nudge stops the
// turn context drops back below budget".
func TestHeraReport_OverBudgetNudge_RecursThenStops(t *testing.T) {
	env := map[string]string{"ARGUS_TASK_ID": "task-1"}

	// Turn 1: over budget.
	f1 := &fakeHeraReportEnv{getenv: env, roleKind: "coordinator", contextSize: 250000, budget: 200000}
	var out1, errOut1 bytes.Buffer
	runHeraReport(stopHookStdin("/tmp/transcript.jsonl"), &out1, &errOut1, f1.env())
	testutil.Contains(t, out1.String(), "block")

	// Turn 2: still over budget — the nudge must recur (each hook invocation
	// re-evaluates fresh; nothing is remembered between processes).
	f2 := &fakeHeraReportEnv{getenv: env, roleKind: "coordinator", contextSize: 260000, budget: 200000}
	var out2, errOut2 bytes.Buffer
	runHeraReport(stopHookStdin("/tmp/transcript.jsonl"), &out2, &errOut2, f2.env())
	testutil.Contains(t, out2.String(), "block")

	// Turn 3: a recycle reset context_size back under budget — the nudge must
	// stop the very turn the condition becomes false.
	f3 := &fakeHeraReportEnv{getenv: env, roleKind: "coordinator", contextSize: 500, budget: 200000}
	var out3, errOut3 bytes.Buffer
	runHeraReport(stopHookStdin("/tmp/transcript.jsonl"), &out3, &errOut3, f3.env())
	if strings.Contains(out3.String(), "block") {
		t.Errorf("nudge must stop once context_size drops below budget; got stdout=%q", out3.String())
	}
	testutil.Equal(t, f3.stampCalled, true) // still stamps, just doesn't block
}
