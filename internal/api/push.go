package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/events"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/uxlog"
)

// pushSubscribeReq matches the W3C PushSubscription serialized shape.
type pushSubscribeReq struct {
	Label    string `json:"label"`
	Endpoint string `json:"endpoint"`
	Keys     struct {
		P256dh string `json:"p256dh"`
		Auth   string `json:"auth"`
	} `json:"keys"`
}

func (s *Server) handleVapidPublicKey(w http.ResponseWriter, r *http.Request) {
	if s.push == nil {
		writeErr(w, http.StatusServiceUnavailable, "push not available", nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"public_key": s.push.PublicKey()})
}

func (s *Server) handlePushSubscribe(w http.ResponseWriter, r *http.Request) {
	if s.push == nil {
		writeErr(w, http.StatusServiceUnavailable, "push not available", nil)
		return
	}
	var req pushSubscribeReq
	r.Body = http.MaxBytesReader(w, r.Body, 16*1024)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON", err)
		return
	}
	if req.Endpoint == "" || req.Keys.P256dh == "" || req.Keys.Auth == "" {
		writeErr(w, http.StatusBadRequest, "endpoint and keys required", nil)
		return
	}
	id, err := s.db.AddPushSubscription(db.PushSubscription{
		Label:    req.Label,
		Endpoint: req.Endpoint,
		P256dh:   req.Keys.P256dh,
		Auth:     req.Keys.Auth,
	})
	if err != nil {
		uxlog.Log("[push] subscribe failed: %v", err)
		writeErr(w, http.StatusInternalServerError, "", err)
		return
	}
	uxlog.Log("[push] subscribed id=%d label=%q", id, req.Label)
	writeJSON(w, http.StatusCreated, map[string]int64{"id": id})
}

func (s *Server) handlePushList(w http.ResponseWriter, r *http.Request) {
	subs, err := s.db.PushSubscriptions()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "", err)
		return
	}
	type subView struct {
		ID        int64  `json:"id"`
		Label     string `json:"label"`
		Endpoint  string `json:"endpoint_masked"`
		CreatedAt int64  `json:"created_at"`
	}
	out := make([]subView, 0, len(subs))
	for _, sub := range subs {
		ep := sub.Endpoint
		if len(ep) > 40 {
			ep = ep[:25] + "…" + ep[len(ep)-12:]
		}
		out = append(out, subView{
			ID:        sub.ID,
			Label:     sub.Label,
			Endpoint:  ep,
			CreatedAt: sub.CreatedAt.Unix(),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"subscriptions": out})
}

func (s *Server) handlePushUnsubscribe(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id", nil)
		return
	}
	if err := s.db.DeletePushSubscription(id); err != nil {
		writeErr(w, http.StatusNotFound, "", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int64{"deleted": id})
}

// handlePushTest sends a test notification to all registered devices.
// Useful for verifying the subscribe flow worked end-to-end. Single-tier
// auth: any authenticated token may trigger it (worst case is a test
// notification to your own devices — no RCE/credential risk).
func (s *Server) handlePushTest(w http.ResponseWriter, r *http.Request) {
	if s.push == nil {
		writeErr(w, http.StatusServiceUnavailable, "push not available", nil)
		return
	}
	uxlog.Log("[push] test push triggered")
	s.push.Notify("", "Argus test", "Push notifications are working", "")
	writeJSON(w, http.StatusOK, map[string]bool{"sent": true})
}

