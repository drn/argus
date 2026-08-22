package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"sync"
	"testing"
	"time"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/app/agentview"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/events"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/notify"
	"github.com/drn/argus/internal/testutil"
)

// blockedTail is a session-log tail that trips agent.DetectNeedsInput via its
// numbered-selection signature (❯ 1.). idleTail does not.
var (
	blockedTail = []byte("doing work\n❯ 1. Yes\n  2. No\n")
	idleTail    = []byte("just some streaming output, nothing to answer here")
	// workingQuestionTail is a BUSY agent whose narration ends in `?` AND that
	// still shows Claude's "working" affordance ("esc to interrupt"). It has NO
	// selection widget. The idle-gate-less content-stability pass MUST NOT flag
	// it even when its content is stable across ticks — the working-affordance
	// gate (BUG-035) is what keeps the BUG-032 false positive from returning.
	workingQuestionTail = []byte("⏺ Want me to ship it?\n\n✻ Cogitating… (12s · esc to interrupt)\n\n╭───╮\n│ > │\n╰───╯\n  ? for shortcuts\n")
	// awaitingQuestionTail is a parked agent at a FREE-TEXT question with NO
	// selection widget AND NO working affordance — genuinely awaiting input.
	// The content-stability pass MUST flag it once its content is stable
	// (BUG-035 GAP A: a fullscreen agent here never goes idle).
	awaitingQuestionTail = []byte("⏺ Want me to ship it?\n\n✻ Brewed for 12s\n\n╭───╮\n│ > │\n╰───╯\n  ? for shortcuts\n")
)

// defaultSizeOf is the size lookup the computeNeedsInput tests use: canned
// tails are formatted for a standard terminal, so 80×24 is fine. The emulated-
// screen fallback only fires when the raw regex misses; these tails match raw.
func defaultSizeOf(string) (int, int) { return 80, 24 }

// noInput / notArchived are the BUG-034 clear-disabled defaults for tests that
// don't exercise the clear path: no session has received input (zero time, so
// nothing ever advances past a baseline) and nothing is archived.
func noInput(string) time.Time { return time.Time{} }
func notArchived(string) bool  { return false }

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
		"working":  workingQuestionTail,
		"awaiting": awaitingQuestionTail,
	}
	tailOf := func(id string) []byte { return tails[id] }

	blockedFP := agent.ContentFingerprint(blockedTail)
	workingFP := agent.ContentFingerprint(workingQuestionTail)
	awaitingFP := agent.ContentFingerprint(awaitingQuestionTail)

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
			// Regression guard (coord feedback + BUG-035): a BUSY agent whose
			// last line ends in `?` but that still shows the "esc to interrupt"
			// working affordance must NOT be flagged by the idle-gate-less
			// stability pass, even when its content is stable across ticks. The
			// working-affordance-absent gate is what keeps this case safe.
			name:    "content-stable trailing-question WORKING agent not flagged",
			idle:    nil,
			running: []string{"working"},
			prev:    nil,
			prevFP:  map[string]uint64{"working": workingFP}, // stable since last tick
			want:    []string{},
		},
		{
			// BUG-035 GAP A: a never-idle agent parked at a FREE-TEXT question
			// with NO working affordance is genuinely awaiting input, so the
			// content-stability pass flags it once its content is stable.
			name:    "content-stable trailing-question AWAITING agent flagged",
			idle:    nil,
			running: []string{"awaiting"},
			prev:    nil,
			prevFP:  map[string]uint64{"awaiting": awaitingFP}, // stable since last tick
			want:    []string{"awaiting"},
		},
		{
			name:    "sticky: previously blocked task carried forward while still running",
			idle:    nil, // animation byte knocked it out of idle
			running: []string{"blocked"},
			prev:    []string{"blocked"},
			want:    []string{"blocked"},
		},
		{
			// BUG-061: the sticky pass no longer clears just because the marker
			// isn't in THIS tick's tail — a flat tail window can be permanently
			// flooded by Claude's blinking-cursor redraw long after the prompt
			// itself scrolled out of reach, which is indistinguishable from a
			// genuine answer at this layer. NeedsInputClear (exercised by
			// TestComputeNeedsInput_ClearOnInput) is now the only way this clears.
			name:    "sticky stays flagged even once the marker scrolls out of tail",
			idle:    nil,
			running: []string{"answered"},
			prev:    []string{"answered"},
			want:    []string{"answered"},
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
			got, gotFP, _, _, _, _ := computeNeedsInput(tc.idle, tc.running, tc.prev, tc.prevFP, nil, nil, nil, nil, tailOf, noInput, notArchived, &agent.ScreenRenderer{}, defaultSizeOf)
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

	screen := &agent.ScreenRenderer{}
	// Tick 1: not idle, no prior fingerprint → record only, do not flag.
	got1, fp1, _, _, _, _ := computeNeedsInput(nil, []string{"blocked"}, nil, nil, nil, nil, nil, nil, tailOf, noInput, notArchived, screen, defaultSizeOf)
	testutil.Equal(t, len(got1), 0)
	testutil.Equal(t, len(fp1), 1)

	// Tick 2: still not idle, content unchanged → flagged.
	got2, _, _, _, _, _ := computeNeedsInput(nil, []string{"blocked"}, nil, fp1, nil, nil, nil, nil, tailOf, noInput, notArchived, screen, defaultSizeOf)
	testutil.Equal(t, len(got2), 1)
	testutil.Equal(t, got2[0], "blocked")
}

// altScreenQuestionTail mirrors agent.altScreenQuestionFrame (unexported there):
// a FULLSCREEN (alt-screen) agent parked at a FREE-TEXT question — NO numbered
// selection widget, so it is invisible to the raw regex and only the emulated
// screen reveals the trailing `?` above the idle input prompt (BUG-035 GAP A).
// `working` toggles Claude's interrupt affordance ("esc to interrupt"): present
// ⇒ still generating (must NOT flag); absent ⇒ awaiting input (must flag).
func altScreenQuestionTail(secs, glyph string, working bool) []byte {
	spinner := glyph + " Brewed for " + secs
	if working {
		spinner = glyph + " Cogitating… (" + secs + " · esc to interrupt)"
	}
	return []byte("\x1b[?1049h\x1b[2J" +
		"\x1b[3;5H\x1b[38;2;200;200;200m⏺ Should I go ahead and ship it?\x1b[39m" +
		"\x1b[5;1H" + spinner +
		"\x1b[7;1H\x1b[38;2;177;185;249m❯ \x1b[39m" + // idle input prompt (❯ + NBSP)
		"\x1b[8;1H\x1b[?25l")
}

