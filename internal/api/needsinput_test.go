package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/events"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

// blockedTail is a session-log tail that trips agent.DetectNeedsInput via its
// numbered-selection signature (❯ 1.). idleTail does not.
var (
	blockedTail = []byte("doing work\n❯ 1. Yes\n  2. No\n")
	idleTail    = []byte("just some streaming output, nothing to answer here")
	// questionTail trips DetectNeedsInput via endsInQuestion (trailing `?`
	// above the input box) but has NO selection widget — DetectSelectionPrompt
	// is false. A busy agent showing this must NOT be flagged by the
	// idle-gate-less content-stability pass.
	questionTail = []byte("⏺ Want me to ship it?\n\n╭───╮\n│ > │\n╰───╯\n  ? for shortcuts\n")
)

// recordingSink captures emitted events for inspection. Local to the api test
// package (the events package's own recordingSink is unexported there).
type recordingSink struct {
	mu  sync.Mutex
	got []model.Event
}

func (r *recordingSink) Emit(ev model.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.got = append(r.got, ev)
}

func (r *recordingSink) events() []model.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]model.Event, len(r.got))
	copy(out, r.got)
	return out
}

// TestComputeNeedsInput covers the idle-gated detection plus the sticky
// carry-forward pass that keeps the flag from oscillating when Claude's prompt
// UI animation bytes briefly knock a blocked session out of the idle set.
func TestComputeNeedsInput(t *testing.T) {
	tails := map[string][]byte{
		"blocked":  blockedTail,
		"idle":     idleTail,
		"answered": idleTail, // marker scrolled out of the tail
		"question": questionTail,
	}
	tailOf := func(id string) []byte { return tails[id] }

	blockedFP := agent.ContentFingerprint(blockedTail)
	questionFP := agent.ContentFingerprint(questionTail)

	cases := []struct {
		name    string
		idle    []string
		running []string
		prev    []string
		prevFP  map[string]uint64
		want    []string
	}{
		{
			name:    "blocked idle task detected",
			idle:    []string{"blocked", "idle"},
			running: []string{"blocked", "idle"},
			want:    []string{"blocked"},
		},
		{
			name:    "not-idle blocked task ignored on first observation",
			idle:    nil, // streaming past, not idle this tick
			running: []string{"blocked"},
			prev:    nil,
			want:    []string{},
		},
		{
			// BUG-032: a never-idle session parked at a prompt (continuous
			// redraw/animation bytes) is flagged once its content fingerprint
			// is stable across ticks, even though it is not in the idle set.
			name:    "content-stable blocked task flagged though never idle",
			idle:    nil,
			running: []string{"blocked"},
			prev:    nil,
			prevFP:  map[string]uint64{"blocked": blockedFP},
			want:    []string{"blocked"},
		},
		{
			// Regression guard: a streaming agent flashing the marker has a
			// fingerprint that differs from last tick → not flagged.
			name:    "streaming task with shifting content not flagged",
			idle:    nil,
			running: []string{"blocked"},
			prev:    nil,
			prevFP:  map[string]uint64{"blocked": blockedFP + 1}, // last tick differed
			want:    []string{},
		},
		{
			// Regression guard (coord feedback): a busy agent whose last line
			// ends in `?` (endsInQuestion → DetectNeedsInput true) but has NO
			// selection widget must NOT be flagged by the idle-gate-less
			// stability pass, even when its content is stable across ticks.
			name:    "content-stable trailing-question working agent not flagged",
			idle:    nil,
			running: []string{"question"},
			prev:    nil,
			prevFP:  map[string]uint64{"question": questionFP}, // stable since last tick
			want:    []string{},
		},
		{
			name:    "sticky: previously blocked task carried forward while still running",
			idle:    nil, // animation byte knocked it out of idle
			running: []string{"blocked"},
			prev:    []string{"blocked"},
			want:    []string{"blocked"},
		},
		{
			name:    "sticky clears when marker scrolled out of tail",
			idle:    nil,
			running: []string{"answered"},
			prev:    []string{"answered"},
			want:    []string{},
		},
		{
			name:    "sticky clears when session no longer running",
			idle:    nil,
			running: nil, // session exited
			prev:    []string{"blocked"},
			want:    []string{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, gotFP := computeNeedsInput(tc.idle, tc.running, tc.prev, tc.prevFP, tailOf)
			gotSet := map[string]bool{}
			for _, id := range got {
				gotSet[id] = true
			}
			wantSet := map[string]bool{}
			for _, id := range tc.want {
				wantSet[id] = true
			}
			testutil.DeepEqual(t, gotSet, wantSet)
			// Every running task showing the signature must carry its
			// fingerprint forward so the next tick can compare.
			if _, ok := gotFP["blocked"]; !ok {
				for _, id := range tc.running {
					if id == "blocked" {
						t.Error("expected blocked task fingerprint carried forward")
					}
				}
			}
		})
	}
}

// TestComputeNeedsInput_StabilityAcrossTicks drives the content-stability pass
// across two real ticks: the first observation of a never-idle blocked session
// records its fingerprint but does not flag; the second, with content
// unchanged, flags it.
func TestComputeNeedsInput_StabilityAcrossTicks(t *testing.T) {
	tailOf := func(id string) []byte { return blockedTail }

	// Tick 1: not idle, no prior fingerprint → record only, do not flag.
	got1, fp1 := computeNeedsInput(nil, []string{"blocked"}, nil, nil, tailOf)
	testutil.Equal(t, len(got1), 0)
	testutil.Equal(t, len(fp1), 1)

	// Tick 2: still not idle, content unchanged → flagged.
	got2, _ := computeNeedsInput(nil, []string{"blocked"}, nil, fp1, tailOf)
	testutil.Equal(t, len(got2), 1)
	testutil.Equal(t, got2[0], "blocked")
}