// idleWatcherState carries the per-task bookkeeping across idleWatcher ticks.
// Pulled out as a struct so idleWatcherTick can be exercised in unit tests
// without spinning up a real ticker.
type idleWatcherState struct {
	idleNow       map[string]bool      // taskID -> last seen idle?
	seenBefore    map[string]bool      // taskID -> have we observed this session on a prior tick?
	pushedAt      map[string]time.Time // taskID -> wall-clock time we last fired an idle push
	needsInputNow map[string]bool      // taskID -> last seen blocked-on-user-input?
	// contentFP carries each prompt-showing session's content fingerprint from
	// the previous tick (BUG-032). A session whose meaningful output is
	// unchanged across ticks while it shows the prompt signature is blocked
	// even if it never goes idle (continuous redraw/animation bytes).
	contentFP map[string]uint64
	// needsInputSince carries the clear-on-input baseline (BUG-034): the
	// session's last-input timestamp observed when a task first entered the
	// needs-input set. agent.NeedsInputClear freezes it across ticks and clears
	// the flag once the session's last-input advances past it (the user
	// responded), even while the stale question still matches in the tail.
	needsInputSince map[string]time.Time
	// needsInputCleared carries the BUG-063 cleared-marker map: the session's
	// last-input timestamp recorded at the moment a real clear fired, threaded
	// forward for every RUNNING task regardless of candidacy so a later stale
	// content-fingerprint re-flag of already-answered content at the same
	// timestamp cannot recapture a stuck baseline. Mirrors the TUI's
	// App.needsInputCleared.
	needsInputCleared map[string]time.Time
	// needsInputResume carries the resumed-activity escalation counter (see
	// agent.ResumeActivityTick): consecutive ticks a flagged session has shown
	// Claude's "working" affordance, independent of whether any input was ever
	// recorded as user-typed. Lets NeedsInputClear resolve a flag a
	// coordinator's relayed answer (WriteInputSystem) could otherwise never
	// clear. Mirrors the TUI's App.needsInputResume.
	needsInputResume map[string]int
	// screen reconstructs the visible terminal screen from a session's raw tail
	// so detection matches the EMULATED screen, catching fullscreen (alt-screen)
	// prompts whose cursor-addressed glyphs aren't linearly present in the bytes
	// (BUG-033). Reused across ticks (one drain goroutine for the watcher's
	// lifetime); the watcher is single-goroutine so no lock is needed.
	screen *agent.ScreenRenderer
	// contentIdle carries the content-aware idle bookkeeping (BUG-036): the
	// per-task emulated-screen fingerprint + stable-since timestamp used to
	// recognize a fullscreen (alt-screen) agent that is parked at its prompt and
	// so never reaches the raw-byte idle set. agent.ContentIdle folds the result
	// into the idle set used for idle-push / session.idle, so a parked fullscreen
	// agent fires one idle push when its content stabilizes.
	contentIdle *agent.ContentIdleState
}

func newIdleWatcherState() *idleWatcherState {
	return &idleWatcherState{
		idleNow:           make(map[string]bool),
		seenBefore:        make(map[string]bool),
		pushedAt:          make(map[string]time.Time),
		needsInputNow:     make(map[string]bool),
		contentFP:         make(map[string]uint64),
		needsInputSince:   make(map[string]time.Time),
		needsInputCleared: make(map[string]time.Time),
		needsInputResume:  make(map[string]int),
		screen:            &agent.ScreenRenderer{},
	}
}

// needsInputScanBytes is how many bytes of each session's recent PTY output
// the watcher feeds to agent.DetectNeedsInput. DetectNeedsInput truncates to
// its own tail window internally; this is the generous upper bound read from
// the ring. Matches the TUI's detectNeedsInputTailBytes.
const needsInputScanBytes = 16 * 1024

// sessionScreenSize returns the PTY dimensions a task's session was last sized
// at, for re-emulating its output to the screen the bytes were formatted for
// (BUG-033 alt-screen detection). Reads the persisted size sidecar
// (~/.argus/sessions/<id>.size) the daemon writes on every Start/Resize — a
// local file read, so it is non-blocking in both the in-process and supervisor-
// client runner cases (PTYSize() can round-trip to the supervisor). Falls back
// to the session default (80×24) when the sidecar is missing; agent.render
// applies the same default for non-positive dimensions.
func sessionScreenSize(taskID string) (cols, rows int) {
	if c, r, ok := agent.LoadSessionSize(taskID); ok {
		return c, r
	}
	return int(agent.DefaultTermCols), int(agent.DefaultTermRows)
}