// TestComputeNeedsInput_AltScreenFreeTextQuestion drives BUG-035 GAP A through
// the REAL computeNeedsInput emulated-screen path across two ticks, BOTH
// directions: a fullscreen agent parked at a free-text question with NO working
// affordance is flagged once its content is stable; the same agent WHILE working
// (interrupt affordance present) is never flagged (the BUG-032 guard).
func TestComputeNeedsInput_AltScreenFreeTextQuestion(t *testing.T) {
	t.Run("awaiting (no working affordance) flagged on the stable tick", func(t *testing.T) {
		screen := &agent.ScreenRenderer{}
		// Each tick varies only the spinner chrome, so the emulated screen is
		// stable while the raw bytes differ.
		tailOf := func(string) []byte { return altScreenQuestionTail("3s", "✻", false) }

		// Tick 1: first observation — record fingerprint, do not flag.
		got1, fp1, _, _, _, _ := computeNeedsInput(nil, []string{"w"}, nil, nil, nil, nil, nil, nil, tailOf, noInput, notArchived, screen, defaultSizeOf)
		testutil.Equal(t, len(got1), 0)
		testutil.Equal(t, len(fp1), 1)

		// Tick 2: content stable (only spinner seconds changed) → flagged.
		tailOf = func(string) []byte { return altScreenQuestionTail("9s", "✶", false) }
		got2, _, _, _, _, _ := computeNeedsInput(nil, []string{"w"}, nil, fp1, nil, nil, nil, nil, tailOf, noInput, notArchived, screen, defaultSizeOf)
		testutil.DeepEqual(t, got2, []string{"w"})
	})

	t.Run("working (interrupt affordance present) never flagged", func(t *testing.T) {
		screen := &agent.ScreenRenderer{}
		tailOf := func(string) []byte { return altScreenQuestionTail("3s", "✻", true) }

		// Tick 1: working agent shows no awaiting-input signal → no fingerprint.
		got1, fp1, _, _, _, _ := computeNeedsInput(nil, []string{"w"}, nil, nil, nil, nil, nil, nil, tailOf, noInput, notArchived, screen, defaultSizeOf)
		testutil.Equal(t, len(got1), 0)
		testutil.Equal(t, len(fp1), 0) // gated out: not recorded

		// Tick 2: still working → still not flagged.
		got2, _, _, _, _, _ := computeNeedsInput(nil, []string{"w"}, nil, fp1, nil, nil, nil, nil, tailOf, noInput, notArchived, screen, defaultSizeOf)
		testutil.Equal(t, len(got2), 0)
	})
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

	// Tick 2 (BUG-061): the marker scrolls out of "a"'s tail (e.g. Claude's
	// blinking-cursor redraw flooded the window) but "a" is genuinely still
	// BUSY (never idle — the real flooding mechanism requires continuous byte
	// production, which is exactly what raw idleness means has NOT happened),
	// so the settlement pass (BUG-072) never engages, and no genuine user
	// input arrived — the sticky carry-forward pass no longer clears on a
	// tail miss alone, only NeedsInputClear (real input, archive, sustained
	// resumed activity, or genuine settlement) does. "a" stays flagged and no
	// new event fires (steady state).
	tails["a"] = idleTail
	srv.detectNeedsInputTick(state, []string{"a", "b"}, []string{"b"}, tailOf)
	testutil.Equal(t, srv.runner.(*agent.Runner).NeedsInput("a"), true)
	testutil.Equal(t, len(sink.events()), 1)

	// Tick 3: steady state, nothing blocked → no new events.
	srv.detectNeedsInputTick(state, []string{"a", "b"}, []string{"b"}, tailOf)
	testutil.Equal(t, len(sink.events()), 1)
}

// TestDetectNeedsInputTick_SettledActivityClears reproduces BUG-072 through the
// REAL daemon entry point: a flag raised on an idle-gated selection prompt is
// never answered by the user and never sustains agent.NeedsInputResumeTicks of
// working — it simply goes genuinely idle with the tail no longer showing any
// blocking signal, for agent.NeedsInputSettleTicks consecutive ticks. This is
// the exact live repro: the task has already settled (e.g. rolled to
// in_review) with nothing further needed, but nothing had ever cleared the
// flag until now.
func TestDetectNeedsInputTick_SettledActivityClears(t *testing.T) {
	srv, _ := testServer(t)
	close(srv.stopCh)

	tails := map[string][]byte{"a": blockedTail}
	tailOf := func(id string) []byte { return tails[id] }
	state := newIdleWatcherState()

	// Tick 1: idle + blocked → flagged.
	srv.detectNeedsInputTick(state, []string{"a"}, []string{"a"}, tailOf)
	testutil.Equal(t, srv.runner.(*agent.Runner).NeedsInput("a"), true)

	// The worker resolves its own block and goes idle with no blocking signal
	// showing — but for FEWER than NeedsInputSettleTicks consecutive ticks, so
	// it must not clear yet.
	tails["a"] = idleTail
	for i := 0; i < agent.NeedsInputSettleTicks-1; i++ {
		srv.detectNeedsInputTick(state, []string{"a"}, []string{"a"}, tailOf)
		if !srv.runner.(*agent.Runner).NeedsInput("a") {
			t.Fatalf("cleared too early, before sustaining %d settled ticks (tick %d)", agent.NeedsInputSettleTicks, i+1)
		}
	}
	// The Nth consecutive settled tick clears it.
	srv.detectNeedsInputTick(state, []string{"a"}, []string{"a"}, tailOf)
	testutil.Equal(t, srv.runner.(*agent.Runner).NeedsInput("a"), false)
}

