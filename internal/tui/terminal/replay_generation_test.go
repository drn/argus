package terminal

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/testutil"
)

// addTaskLogContent adds a session log under the current HOME (setupTaskLog has
// already pointed HOME at a temp dir).
func addTaskLogContent(t *testing.T, taskID, content string) {
	t.Helper()
	p := agent.SessionLogPath(taskID)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// heldRebuild kicks a real EagerReplayBuild whose rebuild blocks before its
// publish decision until release() is called. entered closes once it is held.
func heldRebuild(t *testing.T, tp *TerminalPane) (release func(), entered <-chan struct{}, done <-chan struct{}) {
	t.Helper()
	in, rel, fin := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var once atomic.Bool
	tp.testHookBeforePublish = func() {
		if once.CompareAndSwap(false, true) {
			close(in)
			<-rel
		}
	}
	tp.OnNeedRedraw = func() {
		select {
		case <-fin:
		default:
			close(fin)
		}
	}
	tp.EagerReplayBuild()
	return func() { close(rel) }, in, fin
}

func waitChan(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(10 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
}

func newSizedPane(taskID string) *TerminalPane {
	tp := NewTerminalPane()
	tp.SetTaskID(taskID)
	tp.ptyCols, tp.ptyRows = 80, 24
	return tp
}

func inFlight(tp *TerminalPane) bool {
	tp.mu.Lock()
	defer tp.mu.Unlock()
	return tp.replayBuilding
}

// A rebuild whose binding was reset mid-flight must not publish: its emulator
// is parked, its alt-screen flag is not recorded, and the completion redraw
// fires so the next Draw can kick a fresh rebuild.
func TestReplayRebuild_StaleAfterResetVT_NotPublished(t *testing.T) {
	setupTaskLog(t, "A", "\x1b[?1049hALT-SCREEN-A")
	tp := newSizedPane("A")
	release, entered, done := heldRebuild(t, tp)
	waitChan(t, entered, "rebuild to reach publish")

	tp.ResetVT() // rail switch: bumps the generation under the in-flight rebuild
	release()
	waitChan(t, done, "completion redraw")

	tp.mu.Lock()
	defer tp.mu.Unlock()
	testutil.Equal(t, tp.replayEmu == nil, true)
	testutil.Equal(t, tp.replayEmuAltScreen, false) // stale alt-screen flag never recorded
	testutil.Equal(t, tp.replayRebuildPending, false)
	testutil.Equal(t, tp.replayEmuStale, 1)
	testutil.Equal(t, tp.replayBuilding, false)
	testutil.Equal(t, tp.replayEmuSpare != nil, true) // parked, not leaked
}

// Rebinding to B while A's rebuild is in flight: the gate holds (no second
// overlapping rebuild), A's result is discarded, B's content is what ends up
// published, and the pane never owns more than the one emulator A allocated.
func TestReplayRebuild_RebindWhileInFlight_PublishesNewBinding(t *testing.T) {
	setupTaskLog(t, "A", "CONTENT-OF-TASK-A\r\n")
	addTaskLogContent(t, "B", "CONTENT-OF-TASK-B\r\n")
	tp := newSizedPane("A")
	release, entered, done := heldRebuild(t, tp)
	waitChan(t, entered, "A to be in flight")

	tp.SetTaskID("B")
	tp.ResetVT()
	tp.EagerReplayBuild() // must be gated: A is still in flight
	tp.mu.Lock()
	testutil.Equal(t, tp.replayEmuCreated, 1)
	tp.mu.Unlock()

	release()
	waitChan(t, done, "A's stale completion")
	testutil.Equal(t, inFlight(tp), false)

	tp.testHookBeforePublish = nil
	tp.OnNeedRedraw = nil
	tp.EagerReplayBuild() // the re-kick Draw would issue for B
	deadline := time.Now().Add(10 * time.Second)
	for inFlight(tp) || tp.replayEmuForTest() == nil {
		if time.Now().After(deadline) {
			t.Fatal("B's rebuild never published")
		}
		time.Sleep(5 * time.Millisecond)
	}
	tp.mu.Lock()
	defer tp.mu.Unlock()
	got := tp.replayEmu.String()
	testutil.Equal(t, strings.Contains(got, "CONTENT-OF-TASK-B"), true)
	testutil.Equal(t, strings.Contains(got, "CONTENT-OF-TASK-A"), false)
	testutil.Equal(t, tp.replayEmuCreated, 1) // B reused A's parked emulator
	testutil.Equal(t, tp.replayEmuReused, 1)
	testutil.Equal(t, tp.replayEmuDropped, 0)
}

func (tp *TerminalPane) replayEmuForTest() any {
	tp.mu.Lock()
	defer tp.mu.Unlock()
	if tp.replayEmu == nil {
		return nil
	}
	return tp.replayEmu
}

// Repeated rebinds while rebuilds are in flight never grow the pane past two
// emulators (the pre-fix code allocated one per overlap and leaked it with its
// drain goroutine), and the dropped-emulator tripwire stays at zero.
func TestReplayRebuild_RepeatedRebinds_BoundedEmulatorsAndGoroutines(t *testing.T) {
	setupTaskLog(t, "A", "CONTENT-A\r\n")
	addTaskLogContent(t, "B", "CONTENT-B\r\n")
	tp := newSizedPane("A")
	base := runtime.NumGoroutine()
	for i := 0; i < 25; i++ {
		id := "A"
		if i%2 == 1 {
			id = "B"
		}
		tp.SetTaskID(id)
		release, entered, done := heldRebuild(t, tp)
		waitChan(t, entered, "rebuild in flight")
		tp.invalidateReplayCache() // and a ResetVT-style bump mid-flight
		tp.ResetVT()
		tp.EagerReplayBuild() // gated
		release()
		waitChan(t, done, "completion")
	}
	tp.mu.Lock()
	defer tp.mu.Unlock()
	if tp.replayEmuCreated > 2 {
		t.Fatalf("created %d replay emulators over 25 rebinds, want <= 2", tp.replayEmuCreated)
	}
	testutil.Equal(t, tp.replayEmuDropped, 0)
	if delta := runtime.NumGoroutine() - base; delta > 4 {
		t.Fatalf("goroutine delta %d after 25 rebinds, want <= 4", delta)
	}
}

// A panic between acquire and publish must not strand the in-flight flag or
// the emulator.
func TestReplayRebuild_PanicParksEmulatorAndClearsInFlight(t *testing.T) {
	setupTaskLog(t, "A", "CONTENT-A\r\n")
	tp := newSizedPane("A")
	tp.testHookBeforePublish = func() { panic("boom") }
	done := make(chan struct{})
	tp.OnNeedRedraw = func() { close(done) }
	tp.EagerReplayBuild()
	waitChan(t, done, "release after panic")
	tp.mu.Lock()
	defer tp.mu.Unlock()
	testutil.Equal(t, tp.replayBuilding, false)
	testutil.Equal(t, tp.replayEmu == nil, true)
	testutil.Equal(t, tp.replayEmuSpare != nil, true)
}

// The tripwire: with both spare slots full a third stash is counted, not
// silently lost (and never Closed).
func TestStashReplayEmulator_DropTripwire(t *testing.T) {
	tp := NewTerminalPane()
	a := tp.acquireReplayEmulatorForRebuild(80, 24, nil)
	b := tp.acquireReplayEmulatorForRebuild(80, 24, nil)
	c := tp.acquireReplayEmulatorForRebuild(80, 24, nil)
	tp.mu.Lock()
	tp.stashReplayEmulatorLocked(a, 80, 24)
	tp.stashReplayEmulatorLocked(b, 80, 24)
	tp.stashReplayEmulatorLocked(c, 80, 24)
	testutil.Equal(t, tp.replayEmuDropped, 1)
	tp.mu.Unlock()
}

// Pre-fix ordering bug: ResetVT cleared replayBuilding while rebuild A was
// still running, so a second rebuild (B, for the new binding) could start,
// finish FIRST, and then A — carrying the PREVIOUS task's content — published
// last and overwrote B's fresh emulator (a garbling source). A is made slow
// with a large dense log and B tiny so B reliably finishes first. Both
// rebuilds are started directly (bypassing the in-flight gate, as the old
// code allowed) and A must still not win.
func TestReplayRebuild_SlowStaleRebuildCannotOverwriteFresh(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a multi-MB log")
	}
	setupTaskLog(t, "slowA", "STALE-A-MARKER\r\n"+denseLogContent(12*1024*1024))
	addTaskLogContent(t, "fastB", "FRESH-B-MARKER\r\n")
	tp := NewTerminalPane()
	tp.ptyCols, tp.ptyRows = 80, 24

	aDone := make(chan struct{})
	go func() {
		tp.asyncReplayRebuild("slowA", 0, 24, 80, 24, nil, nil, 0, false, false, nil)
		close(aDone)
	}()
	time.Sleep(50 * time.Millisecond) // let A read its log and start emulating
	tp.SetTaskID("fastB")
	tp.ResetVT() // rail switch to B while A is in flight
	tp.asyncReplayRebuild("fastB", 0, 24, 80, 24, nil, nil, 0, false, false, nil)
	waitChan(t, aDone, "slow A to finish")

	tp.mu.Lock()
	defer tp.mu.Unlock()
	if tp.replayEmu == nil {
		t.Fatal("no replay emulator published")
	}
	got := tp.replayEmu.String()
	if strings.Contains(got, "STALE-A-MARKER") && !strings.Contains(got, "FRESH-B-MARKER") {
		t.Fatalf("stale rebuild A overwrote fresh rebuild B's emulator")
	}
	testutil.Equal(t, strings.Contains(got, "FRESH-B-MARKER"), true)
}

// Each generation-bumping event must independently invalidate an in-flight
// rebuild (ResetVT is covered above; these pin the other two so removing one
// bump is caught, not masked by the others).
func TestReplayRebuild_StaleAfterEachInvalidatingEvent(t *testing.T) {
	cases := map[string]func(tp *TerminalPane){
		"SetTaskID":             func(tp *TerminalPane) { tp.SetTaskID("B") },
		"invalidateReplayCache": func(tp *TerminalPane) { tp.invalidateReplayCache() },
	}
	for name, event := range cases {
		t.Run(name, func(t *testing.T) {
			setupTaskLog(t, "A", "CONTENT-A\r\n")
			tp := newSizedPane("A")
			release, entered, done := heldRebuild(t, tp)
			waitChan(t, entered, "rebuild in flight")
			event(tp)
			release()
			waitChan(t, done, "completion")
			tp.mu.Lock()
			defer tp.mu.Unlock()
			testutil.Equal(t, tp.replayEmuStale, 1)
			testutil.Equal(t, tp.replayEmu == nil, true)
		})
	}
}

// Positive control for the stale alt-screen test: an UNRESET rebuild of the
// same log does record the alt-screen flag, so "not recorded" above is
// meaningful.
func TestReplayRebuild_NotStale_PublishesAltScreen(t *testing.T) {
	setupTaskLog(t, "A", "\x1b[?1049hALT-SCREEN-A")
	tp := newSizedPane("A")
	release, entered, done := heldRebuild(t, tp)
	waitChan(t, entered, "rebuild in flight")
	release()
	waitChan(t, done, "completion")
	tp.mu.Lock()
	defer tp.mu.Unlock()
	testutil.Equal(t, tp.replayEmuAltScreen, true)
	testutil.Equal(t, tp.replayEmu != nil, true)
	testutil.Equal(t, tp.replayEmuStale, 0)
}

func TestPopReplaySpare_DrainsBothSlots(t *testing.T) {
	tp := NewTerminalPane()
	a := tp.acquireReplayEmulatorForRebuild(80, 24, nil)
	b := tp.acquireReplayEmulatorForRebuild(100, 30, nil)
	tp.mu.Lock()
	defer tp.mu.Unlock()
	tp.stashReplayEmulatorLocked(a, 80, 24)
	tp.stashReplayEmulatorLocked(b, 100, 30)
	e1, c1, r1 := tp.popReplaySpareLocked()
	e2, c2, r2 := tp.popReplaySpareLocked()
	e3, _, _ := tp.popReplaySpareLocked()
	testutil.Equal(t, e1 == a && c1 == 80 && r1 == 24, true)
	testutil.Equal(t, e2 == b && c2 == 100 && r2 == 30, true)
	testutil.Equal(t, e3 == nil, true)
}