// computeNeedsInput returns the set of task IDs whose recent PTY output
// indicates the agent is blocked waiting on the user, reusing the shared
// agent.DetectNeedsInput heuristic. It mirrors the TUI's detection
// (internal/tui/app.go detectNeedsInputSticky) so the daemon-published signal
// matches what the TUI renders. Three passes, each guarded against the
// false-positive risk of flagging a still-streaming agent:
//
//   - Idle pass: an idle session showing the prompt signature is blocked. The
//     idle gate alone rejects a streaming agent that flashes the marker text.
//   - Content-stability pass (BUG-032): a session that NEVER goes idle —
//     because it sits at a prompt emitting continuous redraw/animation bytes
//     (spinner, cursor blink, alt-screen repaint) — is blocked when the
//     signature is present AND its content fingerprint (agent.ContentFingerprint,
//     which strips that animation chrome) is unchanged from the previous tick.
//     A streaming agent's fingerprint changes every tick, so it is never
//     flagged here. prevFP supplies last tick's fingerprints; the returned map
//     carries this tick's forward.
//   - Sticky carry-forward: a previously-flagged task still running stays
//     flagged unconditionally (BUG-061) — it no longer re-requires a fresh
//     tail match, since a flat tail window can be permanently flooded by
//     Claude's blinking-cursor redraw long after the prompt itself scrolled
//     out of reach. NeedsInputClear below is the only way this flag clears.
//
// tailOf returns the recent output tail for a task (nil if unavailable);
// injected so the watcher reads the live session ring while tests supply
// canned bytes. agent.DetectNeedsInput treats nil/empty as "not blocked".
//
// Detection matches the EMULATED screen (BUG-033): the raw byte regex is the
// fast path (linear agents), and on a miss the tail is rendered through
// `screen` sized via sizeOf so a fullscreen (alt-screen) agent's cursor-
// addressed prompt is caught without a view-triggered repaint. A nil screen
// disables the fallback (raw-only), matching pre-BUG-033 behavior.
//
// After the three entry passes build the candidate set, a resumed-activity
// pass computes, for every running session, whether it has shown Claude's
// "working" affordance (agent.ContentIdleFingerprint's working return) for
// agent.NeedsInputResumeTicks consecutive ticks — independent of the
// candidate set, mirroring how the content-stability pass above scans every
// running session regardless of candidacy. This is what lets a hera
// coordinator's relayed answer (delivered via WriteInputSystem, which never
// advances lastUserInput — see agent.NeedsInputClear) resolve a flag once the
// worker demonstrably resumes real work, not just when the human types
// directly into the session.
//
// Then agent.NeedsInputClear applies the clear conditions: a task whose
// session received new input after the flag was raised (lastInputOf advanced
// past the baseline carried in prevSince), whose task is archived
// (archivedOf), or which has sustained resumed activity (resumedOf, fed by
// this pass) is dropped — deterministic, independent of the stale question
// scrolling out of the tail. It also applies the BUG-063 guard: runningIDs/
// prevCleared let a real clear's settled input timestamp survive ticks where a
// task isn't a candidate at all, so a LATER stale content-fingerprint re-flag
// at that same timestamp (nothing new having happened) cannot recapture a
// stuck baseline. The returned newSince/newCleared/newResume carry all three
// maps forward; nil lastInputOf/archivedOf degrade to pre-BUG-034 behavior (no
// clear).
func computeNeedsInput(idleIDs, runningIDs, prev []string, prevFP map[string]uint64, prevSince map[string]time.Time, prevCleared map[string]time.Time, prevResume map[string]int, tailOf func(string) []byte, lastInputOf func(string) time.Time, archivedOf func(string) bool, screen *agent.ScreenRenderer, sizeOf func(string) (cols, rows int)) ([]string, map[string]uint64, map[string]time.Time, map[string]time.Time, map[string]int) {
	out := make([]string, 0, len(idleIDs))
	seen := make(map[string]bool, len(idleIDs))
	newFP := make(map[string]uint64)
	flag := func(id string) {
		if !seen[id] {
			out = append(out, id)
			seen[id] = true
		}
	}

	for _, id := range idleIDs {
		if seen[id] {
			continue
		}
		cols, rows := sizeOf(id)
		if agent.DetectNeedsInputScreen(screen, tailOf(id), cols, rows) {
			flag(id)
		}
	}

	runningSet := make(map[string]bool, len(runningIDs))
	for _, id := range runningIDs {
		runningSet[id] = true
	}

	// Content-stability pass: only sessions showing an awaiting-input signal
	// (agent.AwaitingInputFingerprint: the UNAMBIGUOUS selection widget, OR a
	// free-text trailing question with the "working" affordance absent — this
	// pass has no idle gate, so a busy agent that is still working must not
	// qualify) are fingerprinted and considered.
	for _, id := range runningIDs {
		cols, rows := sizeOf(id)
		fp, ok := agent.AwaitingInputFingerprint(screen, tailOf(id), cols, rows)
		if !ok {
			continue
		}
		newFP[id] = fp
		if last, ok := prevFP[id]; ok && last == fp {
			flag(id)
		}
	}

	// Sticky carry-forward (BUG-061 hardening): a task already flagged last tick
	// and still running stays flagged WITHOUT re-requiring a fresh tail match —
	// mirrors the TUI's detectNeedsInputSticky. NeedsInputClear below is the
	// sole, deterministic clearing mechanism, so this can never leak a flag past
	// a genuine answer.
	for _, id := range prev {
		if seen[id] || !runningSet[id] {
			continue
		}
		flag(id)
	}

	// Resumed-activity pass: independent of candidacy (every running session is
	// tracked, mirroring the content-stability pass above), advance each
	// session's consecutive "working" streak. A hera coordinator's relayed
	// answer is delivered via WriteInputSystem, which never advances
	// lastUserInput (see agent.NeedsInputClear) — this is the only signal that
	// can resolve a flag raised on a worker who was genuinely un-stuck by that
	// relayed answer rather than direct user input.
	newResume := make(map[string]int, len(runningIDs))
	resumed := make(map[string]bool)
	for _, id := range runningIDs {
		cols, rows := sizeOf(id)
		_, working := agent.ContentIdleFingerprint(screen, tailOf(id), cols, rows)
		ticks, isResumed := agent.ResumeActivityTick(prevResume[id], working)
		if ticks != 0 {
			newResume[id] = ticks
		}
		if isResumed {
			resumed[id] = true
		}
	}
	resumedOf := func(id string) bool { return resumed[id] }

	// BUG-034/BUG-063: clear the flag for tasks the user has responded to,
	// archived, or that have sustained resumed activity, and suppress a stale
	// re-candidacy at an already-settled timestamp for a still-running task.
	out, newSince, newCleared := agent.NeedsInputClear(out, runningIDs, prevSince, prevCleared, lastInputOf, archivedOf, resumedOf)
	return out, newFP, newSince, newCleared, newResume
}