// TestDetectNeedsInputTick_StillBlockedIdleDoesNotSettle guards the BUG-072
// regression this fix must not introduce: an idle session whose tail STILL
// shows the identical blocking signal must never be cleared by the settlement
// pass, however many consecutive idle ticks elapse — it is indistinguishable,
// by design, from the ordinary still-genuinely-blocked case.
func TestDetectNeedsInputTick_StillBlockedIdleDoesNotSettle(t *testing.T) {
	srv, _ := testServer(t)
	close(srv.stopCh)

	tailOf := func(string) []byte { return blockedTail }
	state := newIdleWatcherState()

	for i := 0; i < agent.NeedsInputSettleTicks+3; i++ {
		srv.detectNeedsInputTick(state, []string{"a"}, []string{"a"}, tailOf)
		if !srv.runner.(*agent.Runner).NeedsInput("a") {
			t.Fatalf("BUG-072 REGRESSION: a still-blocked idle session settled and cleared on tick %d", i+1)
		}
	}
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

// TestComputeNeedsInput_ClearOnInput covers BUG-034: a task flagged via the
// trailing-question heuristic clears once the user delivers input to that
// session, even though the question still matches in the tail, and input to a
// different session does not clear it.
func TestComputeNeedsInput_ClearOnInput(t *testing.T) {
	t0 := time.Unix(1000, 0)
	t1 := time.Unix(2000, 0)
	// Both sessions sit idle showing a numbered selection prompt (always flagged
	// by the idle pass), so the only variable is the clear filter.
	tailOf := func(string) []byte { return blockedTail }
	screen := &agent.ScreenRenderer{}

	// Tick 1: no input yet (t0 predates the flag) → both flagged, baselines t0.
	lastInput := map[string]time.Time{"a": t0, "b": t0}
	lastInputOf := func(id string) time.Time { return lastInput[id] }
	got1, _, since1, cleared1, _, _ := computeNeedsInput([]string{"a", "b"}, []string{"a", "b"}, nil, nil, nil, nil, nil, nil, tailOf, lastInputOf, notArchived, screen, defaultSizeOf)
	testutil.Equal(t, len(got1), 2)

	// Tick 2: user responds to "a" only (advances past its baseline). "a" clears
	// despite the stale prompt still in the tail; "b" stays flagged.
	lastInput["a"] = t1
	got2, _, _, _, _, _ := computeNeedsInput([]string{"a", "b"}, []string{"a", "b"}, got1, nil, since1, cleared1, nil, nil, tailOf, lastInputOf, notArchived, screen, defaultSizeOf)
	gotSet := map[string]bool{}
	for _, id := range got2 {
		gotSet[id] = true
	}
	testutil.DeepEqual(t, gotSet, map[string]bool{"b": true})
}

// TestComputeNeedsInput_BUG063_StaleReflagDoesNotReStick reproduces the exact
// race through the REAL computeNeedsInput: a task clears on genuine user
// input, then — after a gap tick with no candidacy at all while the session
// stays running — a later tick re-presents the SAME already-answered prompt
// content with no new input. Before the fix, the gap tick would have
// forgotten the task's baseline entirely, so the stale re-candidacy would
// recapture baseline == lastInputOf(id) and never clear again.
func TestComputeNeedsInput_BUG063_StaleReflagDoesNotReStick(t *testing.T) {
	t0 := time.Unix(1000, 0)
	t1 := time.Unix(2000, 0)
	t2 := time.Unix(3000, 0)
	screen := &agent.ScreenRenderer{}

	lastInput := t0
	lastInputOf := func(string) time.Time { return lastInput }

	var tail []byte
	tailOf := func(string) []byte { return tail }

	// Tick 1: idle on a selection prompt, no input since → flagged.
	tail = blockedTail
	got1, fp1, since1, cleared1, _, _ := computeNeedsInput([]string{"a"}, []string{"a"}, nil, nil, nil, nil, nil, nil, tailOf, lastInputOf, notArchived, screen, defaultSizeOf)
	testutil.DeepEqual(t, got1, []string{"a"})

	// Tick 2: user responds (lastInputOf advances past baseline). The stale
	// prompt is STILL in the tail (unchanged) — must clear anyway.
	lastInput = t1
	got2, fp2, since2, cleared2, _, _ := computeNeedsInput([]string{"a"}, []string{"a"}, got1, fp1, since1, cleared1, nil, nil, tailOf, lastInputOf, notArchived, screen, defaultSizeOf)
	testutil.Equal(t, len(got2), 0)

	// Tick 3: a genuine gap — the tail shows plain, non-blocking output, so
	// neither the idle-gated pass nor the content-fingerprint pass sees any
	// signal at all. The session stays running throughout.
	tail = idleTail
	got3, fp3, since3, cleared3, _, _ := computeNeedsInput(nil, []string{"a"}, got2, fp2, since2, cleared2, nil, nil, tailOf, lastInputOf, notArchived, screen, defaultSizeOf)
	testutil.Equal(t, len(got3), 0)

	// Tick 4: the tail reverts to the EXACT SAME already-answered prompt (a
	// stale re-detection), with no new input since t1. Must NOT re-stick.
	tail = blockedTail
	got4, fp4, since4, cleared4, _, _ := computeNeedsInput([]string{"a"}, []string{"a"}, got3, fp3, since3, cleared3, nil, nil, tailOf, lastInputOf, notArchived, screen, defaultSizeOf)
	if len(got4) != 0 {
		t.Fatalf("BUG-063 REGRESSION: stale re-candidacy at the same input timestamp re-stuck the flag: %v", got4)
	}

	// Stays clear across further stale re-candidacies too.
	got, fp, since, cleared := got4, fp4, since4, cleared4
	for i := 0; i < 3; i++ {
		got, fp, since, cleared, _, _ = computeNeedsInput([]string{"a"}, []string{"a"}, got, fp, since, cleared, nil, nil, tailOf, lastInputOf, notArchived, screen, defaultSizeOf)
		if len(got) != 0 {
			t.Fatalf("BUG-063 REGRESSION: flag re-stuck on a later tick: %v", got)
		}
	}

	// A genuinely newer input finally arrives → re-arms normally.
	lastInput = t2
	got, _, _, _, _, _ = computeNeedsInput([]string{"a"}, []string{"a"}, got, fp, since, cleared, nil, nil, tailOf, lastInputOf, notArchived, screen, defaultSizeOf)
	testutil.DeepEqual(t, got, []string{"a"})
}

// TestComputeNeedsInput_ResumedActivityClears reproduces the live hera-worker
// bug: a coordinator relays the human's real answer via reliable-notify
// delivery (WriteInputSystem), which never advances LastUserInput — so
// lastInputOf never progresses past the flag's baseline, and the BUG-034
// clear-on-input path can never fire — yet the worker demonstrably resumes
// real work (Claude's "working" affordance, sustained across ticks). The flag
// must clear via the resumed-activity pass alone.
func TestComputeNeedsInput_ResumedActivityClears(t *testing.T) {
	t0 := time.Unix(1000, 0)
	screen := &agent.ScreenRenderer{}

	// lastInputOf never advances past t0 for the whole scenario — simulating a
	// coordinator's relayed answer, which never counts as user input for the
	// BUG-034 clear filter.
	lastInputOf := func(string) time.Time { return t0 }

	var tail []byte
	tailOf := func(string) []byte { return tail }

	// Tick 1: idle on the selection prompt → flagged.
	tail = blockedTail
	got, fp, since, cleared, resume, _ := computeNeedsInput([]string{"a"}, []string{"a"}, nil, nil, nil, nil, nil, nil, tailOf, lastInputOf, notArchived, screen, defaultSizeOf)
	testutil.DeepEqual(t, got, []string{"a"})

	// The worker resumes real work: it is no longer idle, and its tail now
	// shows Claude's "working" affordance, sustained across several ticks.
	// lastInputOf never advances — only sustained activity can clear this.
	tail = workingQuestionTail
	for i := 0; i < agent.NeedsInputResumeTicks-1; i++ {
		got, fp, since, cleared, resume, _ = computeNeedsInput(nil, []string{"a"}, got, fp, since, cleared, resume, nil, tailOf, lastInputOf, notArchived, screen, defaultSizeOf)
		if len(got) == 0 {
			t.Fatalf("cleared too early, before sustaining %d working ticks (tick %d)", agent.NeedsInputResumeTicks, i+1)
		}
	}
	// The Nth consecutive working tick clears it.
	got, _, _, _, _, _ = computeNeedsInput(nil, []string{"a"}, got, fp, since, cleared, resume, nil, tailOf, lastInputOf, notArchived, screen, defaultSizeOf)
	if len(got) != 0 {
		t.Fatalf("expected the resumed-activity pass to clear the flag after %d sustained working ticks, got %v", agent.NeedsInputResumeTicks, got)
	}
}

// TestComputeNeedsInput_ResumedActivityBriefBurstDoesNotClear guards the
// BUG-034 regression this fix must not reintroduce: a coordinator's relayed
// message that produces only a BRIEF working burst (fewer than
// agent.NeedsInputResumeTicks consecutive ticks) before the worker re-parks at
// the EXACT SAME blocking prompt must not clear the flag — an unrelated system
// nudge to a genuinely still-parked agent must stay flagged; only SUSTAINED
// resumed activity clears it.
func TestComputeNeedsInput_ResumedActivityBriefBurstDoesNotClear(t *testing.T) {
	t0 := time.Unix(1000, 0)
	screen := &agent.ScreenRenderer{}
	lastInputOf := func(string) time.Time { return t0 } // never advances

	var tail []byte
	tailOf := func(string) []byte { return tail }

	tail = blockedTail
	got, fp, since, cleared, resume, _ := computeNeedsInput([]string{"a"}, []string{"a"}, nil, nil, nil, nil, nil, nil, tailOf, lastInputOf, notArchived, screen, defaultSizeOf)
	testutil.DeepEqual(t, got, []string{"a"})

	// A brief burst of working ticks — one short of the threshold — then it
	// re-parks at the identical blocking prompt.
	tail = workingQuestionTail
	for i := 0; i < agent.NeedsInputResumeTicks-2; i++ {
		got, fp, since, cleared, resume, _ = computeNeedsInput(nil, []string{"a"}, got, fp, since, cleared, resume, nil, tailOf, lastInputOf, notArchived, screen, defaultSizeOf)
		if len(got) == 0 {
			t.Fatalf("cleared too early, during the brief working burst (tick %d)", i+1)
		}
	}
	tail = blockedTail
	got, _, _, _, _, _ = computeNeedsInput([]string{"a"}, []string{"a"}, got, fp, since, cleared, resume, nil, tailOf, lastInputOf, notArchived, screen, defaultSizeOf)
	if len(got) != 1 || got[0] != "a" {
		t.Fatalf("BUG-034 REGRESSION: a brief working burst falsely cleared a still-parked agent, got %v", got)
	}
}

// askUserQuestionChooserTail mirrors Claude Code's AskUserQuestion tool-call
// widget: the chooser footer ("Enter to select ... Esc to cancel") with NO
// ❯ N. numbered-selection glyph, so it exercises needsInputChooserFooterRe
// specifically (not needsInputSelectionRe) — distinct from blockedTail's
// permission-check numbered-selection shape.
var askUserQuestionChooserTail = []byte("⏺ Which approach should I take?\n\n  1) Approach A\n  2) Approach B\n\nEnter to select · ↑/↓ to navigate · Esc to cancel\n")

// TestComputeNeedsInput_ResumedActivityClears_ChooserFooter verifies the
// resumed-activity clear path (BUG-065) also resolves a flag raised by
// Claude's AskUserQuestion tool-call chooser widget, not just the
// permission-check numbered-selection shape
// TestComputeNeedsInput_ResumedActivityClears already covers. The clear
// mechanism (agent.ResumeActivityTick, fed purely by the "working" affordance)
// is entry-heuristic-agnostic by construction, but this pins that guarantee
// with an explicit repro rather than relying on the reasoning alone.
func TestComputeNeedsInput_ResumedActivityClears_ChooserFooter(t *testing.T) {
	t0 := time.Unix(1000, 0)
	screen := &agent.ScreenRenderer{}
	lastInputOf := func(string) time.Time { return t0 } // frozen: models WriteInputSystem-only delivery

	var tail []byte
	tailOf := func(string) []byte { return tail }

	tail = askUserQuestionChooserTail
	got, fp, since, cleared, resume, _ := computeNeedsInput([]string{"a"}, []string{"a"}, nil, nil, nil, nil, nil, nil, tailOf, lastInputOf, notArchived, screen, defaultSizeOf)
	testutil.DeepEqual(t, got, []string{"a"})

	tail = workingQuestionTail
	for i := 0; i < agent.NeedsInputResumeTicks-1; i++ {
		got, fp, since, cleared, resume, _ = computeNeedsInput(nil, []string{"a"}, got, fp, since, cleared, resume, nil, tailOf, lastInputOf, notArchived, screen, defaultSizeOf)
		if len(got) == 0 {
			t.Fatalf("cleared too early, before sustaining %d working ticks (tick %d)", agent.NeedsInputResumeTicks, i+1)
		}
	}
	got, _, _, _, _, _ = computeNeedsInput(nil, []string{"a"}, got, fp, since, cleared, resume, nil, tailOf, lastInputOf, notArchived, screen, defaultSizeOf)
	if len(got) != 0 {
		t.Fatalf("expected the resumed-activity pass to clear a flag raised by an AskUserQuestion chooser after %d sustained working ticks, got %v", agent.NeedsInputResumeTicks, got)
	}
}

// TestComputeNeedsInput_ResumedActivityBriefBurstDoesNotClear_ChooserFooter
// mirrors TestComputeNeedsInput_ResumedActivityBriefBurstDoesNotClear but for
// a flag raised by an AskUserQuestion chooser — the BUG-034 regression guard
// must apply uniformly regardless of which entry shape raised the flag.
func TestComputeNeedsInput_ResumedActivityBriefBurstDoesNotClear_ChooserFooter(t *testing.T) {
	t0 := time.Unix(1000, 0)
	screen := &agent.ScreenRenderer{}
	lastInputOf := func(string) time.Time { return t0 }

	var tail []byte
	tailOf := func(string) []byte { return tail }

	tail = askUserQuestionChooserTail
	got, fp, since, cleared, resume, _ := computeNeedsInput([]string{"a"}, []string{"a"}, nil, nil, nil, nil, nil, nil, tailOf, lastInputOf, notArchived, screen, defaultSizeOf)
	testutil.DeepEqual(t, got, []string{"a"})

	tail = workingQuestionTail
	for i := 0; i < agent.NeedsInputResumeTicks-2; i++ {
		got, fp, since, cleared, resume, _ = computeNeedsInput(nil, []string{"a"}, got, fp, since, cleared, resume, nil, tailOf, lastInputOf, notArchived, screen, defaultSizeOf)
		if len(got) == 0 {
			t.Fatalf("cleared too early, during the brief working burst (tick %d)", i+1)
		}
	}
	tail = askUserQuestionChooserTail
	got, _, _, _, _, _ = computeNeedsInput([]string{"a"}, []string{"a"}, got, fp, since, cleared, resume, nil, tailOf, lastInputOf, notArchived, screen, defaultSizeOf)
	if len(got) != 1 || got[0] != "a" {
		t.Fatalf("BUG-034 REGRESSION: a brief working burst falsely cleared a still-parked AskUserQuestion prompt, got %v", got)
	}
}

// TestComputeNeedsInput_ResumedActivityClears_FreeTextQuestion verifies the
// resumed-activity clear path (BUG-065) also resolves a flag raised by a
// free-text trailing question (no selection widget at all — the shape a
// worker's plain conversational check-in uses, e.g. "awaiting go"), distinct
// from any structured tool-call widget.
func TestComputeNeedsInput_ResumedActivityClears_FreeTextQuestion(t *testing.T) {
	t0 := time.Unix(1000, 0)
	screen := &agent.ScreenRenderer{}
	lastInputOf := func(string) time.Time { return t0 } // frozen: models WriteInputSystem-only delivery

	var tail []byte
	tailOf := func(string) []byte { return tail }

	// The content-stability pass needs the SAME fingerprint across two
	// consecutive ticks to flag a never-idle free-text question (BUG-032).
	tail = awaitingQuestionTail
	_, fp, since, cleared, resume, _ := computeNeedsInput(nil, []string{"a"}, nil, nil, nil, nil, nil, nil, tailOf, lastInputOf, notArchived, screen, defaultSizeOf)
	got, fp, since, cleared, resume, _ := computeNeedsInput(nil, []string{"a"}, nil, fp, since, cleared, resume, nil, tailOf, lastInputOf, notArchived, screen, defaultSizeOf)
	testutil.DeepEqual(t, got, []string{"a"})

	tail = workingQuestionTail
	for i := 0; i < agent.NeedsInputResumeTicks-1; i++ {
		got, fp, since, cleared, resume, _ = computeNeedsInput(nil, []string{"a"}, got, fp, since, cleared, resume, nil, tailOf, lastInputOf, notArchived, screen, defaultSizeOf)
		if len(got) == 0 {
			t.Fatalf("cleared too early, before sustaining %d working ticks (tick %d)", agent.NeedsInputResumeTicks, i+1)
		}
	}
	got, _, _, _, _, _ = computeNeedsInput(nil, []string{"a"}, got, fp, since, cleared, resume, nil, tailOf, lastInputOf, notArchived, screen, defaultSizeOf)
	if len(got) != 0 {
		t.Fatalf("expected the resumed-activity pass to clear a flag raised by a free-text question after %d sustained working ticks, got %v", agent.NeedsInputResumeTicks, got)
	}
}

// TestComputeNeedsInput_SettledActivityClears reproduces BUG-072: a worker
// resolves its own block and settles into idle FASTER than
// agent.NeedsInputResumeTicks consecutive ticks of visible work — too fast for
// the resumed-activity pass to ever fire (going idle drives workingNow false,
// resetting that streak, and an idle session never shows the working
// affordance again) — with no recorded user input either. Only the settlement
// pass (agent.SettleTick) can resolve this.
func TestComputeNeedsInput_SettledActivityClears(t *testing.T) {
	t0 := time.Unix(1000, 0)
	screen := &agent.ScreenRenderer{}
	lastInputOf := func(string) time.Time { return t0 } // never advances

	var tail []byte
	idle := []string{"a"}
	tailOf := func(string) []byte { return tail }

	// Tick 1: idle on the selection prompt → flagged.
	tail = blockedTail
	got, fp, since, cleared, resume, settle := computeNeedsInput(idle, []string{"a"}, nil, nil, nil, nil, nil, nil, tailOf, lastInputOf, notArchived, screen, defaultSizeOf)
	testutil.DeepEqual(t, got, []string{"a"})

	// The worker resolves its own block and goes idle with the blocking signal
	// gone from the tail — for FEWER than agent.NeedsInputSettleTicks
	// consecutive ticks, so it must not clear yet.
	tail = idleTail
	for i := 0; i < agent.NeedsInputSettleTicks-1; i++ {
		got, fp, since, cleared, resume, settle = computeNeedsInput(idle, []string{"a"}, got, fp, since, cleared, resume, settle, tailOf, lastInputOf, notArchived, screen, defaultSizeOf)
		if len(got) == 0 {
			t.Fatalf("cleared too early, before sustaining %d settled ticks (tick %d)", agent.NeedsInputSettleTicks, i+1)
		}
	}
	// The Nth consecutive settled tick clears it.
	got, _, _, _, _, _ = computeNeedsInput(idle, []string{"a"}, got, fp, since, cleared, resume, settle, tailOf, lastInputOf, notArchived, screen, defaultSizeOf)
	if len(got) != 0 {
		t.Fatalf("expected the settlement pass to clear the flag after %d settled ticks, got %v", agent.NeedsInputSettleTicks, got)
	}
}

// TestComputeNeedsInput_StillBlockedIdleDoesNotSettle guards the BUG-072
// regression this fix must not introduce: an idle session whose tail STILL
// shows the identical blocking signal must never be cleared by the settlement
// pass, however many consecutive idle ticks elapse.
func TestComputeNeedsInput_StillBlockedIdleDoesNotSettle(t *testing.T) {
	screen := &agent.ScreenRenderer{}
	idle := []string{"a"}
	tailOf := func(string) []byte { return blockedTail }

	var got []string
	var fp map[string]uint64
	var since map[string]time.Time
	var cleared map[string]agent.ClearedMarker
	var resume, settle map[string]int
	for i := 0; i < agent.NeedsInputSettleTicks+3; i++ {
		got, fp, since, cleared, resume, settle = computeNeedsInput(idle, []string{"a"}, got, fp, since, cleared, resume, settle, tailOf, noInput, notArchived, screen, defaultSizeOf)
		if len(got) != 1 || got[0] != "a" {
			t.Fatalf("BUG-072 REGRESSION: a still-blocked idle session settled and cleared on tick %d: %v", i+1, got)
		}
	}
}

// TestComputeNeedsInput_NotIdleNeverSettles guards against conflating "signal
// absent" with "settled": a session that keeps producing new output (never
// reaches genuine raw idleness) must never accumulate settlement credit,
// however long its tail shows no blocking signal.
func TestComputeNeedsInput_NotIdleNeverSettles(t *testing.T) {
	screen := &agent.ScreenRenderer{}
	tailOf := func(string) []byte { return idleTail }

	var got []string
	var fp map[string]uint64
	var since map[string]time.Time
	var cleared map[string]agent.ClearedMarker
	var resume, settle map[string]int
	for i := 0; i < agent.NeedsInputSettleTicks+3; i++ {
		got, fp, since, cleared, resume, settle = computeNeedsInput(nil, []string{"a"}, got, fp, since, cleared, resume, settle, tailOf, noInput, notArchived, screen, defaultSizeOf)
	}
	testutil.Equal(t, len(got), 0)
	testutil.Equal(t, settle["a"], 0)
}

// altScreenPromptTail mirrors agent.altScreenPromptFrame (unexported there): a
// fullscreen (alt-screen) selection prompt whose ❯ cursor is painted LAST, to
// the LEFT of "1." — so in raw byte order ❯ TRAILS "1." and DetectNeedsInput's
// raw regex misses. Only after vt emulation lines the glyphs up does it match,
// exercising the BUG-033 emulated-screen path the live hera workers hit.
func altScreenPromptTail(secs, glyph string) []byte {
	return []byte("\x1b[?1049h\x1b[2J" +
		"\x1b[1;1H" + glyph + " Brewed for " + secs +
		"\x1b[3;5H\x1b[38;2;200;200;200mDo you want to proceed?\x1b[39m" +
		"\x1b[5;5H1. Yes" +
		"\x1b[6;5H2. No" +
		"\x1b[5;3H\x1b[38;2;177;185;249m❯\x1b[39m" +
		"\x1b[8;1H\x1b[?25l")
}

// TestDetectNeedsInputTick_SystemDeliveryDoesNotClear is the BUG-034-regression
// repro through the REAL daemon entry point (detectNeedsInputTick → the real
// runner's per-session LastInput, the real Notifier delivery). It mirrors the
// live scenario: a freshly-spawned hera worker parks at a FULLSCREEN (alt-screen)
// permission prompt — detected only via the emulated-screen pass (BUG-033) — and
// is flagged (?) autonomously (BUG-032/033). The coordinator then reliably
// delivers a message to the (idle) worker. That delivery is a SYSTEM write
// (Ctrl+U + text + CR), NOT the user answering the prompt, so it MUST NOT clear
// the (?). Before the fix, the clear-on-input filter read Session.LastInput(),
// which the notify delivery advanced — so the (?) was wrongly cleared.
func TestDetectNeedsInputTick_SystemDeliveryDoesNotClear(t *testing.T) {
	if testing.Short() {
		t.Skip("starts a real PTY session")
	}
	t.Setenv("HOME", t.TempDir())
	srv, d := testServer(t)
	close(srv.stopCh) // silence the background watcher so it can't race our ticks

	rnr := srv.runner.(*agent.Runner)
	// A real session that emits a little output then idles, so the Notifier's
	// idle gate opens (a session with no output ever is treated as "still
	// starting up", never idle). Its PTY master accepts WriteInput. This is the
	// freshly-spawned worker whose only "input" was the CLI-arg prompt — no user
	// keystroke yet, so LastInput / LastUserInput are both zero.
	sess, err := agent.StartSession("w", exec.Command("sh", "-c", "echo ready; sleep 60"), 24, 80)
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = sess.Stop() })
	rnr.SetSessionForTest("w", sess)

	// Wait for the session to settle into idle (output quiesced past the idle
	// threshold) so the Notifier will deliver.
	idleDeadline := time.Now().Add(8 * time.Second)
	for !sess.IsIdle() {
		if time.Now().After(idleDeadline) {
			t.Fatal("session never went idle")
		}
		time.Sleep(50 * time.Millisecond)
	}

	testutil.NoError(t, d.Add(&model.Task{ID: "w", Name: "w", Status: model.StatusInProgress, Project: "p"}))

	altTail := altScreenPromptTail("3s", "✻")
	tailOf := func(id string) []byte {
		if id == "w" {
			return altTail
		}
		return nil
	}

	state := newIdleWatcherState()

	// Tick 1: parked at the alt-screen prompt → flagged autonomously (no pane
	// open). This is the BUG-032/033 behavior that must keep working.
	srv.detectNeedsInputTick(state, []string{"w"}, []string{"w"}, tailOf)
	testutil.Equal(t, rnr.NeedsInput("w"), true)

	// The coordinator reliably delivers a message to the idle, unfocused worker.
	// processOne writes Ctrl+U + text + CR through the session — a SYSTEM write,
	// not the user responding to the prompt.
	ft := notify.NewFocusTracker(nil)
	n := notify.New(notify.AdaptRunner(func(id string) notify.SessionHandleIface {
		return rnr.Get(id)
	}), ft)
	n.ReliableNotify("w", "coordinator: how's it going?", "msg-1", notify.NotifyOpts{})
	n.Reconcile(time.Now())

	// Tick 2: still parked at the same prompt. The system delivery must NOT have
	// cleared the (?) — the user never answered.
	srv.detectNeedsInputTick(state, []string{"w"}, []string{"w"}, tailOf)
	testutil.Equal(t, rnr.NeedsInput("w"), true)
}