// TestDetectNeedsInputTick drives the full watcher pass: it publishes the
// detected set onto the runner and emits session.needs_input on every
// enter/leave transition with the payload bool distinguishing the edge.
func TestDetectNeedsInputTick(t *testing.T) {
	srv, _ := testServer(t)
	// Stop the background idleWatcher goroutine so it can't race our manual
	// ticks by clearing the runner's needs-input set on its own 5s cadence.
	close(srv.stopCh)

	sink := &recordingSink{}
	prev := events.SetSink(sink)
	t.Cleanup(func() { events.SetSink(prev) })

	tails := map[string][]byte{"a": blockedTail, "b": idleTail}
	tailOf := func(id string) []byte { return tails[id] }

	state := newIdleWatcherState()

	// Tick 1: "a" is idle + blocked → enters needs-input; "b" idle but not
	// blocked → nothing.
	srv.detectNeedsInputTick(state, []string{"a", "b"}, []string{"a", "b"}, tailOf)
	testutil.Equal(t, srv.runner.(*agent.Runner).NeedsInput("a"), true)
	testutil.Equal(t, srv.runner.(*agent.Runner).NeedsInput("b"), false)

	ev := sink.events()
	testutil.Equal(t, len(ev), 1)
	testutil.Equal(t, ev[0].Type, model.EventTypeSessionNeedsInput)
	testutil.Equal(t, ev[0].TaskID, "a")
	var p1 map[string]bool
	testutil.NoError(t, json.Unmarshal(ev[0].Payload, &p1))
	testutil.Equal(t, p1["needs_input"], true)

	// Tick 2: "a" answered the prompt — marker gone from tail, still running.
	// No re-entry event (steady state was just cleared), one clear event.
	tails["a"] = idleTail
	srv.detectNeedsInputTick(state, []string{"a", "b"}, []string{"a", "b"}, tailOf)
	testutil.Equal(t, srv.runner.(*agent.Runner).NeedsInput("a"), false)

	ev = sink.events()
	testutil.Equal(t, len(ev), 2)
	testutil.Equal(t, ev[1].Type, model.EventTypeSessionNeedsInput)
	testutil.Equal(t, ev[1].TaskID, "a")
	var p2 map[string]bool
	testutil.NoError(t, json.Unmarshal(ev[1].Payload, &p2))
	testutil.Equal(t, p2["needs_input"], false)

	// Tick 3: steady state, nothing blocked → no new events.
	srv.detectNeedsInputTick(state, []string{"a", "b"}, []string{"a", "b"}, tailOf)
	testutil.Equal(t, len(sink.events()), 2)
}

// TestHandleListTasks_NeedsInput verifies the runner's needs-input set surfaces
// as the per-task needs_input field on GET /api/tasks, gated on in_progress.
func TestHandleListTasks_NeedsInput(t *testing.T) {
	srv, d := testServer(t)
	close(srv.stopCh) // silence background watcher
	mux := srv.routes()

	blocked := &model.Task{ID: "blocked", Name: "blocked", Status: model.StatusInProgress, Project: "p"}
	working := &model.Task{ID: "working", Name: "working", Status: model.StatusInProgress, Project: "p"}
	pending := &model.Task{ID: "pending", Name: "pending", Status: model.StatusPending, Project: "p"}
	testutil.NoError(t, d.Add(blocked))
	testutil.NoError(t, d.Add(working))
	testutil.NoError(t, d.Add(pending))

	// Pending is also flagged to prove the in_progress gate suppresses it.
	srv.runner.SetNeedsInputIDs([]string{"blocked", "pending"})

	req := authedReq("GET", "/api/tasks", "")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	testutil.Equal(t, w.Code, http.StatusOK)

	var resp map[string][]taskJSON
	testutil.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))

	byID := map[string]taskJSON{}
	for _, tj := range resp["tasks"] {
		byID[tj.ID] = tj
	}
	testutil.Equal(t, byID["blocked"].NeedsInput, true)
	testutil.Equal(t, byID["working"].NeedsInput, false)
	// Non-in_progress never reports needs_input even when in the set.
	testutil.Equal(t, byID["pending"].NeedsInput, false)
}

// TestComputeRuntimeState_NeedsInput pins the gate: needs_input is true only
// for in_progress tasks present in the set.
func TestComputeRuntimeState_NeedsInput(t *testing.T) {
	running := map[string]bool{"t1": true}
	idle := map[string]bool{"t1": true}
	needs := map[string]bool{"t1": true}

	inProg := &model.Task{ID: "t1", Status: model.StatusInProgress}
	testutil.Equal(t, computeRuntimeState(inProg, running, idle, needs).NeedsInput, true)

	// Same task id, non-in_progress status → never flagged.
	review := &model.Task{ID: "t1", Status: model.StatusInReview}
	testutil.Equal(t, computeRuntimeState(review, running, idle, needs).NeedsInput, false)

	// In set absent → false.
	other := &model.Task{ID: "t2", Status: model.StatusInProgress}
	testutil.Equal(t, computeRuntimeState(other, running, idle, needs).NeedsInput, false)
}