// idleWatcher periodically polls all running sessions and fires
//   - a session.idle event (plugin substrate, PR 2) on every busy→idle
//     transition, regardless of whether push is enabled, and
//   - a Web Push notification on the same transition when push is enabled
//     AND the per-task cycle gate allows it (see shouldFireIdlePush).
//
// 5s tick is the coarsest cadence that still feels responsive in the PWA.
// Exits when s.stopCh is closed (Server.Shutdown).
//
// Single-goroutine: state is only touched here so no mutex is needed.
func (s *Server) idleWatcher() {
	state := newIdleWatcherState()
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()

	for {
		select {
		case <-s.stopCh:
			return
		case <-tick.C:
		}
		s.idleWatcherTick(state)
	}
}

// idleTransitioned reports whether (id, isIdle) is a busy→idle transition
// relative to the prior tick. Used for session.idle event emission, which
// fires regardless of push state. Does NOT mutate the state — the state
// update happens inside the subsequent shouldFireIdlePush call.
//
// Returns false on the first observation of a session (so a watcher that
// starts with already-idle sessions doesn't fire spurious events on boot)
// and on idle→idle / busy→busy steady states.
func idleTransitioned(state *idleWatcherState, id string, isIdle bool) bool {
	if !state.seenBefore[id] {
		return false
	}
	return isIdle && !state.idleNow[id]
}

// shouldFireIdlePush applies one observation to the per-task state and
// returns whether the watcher should fire a push for an idle transition.
// Deterministic and I/O-free (the only side effect is mutating the passed
// state) so the firing logic can be unit-tested without wiring up a real
// runner + session + db. The gates run in this order:
//
// Visibility gates — must pass before any input check matters:
//   - First observation of a session never fires: prevents spurious push
//     when an already-idle session enters the watcher's view (e.g. fresh
//     idleWatcher start with running sessions present).
//   - Only busy→idle transitions fire (idle→idle and busy→busy are silent).
//
// Input-presence gate — unconditional suppression on no-input sessions:
//   - lastInputAt zero → no fire. A session that auto-started on agent-
//     view entry boots, goes idle waiting for the user's first prompt, and
//     would otherwise nag with a push for work the user hasn't kicked off.
//     lastInputAt is zero until the first WriteInput on the live session.
//
// Cycle-level gate — conditional suppression on already-notified work:
//   - One push per work cycle: after a push, no further pushes fire for the
//     same task until input arrives via WriteInput. lastInputAt is the
//     session's input timestamp; a push is suppressed if a previous push
//     already covered this idle period (no input since). This is the
//     primary defence against repeat pushes for a stale long-idle agent
//     whose incidental output (heartbeats, cursor redraws) keeps flipping
//     IsIdle false→true.
func shouldFireIdlePush(state *idleWatcherState, id string, isIdle bool, lastInputAt time.Time, now time.Time) bool {
	// Visibility gates.
	if !state.seenBefore[id] {
		state.seenBefore[id] = true
		state.idleNow[id] = isIdle
		return false
	}
	wasIdle := state.idleNow[id]
	state.idleNow[id] = isIdle
	if !isIdle || wasIdle {
		return false
	}
	// Input-presence gate.
	if lastInputAt.IsZero() {
		return false
	}
	// Cycle-level gate.
	if pushedAt, ok := state.pushedAt[id]; ok && !lastInputAt.After(pushedAt) {
		// Already pushed for this work cycle and no input has arrived since.
		// The transition is just an output blip on a stale idle session.
		return false
	}
	state.pushedAt[id] = now
	return true
}