// TestDetectNeedsInputTick_UserInputClears is the other direction through the
// REAL path: an alt-screen-parked worker is flagged (?), then a genuine USER
// keystroke (WriteInput) on that session clears it — BUG-034's intended
// behavior, which the fix preserves. Together with
// TestDetectNeedsInputTick_SystemDeliveryDoesNotClear this proves the fix
// distinguishes user input (clears) from system delivery (does not).
func TestDetectNeedsInputTick_UserInputClears(t *testing.T) {
	if testing.Short() {
		t.Skip("starts a real PTY session")
	}
	t.Setenv("HOME", t.TempDir())
	srv, d := testServer(t)
	close(srv.stopCh)

	rnr := srv.runner.(*agent.Runner)
	sess, err := agent.StartSession("w", exec.Command("sleep", "60"), 24, 80)
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = sess.Stop() })
	rnr.SetSessionForTest("w", sess)
	testutil.NoError(t, d.Add(&model.Task{ID: "w", Name: "w", Status: model.StatusInProgress, Project: "p"}))

	altTail := altScreenPromptTail("3s", "✻")
	tailOf := func(id string) []byte {
		if id == "w" {
			return altTail
		}
		return nil
	}

	state := newIdleWatcherState()
	// Tick 1: parked → flagged.
	srv.detectNeedsInputTick(state, []string{"w"}, []string{"w"}, tailOf)
	testutil.Equal(t, rnr.NeedsInput("w"), true)

	// The user answers the prompt: a real keystroke through the agent pane.
	_, err = sess.WriteInput([]byte("1\r"), agentview.OriginUser)
	testutil.NoError(t, err)

	// Tick 2: user responded → cleared, even though the prompt still matches the
	// (injected) tail (the stale-tail crux BUG-034 targets).
	srv.detectNeedsInputTick(state, []string{"w"}, []string{"w"}, tailOf)
	testutil.Equal(t, rnr.NeedsInput("w"), false)
}

// TestComputeNeedsInput_ClearOnArchive covers BUG-034: an archived task is
// dropped from the set regardless of its detection signal.
func TestComputeNeedsInput_ClearOnArchive(t *testing.T) {
	tailOf := func(string) []byte { return blockedTail }
	archivedOf := func(id string) bool { return id == "a" }
	got, _, _, _, _, _ := computeNeedsInput([]string{"a", "b"}, []string{"a", "b"}, nil, nil, nil, nil, nil, nil, tailOf, noInput, archivedOf, &agent.ScreenRenderer{}, defaultSizeOf)
	testutil.DeepEqual(t, got, []string{"b"})
}

// mkBlockedHeraRole creates an orchestrator + worker role bound live to
// taskID, with hera_status already set to "blocked" — the fixture shared by
// the autoClearBlockedHeraRoles tests below. Returns the role's live status
// row (for its BlockedAt timestamp) and role ID.
func mkBlockedHeraRole(t *testing.T, d *db.DB, taskID string) *db.HeraRoleStatus {
	t.Helper()
	o, err := d.CreateHeraOrchestrator("orch", "")
	testutil.NoError(t, err)
	role, err := d.CreateHeraRole(db.CreateHeraRoleInput{
		OrchestratorID: o.ID, Name: "worker", Kind: db.HeraKindWorker, ArgusProject: "proj",
	})
	testutil.NoError(t, err)
	_, err = d.CreateHeraBinding(db.CreateHeraBindingInput{
		RoleID: role.ID, ArgusTaskID: taskID, WorktreePath: "/w/" + taskID,
	})
	testutil.NoError(t, err)
	testutil.NoError(t, d.UpsertHeraRoleStatus(role.ID, db.HeraStatusBlocked))
	st, err := d.HeraRoleStatusFor(role.ID)
	testutil.NoError(t, err)
	return st
}