// idleWatcherTick runs one iteration of the idle-watch loop. Extracted from
// idleWatcher so the DB + notify path is isolated from the ticker, and so
// the firing decision (shouldFireIdlePush) can be unit-tested without a
// real runner.
func (s *Server) idleWatcherTick(state *idleWatcherState) {
	running, idle := s.runner.RunningAndIdle()
	seen := make(map[string]bool, len(running))
	idleSet := make(map[string]bool, len(idle))
	for _, id := range idle {
		idleSet[id] = true
	}

	now := time.Now()

	// tailOf reads a task's recent PTY output from the live session ring (the
	// daemon is the sole PTY reader, so the ring is populated with no TUI). Shared
	// by the content-idle pass and the needs-input pass below. Routed through
	// agent.SubstantiveTail (BUG-061): a flat needsInputScanBytes window can be
	// entirely consumed by Claude's blinking cursor/status-glyph redraw, which
	// never stops even at a genuinely parked prompt — SubstantiveTail expands the
	// ring read backward (bounded by the ring's own defaultBufSize ceiling) until
	// real content surfaces.
	tailOf := func(id string) []byte {
		sess := s.runner.Get(id)
		if sess == nil {
			return nil
		}
		return agent.SubstantiveTail(sess.RecentOutputTail, needsInputScanBytes, agent.NeedsInputMaxExpandBytes)
	}

	// Content-aware idle (BUG-036): a fullscreen (alt-screen) agent parked at its
	// prompt repaints continuously, so it never reaches the raw-byte idle set and
	// would never fire idle-push. Fold the content-idle augmentation (sessions
	// whose emulated screen is stable and NOT "working") into idleSet so the
	// busy→idle transition and session.idle fire for them too. Exactly-once firing
	// is still guaranteed by shouldFireIdlePush's per-work-cycle gate (no re-push
	// until new input), so a re-asserted content-idle signal cannot storm; and a
	// non-fullscreen agent is already in idleSet (raw-idle), so it is unaffected.
	contentIdle, nextCI := agent.ContentIdle(running, idleSet, tailOf, sessionScreenSize, state.screen, state.contentIdle, now)
	state.contentIdle = nextCI
	for _, id := range contentIdle {
		idleSet[id] = true
	}

	for _, id := range running {
		seen[id] = true
		// Idle bit comes from the RunningAndIdle snapshot above so the
		// (running, idle) view stays internally consistent within this tick.
		// LastInput needs a fresh runner.Get because it isn't in the snapshot
		// — that's a small TOCTOU window: the session can exit between the
		// snapshot and Get. Get returns nil on exit, lastInput stays the
		// zero time, and the session-exited cleanup below prunes the
		// watcher state on the next tick. Net effect: zero lastInput on a
		// dying session is suppressed by shouldFireIdlePush — either by the
		// no-input gate (never-fired sessions) or by the re-arm condition
		// (previously-fired sessions, where zero is never strictly after a
		// recorded pushedAt). Worst case: one suppressed push for a session
		// about to vanish.
		var lastInput time.Time
		if sess := s.runner.Get(id); sess != nil {
			lastInput = sess.LastInput()
		}
		// Emit session.idle on every busy→idle transition (plugin
		// substrate, PR 2). Independent of push gating — plugins want
		// fine-grained visibility, push wants throttling. The state
		// mutation happens inside shouldFireIdlePush below, so the
		// transition check must read state BEFORE that call.
		if idleTransitioned(state, id, idleSet[id]) {
			events.Emit(model.EventTypeSessionIdle, id, nil)
		}
		if !shouldFireIdlePush(state, id, idleSet[id], lastInput, now) {
			continue
		}
		// Push path is gated independently — every other emission path in
		// this loop is plugin-visible, but push.Notify needs an actual
		// push manager.
		if s.push == nil {
			continue
		}
		task, err := s.db.Get(id)
		if err != nil || task == nil {
			continue
		}
		name := task.Name
		if name == "" {
			name = id
		}
		body := "Agent idle — needs attention"
		if task.Status == model.StatusInReview {
			body = "Ready for review"
		}
		uxlog.Log("[push] idle transition task=%s name=%q", id, name)
		// Empty throttle key: shouldFireIdlePush is the sole gate. See
		// context/knowledge/gotchas/web-remote.md (Web Push / VAPID) for
		// why the old "idle:<id>" 5-min throttle was removed.
		s.push.Notify("", name, body, id)
	}

	s.detectNeedsInputTick(state, running, idle, tailOf)

	// Drop entries for sessions that exited.
	for id := range state.idleNow {
		if !seen[id] {
			delete(state.idleNow, id)
			delete(state.seenBefore, id)
			delete(state.pushedAt, id)
		}
	}

	// Drive the reliable pane-delivery reconciler on every tick so pending
	// deliveries are submitted as soon as their target session becomes idle
	// and unfocused. Guard nil: notifier is wired only when the daemon fully
	// starts (may be absent in lightweight test setups).
	if s.notifier != nil {
		s.notifier.Reconcile(now)
	}
}