// TestAutoClearBlockedHeraRoles_DirectReply reproduces the exact live repro
// (root-cause-and-fix-a-live) daemon-side: a hera role marks itself "blocked"
// (e.g. awaiting a check-in), the user answers DIRECTLY in the pane — a real
// keystroke, advancing LastUserInput — and the agent's own follow-up reply is
// brief. hera_status must auto-clear back to "working" immediately: it must
// NOT depend on the resumed-activity threshold, since a brief acknowledgment
// reply may never sustain agent.NeedsInputResumeTicks of Claude's "working"
// affordance. Before this fix hera_status had no auto-clear at all —
// RoleView.needsInputOwn ORs it into the rail's "(?)" alongside the separate,
// auto-clearing PTY needs-input flag, so the glyph stayed lit forever with no
// TUI attached to run the TUI-side fix either.
func TestAutoClearBlockedHeraRoles_DirectReply(t *testing.T) {
	srv, d := testServer(t)
	st := mkBlockedHeraRole(t, d, "a")

	lastUserInput := st.UpdatedAt.Add(-time.Second)
	lastUserInputOf := func(string) time.Time { return lastUserInput }
	tailOf := func(string) []byte { return nil }
	state := newIdleWatcherState()

	// Before the user replies: still blocked.
	srv.autoClearBlockedHeraRoles(state, []string{"a"}, tailOf, lastUserInputOf)
	got, err := d.HeraRoleStatusFor(st.RoleID)
	testutil.NoError(t, err)
	testutil.Equal(t, got.Status, db.HeraStatusBlocked)

	// The user replies directly in the pane.
	lastUserInput = st.UpdatedAt.Add(time.Second)
	srv.autoClearBlockedHeraRoles(state, []string{"a"}, tailOf, lastUserInputOf)
	got, err = d.HeraRoleStatusFor(st.RoleID)
	testutil.NoError(t, err)
	testutil.Equal(t, got.Status, db.HeraStatusWorking)
}

// TestAutoClearBlockedHeraRoles_ResumedActivityClears covers the symmetric
// BUG-065-style case daemon-side: the block was resolved via a coordinator-
// relayed answer (WriteInputSystem), which never advances LastUserInput, so
// the direct-reply condition can never fire — only sustained resumed activity
// can clear it.
func TestAutoClearBlockedHeraRoles_ResumedActivityClears(t *testing.T) {
	srv, d := testServer(t)
	st := mkBlockedHeraRole(t, d, "a")

	// lastUserInputOf never advances past the block — models a system-relayed
	// answer, not a direct keystroke.
	lastUserInputOf := func(string) time.Time { return st.UpdatedAt.Add(-time.Second) }
	tailOf := func(string) []byte { return workingQuestionTail }
	state := newIdleWatcherState()

	for i := 0; i < agent.NeedsInputResumeTicks-1; i++ {
		srv.autoClearBlockedHeraRoles(state, []string{"a"}, tailOf, lastUserInputOf)
		got, err := d.HeraRoleStatusFor(st.RoleID)
		testutil.NoError(t, err)
		if got.Status != db.HeraStatusBlocked {
			t.Fatalf("cleared too early, before sustaining %d working ticks (tick %d): %v", agent.NeedsInputResumeTicks, i+1, got.Status)
		}
	}
	srv.autoClearBlockedHeraRoles(state, []string{"a"}, tailOf, lastUserInputOf)
	got, err := d.HeraRoleStatusFor(st.RoleID)
	testutil.NoError(t, err)
	testutil.Equal(t, got.Status, db.HeraStatusWorking)
}