// detectNeedsInputTick scans the idle sessions for the "blocked waiting on the
// user" signature, publishes the resulting set onto the runner (so /api/tasks
// reflects it with no TUI attached), and emits session.needs_input events on
// every enter/leave transition — parallel to the session.idle emission above.
// One event type carries both edges; the payload's needs_input bool
// distinguishes enter (true) from clear (false).
//
// tailOf supplies each task's recent PTY output; idleWatcherTick reads from
// the live session ring buffer via the runner (the daemon is the sole PTY
// reader, so the ring is always populated — correct without any TUI). The
// state's needsInputNow map is replaced wholesale each tick, so exited
// sessions (absent from running/idle) drop out and fire a clear event.
func (s *Server) detectNeedsInputTick(state *idleWatcherState, running, idle []string, tailOf func(string) []byte) {
	prev := make([]string, 0, len(state.needsInputNow))
	for id := range state.needsInputNow {
		prev = append(prev, id)
	}
	// lastInputOf reads the daemon-owned session's most-recent-USER-input
	// timestamp (BUG-034 clear-on-input). It MUST be LastUserInput, not
	// LastInput: every input surface — TUI socket, REST — funnels through this
	// handle, but so does reliable pane delivery (hera/task messages), which is
	// SYSTEM input, not the user answering the prompt. Reliable delivery uses
	// WriteInputSystem, which advances LastInput (work cycle) but NOT
	// LastUserInput — so reading LastUserInput here keeps a delivered coordinator
	// message from clearing a parked worker's "(?)" (the regression this fixes).
	// In-process mode reads the real *agent.Session; supervisor mode reads the
	// daemon-side client handle, both of which track the user/system split.
	lastInputOf := func(id string) time.Time {
		if sess := s.runner.Get(id); sess != nil {
			return sess.LastUserInput()
		}
		return time.Time{}
	}
	// archivedOf drops archived tasks from the set (BUG-034 clear-on-archive)
	// so they stop surfacing "(?)" and stop rolling up. Built once per tick.
	archived := make(map[string]bool)
	if s.db != nil {
		if tasks, err := s.db.Tasks(); err == nil {
			for _, t := range tasks {
				if t.Archived {
					archived[t.ID] = true
				}
			}
		}
	}
	archivedOf := func(id string) bool { return archived[id] }
	needs, newFP, newSince, newCleared, newResume := computeNeedsInput(idle, running, prev, state.contentFP, state.needsInputSince, state.needsInputCleared, state.needsInputResume, tailOf, lastInputOf, archivedOf, state.screen, sessionScreenSize)
	state.contentFP = newFP
	state.needsInputSince = newSince
	state.needsInputCleared = newCleared
	state.needsInputResume = newResume
	needsSet := make(map[string]bool, len(needs))
	for _, id := range needs {
		needsSet[id] = true
	}
	for _, id := range needs {
		if !state.needsInputNow[id] {
			uxlog.Log("[needsinput] task=%s entered needs-input", id)
			events.Emit(model.EventTypeSessionNeedsInput, id, map[string]bool{"needs_input": true})
		}
	}
	for id := range state.needsInputNow {
		if !needsSet[id] {
			uxlog.Log("[needsinput] task=%s left needs-input", id)
			events.Emit(model.EventTypeSessionNeedsInput, id, map[string]bool{"needs_input": false})
		}
	}
	state.needsInputNow = needsSet
	s.runner.SetNeedsInputIDs(needs)
}