// TestAutoClearBlockedHeraRoles_NotRunningStaysBlocked guards against clearing
// a role whose bound task isn't in the running set at all (e.g. the session
// has exited) — no signal should be trusted for a task that isn't live.
func TestAutoClearBlockedHeraRoles_NotRunningStaysBlocked(t *testing.T) {
	srv, d := testServer(t)
	st := mkBlockedHeraRole(t, d, "a")

	lastUserInputOf := func(string) time.Time { return st.UpdatedAt.Add(time.Hour) }
	tailOf := func(string) []byte { return nil }
	state := newIdleWatcherState()

	srv.autoClearBlockedHeraRoles(state, nil, tailOf, lastUserInputOf)

	got, err := d.HeraRoleStatusFor(st.RoleID)
	testutil.NoError(t, err)
	testutil.Equal(t, got.Status, db.HeraStatusBlocked)
}

// TestAutoClearBlockedHeraRoles_NothingBlockedNoOp guards the common-case
// short-circuit: with no live blocked hera bindings at all, the pass must not
// error or touch state.
func TestAutoClearBlockedHeraRoles_NothingBlockedNoOp(t *testing.T) {
	srv, _ := testServer(t)
	state := newIdleWatcherState()
	srv.autoClearBlockedHeraRoles(state, []string{"a"}, func(string) []byte { return nil }, func(string) time.Time { return time.Time{} })
	testutil.Equal(t, len(state.heraBlockedResume), 0)
}

// TestComputeNeedsInput_BUG067_DistinctSequentialPromptReflagsAlongsideUnrelatedAutoClear
// reproduces the live repro (orchestrator "sketch-handoffs", roles
// 12a-blueprint-ui-lifecycle / 13a-blueprint-restore-version, 2026-07-21)
// daemon-side, AND directly tests the suspicion that the PR #904
// autoClearBlockedHeraRoles pass — which reuses the SAME shared
// idleWatcherState.screen ScreenRenderer as computeNeedsInput/detectNeedsInputTick
// — could corrupt an unrelated task's needs-input detection by rendering a
// DIFFERENT task's tail through it in the same tick. It does not: task "a"
// runs the BUG-067 sequential-distinct-prompt scenario through
// computeNeedsInput while task "b" (an unrelated hera role, self-reported
// blocked) is processed by autoClearBlockedHeraRoles in the SAME tick,
// sharing the SAME *agent.ScreenRenderer — task "a"'s detection must be
// unaffected by task "b"'s render, and vice versa.
func TestComputeNeedsInput_BUG067_DistinctSequentialPromptReflagsAlongsideUnrelatedAutoClear(t *testing.T) {
	srv, d := testServer(t)
	st := mkBlockedHeraRole(t, d, "b")

	q1 := []byte("Where should the four lifecycle affordances live in the UI?\n❯ 1. List-centric (card kebab menu)\n  2. Detail-page only\n")
	q2 := []byte("What should the 'New Blueprint' button do?\n❯ 1. Bare create, open viewer\n  2. Bare create + copy hint\n")

	aTail := q1
	tailOf := func(id string) []byte {
		if id == "a" {
			return aTail
		}
		return idleTail // task "b"'s own tail is irrelevant; it's driven via hera_status, not PTY content
	}
	t0 := time.Unix(1000, 0)
	t1 := time.Unix(2000, 0)
	aLastInput := t0
	lastInputOf := func(id string) time.Time {
		if id == "a" {
			return aLastInput
		}
		return time.Time{}
	}
	archivedOf := func(string) bool { return false }
	// "b" never receives a direct reply and never shows working — it must stay
	// blocked, untouched by whatever happens to task "a" in the same tick.
	bLastUserInputOf := func(string) time.Time { return st.UpdatedAt.Add(-time.Second) }

	state := newIdleWatcherState()
	running := []string{"a", "b"}

	tick := func() []string {
		needs, newFP, newSince, newCleared, newResume, newSettle := computeNeedsInput(
			[]string{"a"}, running, prevNeeds(state), state.contentFP, state.needsInputSince,
			state.needsInputCleared, state.needsInputResume, state.needsInputSettle, tailOf, lastInputOf, archivedOf,
			state.screen, defaultSizeOf,
		)
		state.contentFP, state.needsInputSince, state.needsInputCleared, state.needsInputResume, state.needsInputSettle = newFP, newSince, newCleared, newResume, newSettle
		state.needsInputNow = make(map[string]bool, len(needs))
		for _, id := range needs {
			state.needsInputNow[id] = true
		}
		srv.autoClearBlockedHeraRoles(state, running, tailOf, bLastUserInputOf)
		return needs
	}

	// Tick 1: question 1 shown for "a" → flagged. "b" stays blocked.
	got := tick()
	testutil.DeepEqual(t, got, []string{"a"})
	bStatus, err := d.HeraRoleStatusFor(st.RoleID)
	testutil.NoError(t, err)
	testutil.Equal(t, bStatus.Status, db.HeraStatusBlocked)

	// Tick 2: "a" is answered directly (lastInput advances past baseline) →
	// real clear. "b" is untouched by "a"'s render sharing the same screen.
	aLastInput = t1
	got = tick()
	testutil.Equal(t, len(got), 0)
	bStatus, err = d.HeraRoleStatusFor(st.RoleID)
	testutil.NoError(t, err)
	testutil.Equal(t, bStatus.Status, db.HeraStatusBlocked)

	// Tick 3: Claude asks "a" a DIFFERENT, still-unanswered question — no
	// further input has arrived (still t1, identical to the clear). Must
	// re-flag despite the unrelated auto-clear pass rendering "b" in the same
	// tick via the same ScreenRenderer.
	aTail = q2
	got = tick()
	if len(got) != 1 || got[0] != "a" {
		t.Fatalf("BUG-067 REGRESSION (shared renderer w/ unrelated auto-clear pass): distinct second prompt was suppressed: got %v", got)
	}
	bStatus, err = d.HeraRoleStatusFor(st.RoleID)
	testutil.NoError(t, err)
	testutil.Equal(t, bStatus.Status, db.HeraStatusBlocked)
}

// prevNeeds extracts the previous tick's needs-input set from state, mirroring
// how Server.detectNeedsInputTick builds it from state.needsInputNow.
func prevNeeds(state *idleWatcherState) []string {
	prev := make([]string, 0, len(state.needsInputNow))
	for id := range state.needsInputNow {
		prev = append(prev, id)
	}
	return prev
}
