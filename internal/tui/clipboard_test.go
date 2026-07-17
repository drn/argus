package tui

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/apiclient"
	dclient "github.com/drn/argus/internal/daemon/client"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
	"github.com/drn/argus/internal/tui/hera"
	"github.com/gdamore/tcell/v2"
)

// Compile-time assertions: both runner transports that back the agent view
// satisfy clipboardAccessor, so ctrl+y copy works in local daemon mode AND in
// --remote mode. Lives in _test.go so importing apiclient/daemon-client here
// doesn't drag those deps into production tui code. If a future refactor drops
// either method, this fails the build and pinpoints the drift.
var (
	_ clipboardAccessor = (*dclient.Client)(nil)
	_ clipboardAccessor = (*apiclient.Provider)(nil)
)

// fakeProvider satisfies agent.SessionProvider + clipboardAccessor. It also
// implements ClipboardClear (not required by clipboardAccessor since
// fix-ctrl-y-copy-persist) purely as test instrumentation, so tests can
// assert that copying a staged payload does NOT trigger a clear.
type fakeProvider struct {
	*agent.Runner

	mu          sync.Mutex
	clipText    string
	clipPresent bool
	clearedFor  []string
}

func newFakeProvider() *fakeProvider {
	return &fakeProvider{Runner: agent.NewRunner(nil)}
}

func (f *fakeProvider) ClipboardGet(taskID string) (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.clipPresent {
		return "", false
	}
	return f.clipText, true
}

func (f *fakeProvider) ClipboardClear(taskID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.clearedFor = append(f.clearedFor, taskID)
	return nil
}

func (f *fakeProvider) clearedSnapshot() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.clearedFor))
	copy(out, f.clearedFor)
	return out
}

func (f *fakeProvider) setPayload(text string, present bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.clipText = text
	f.clipPresent = present
}

func TestRefreshClipboardCache_NoAccessor(t *testing.T) {
	d := testDB(t)
	app := New(d, agent.NewRunner(nil), false)
	// Plain runner is NOT a clipboardAccessor — refresh is a no-op.
	app.refreshClipboardCache("task1")
	testutil.Equal(t, app.clipboardPending, "")
	testutil.Equal(t, app.agentHeader.ClipboardHint(), false)
}

func TestRefreshClipboardCache_PresentSetsHint(t *testing.T) {
	d := testDB(t)
	fp := newFakeProvider()
	fp.setPayload("hello", true)
	app := New(d, fp, false)

	app.refreshClipboardCache("task1")
	testutil.Equal(t, app.clipboardPending, "hello")
	testutil.Equal(t, app.clipboardPendingTask, "task1")
	testutil.Equal(t, app.agentHeader.ClipboardHint(), true)
}

func TestRefreshClipboardCache_AbsentClearsHint(t *testing.T) {
	d := testDB(t)
	fp := newFakeProvider()
	fp.setPayload("hi", true)
	app := New(d, fp, false)

	app.refreshClipboardCache("task1")
	testutil.Equal(t, app.agentHeader.ClipboardHint(), true)

	fp.setPayload("", false)
	app.refreshClipboardCache("task1")
	testutil.Equal(t, app.clipboardPending, "")
	testutil.Equal(t, app.agentHeader.ClipboardHint(), false)
}

func TestCopyStagedClipboard_NoPayload(t *testing.T) {
	d := testDB(t)
	app := New(d, agent.NewRunner(nil), false)

	if app.copyStagedClipboard() {
		t.Error("expected false when nothing staged")
	}
}

// ctrlYEvent is the key event ctrl+y dispatches in the agent view.
func ctrlYEvent() *tcell.EventKey { return tcell.NewEventKey(tcell.KeyCtrlY, 0, tcell.ModNone) }

// TestHandleAgentKey_CtrlY_NothingStaged_FlashesNoticeAndConsumes drives the
// REAL dispatcher (handleAgentKey), not a manual re-execution of the switch
// case body, so a regression in the ActAgentCopy wiring itself (e.g. an
// inverted condition, or the case losing its `return nil`) would fail this
// test. Covers the "nothing staged" path: ctrl+y must be consumed (nil
// result) and flash "Nothing to copy" rather than reaching the PTY.
func TestHandleAgentKey_CtrlY_NothingStaged_FlashesNoticeAndConsumes(t *testing.T) {
	d := testDB(t)
	app := New(d, agent.NewRunner(nil), false)
	app.mode = modeAgent
	app.agentState.Reset("t1", "test")

	result := app.handleAgentKey(ctrlYEvent())

	testutil.Nil(t, result) // consumed, never forwarded to the PTY
	testutil.Equal(t, app.header.Notice(), "Nothing to copy")
}

// TestHandleAgentKey_CtrlY_Staged_CopiesAndConsumes covers the staged-payload
// path through the real dispatcher: ctrl+y copies to the OS clipboard writer,
// leaves the staged payload in place (fix-ctrl-y-copy-persist), and consumes
// the key.
func TestHandleAgentKey_CtrlY_Staged_CopiesAndConsumes(t *testing.T) {
	d := testDB(t)
	fp := newFakeProvider()
	app := New(d, fp, false)
	app.mode = modeAgent
	app.agentState.Reset("t1", "test")

	wrote := make(chan string, 1)
	app.clipboardWriter = func(s string) error {
		select {
		case wrote <- s:
		default:
		}
		return nil
	}
	app.clipboardPending = "snippet"
	app.clipboardPendingTask = "t1"

	result := app.handleAgentKey(ctrlYEvent())

	testutil.Nil(t, result) // consumed, never forwarded to the PTY
	testutil.Equal(t, app.clipboardPending, "snippet")
	testutil.Equal(t, app.clipboardPendingTask, "t1")

	select {
	case s := <-wrote:
		testutil.Equal(t, s, "snippet")
	case <-time.After(time.Second):
		t.Fatal("clipboard writer never called")
	}
}

// TestCopyStagedClipboard_PreservesStagedStateAfterCopy is the regression
// test for fix-ctrl-y-copy-persist: a successful copy must NOT clear the
// local cache, the header hint, or fire a ClipboardClear RPC — clearing is
// owned entirely by the store's own TTL/last-write-wins/session-exit
// lifecycle, not by the copy action.
func TestCopyStagedClipboard_PreservesStagedStateAfterCopy(t *testing.T) {
	d := testDB(t)
	fp := newFakeProvider()
	app := New(d, fp, false)
	app.clipboardWriter = func(string) error { return nil }

	app.clipboardPending = "snippet"
	app.clipboardPendingTask = "abc123"
	app.agentHeader.SetClipboardHint(true)

	if !app.copyStagedClipboard() {
		t.Fatal("expected true when payload staged")
	}

	// Local state and hint stay intact after a successful copy.
	testutil.Equal(t, app.clipboardPending, "snippet")
	testutil.Equal(t, app.clipboardPendingTask, "abc123")
	testutil.Equal(t, app.agentHeader.ClipboardHint(), true)

	// No clear RPC fires. Give any errant async call a moment to land before
	// asserting its absence.
	time.Sleep(20 * time.Millisecond)
	testutil.Equal(t, len(fp.clearedSnapshot()), 0)
}

// TestCopyStagedClipboard_CopyTwiceBothSucceed: pressing ctrl+y twice in a
// row with the same staged payload must succeed both times — the second
// press must NOT report "nothing to copy", since the first copy no longer
// clears the staged slot (fix-ctrl-y-copy-persist).
func TestCopyStagedClipboard_CopyTwiceBothSucceed(t *testing.T) {
	d := testDB(t)
	fp := newFakeProvider()
	app := New(d, fp, false)

	wrote := make(chan string, 2)
	app.clipboardWriter = func(s string) error {
		wrote <- s
		return nil
	}
	app.clipboardPending = "snippet"
	app.clipboardPendingTask = "abc123"

	if !app.copyStagedClipboard() {
		t.Fatal("expected true on first copy")
	}
	if !app.copyStagedClipboard() {
		t.Fatal("expected true on second copy")
	}

	for i := 0; i < 2; i++ {
		select {
		case s := <-wrote:
			testutil.Equal(t, s, "snippet")
		case <-time.After(time.Second):
			t.Fatalf("clipboard writer call %d never arrived", i+1)
		}
	}
}

func TestCopyStagedClipboardForHeraPane_NoTaskOrAccessor(t *testing.T) {
	d := testDB(t)
	// Empty task → no-op, no panic, no notice (never reaches the intercept).
	app := New(d, agent.NewRunner(nil), false)
	app.copyStagedClipboardForHeraPane("")
	testutil.Equal(t, app.header.Notice(), "")

	// Plain runner is not a clipboardAccessor → logged no-op, flashes "Nothing
	// to copy" (ctrl+y is always intercepted, so the user still gets feedback).
	app.copyStagedClipboardForHeraPane("task1")
	testutil.Equal(t, app.header.Notice(), "Nothing to copy")
}

func TestCopyStagedClipboardForHeraPane_AbsentNoCopy(t *testing.T) {
	d := testDB(t)
	fp := newFakeProvider()
	fp.setPayload("", false) // nothing staged
	app := New(d, fp, false)
	app.clipboardWriter = func(string) error { return nil }

	app.copyStagedClipboardForHeraPane("task1")
	// Nothing staged → no clear RPC fired, but the user still gets feedback.
	time.Sleep(20 * time.Millisecond)
	testutil.Equal(t, len(fp.clearedSnapshot()), 0)
	testutil.Equal(t, app.header.Notice(), "Nothing to copy")
}

// TestCopyStagedClipboardForHeraPane_PresentCopiesWithoutClearing is the
// Hera-pane analogue of TestCopyStagedClipboard_PreservesStagedStateAfterCopy
// (fix-ctrl-y-copy-persist): copying a staged payload must not clear the
// daemon-side staged slot.
func TestCopyStagedClipboardForHeraPane_PresentCopiesWithoutClearing(t *testing.T) {
	d := testDB(t)
	fp := newFakeProvider()
	fp.setPayload("snippet", true)
	app := New(d, fp, false)

	wrote := make(chan string, 1)
	app.clipboardWriter = func(s string) error {
		select {
		case wrote <- s:
		default:
		}
		return nil
	}

	app.copyStagedClipboardForHeraPane("wkr-task")

	// The staged text reaches the OS-clipboard writer.
	select {
	case s := <-wrote:
		testutil.Equal(t, s, "snippet")
	case <-time.After(time.Second):
		t.Fatal("clipboard writer never called")
	}

	// No clear RPC fires — the daemon-side staged payload stays intact.
	time.Sleep(20 * time.Millisecond)
	testutil.Equal(t, len(fp.clearedSnapshot()), 0)
}

// seedHeraCoord seeds an orchestrator with a coordinator bound to "tc" and a
// worker bound to "tw". The App's rail starts with the cursor on the folded
// orch header (= the coordinator), so focusing the coord pane yields "tc"
// without any rail navigation.
func seedHeraCoord(t *testing.T, d *db.DB) {
	t.Helper()
	o, err := d.CreateHeraOrchestrator("o", "")
	testutil.NoError(t, err)
	coord, err := d.CreateHeraRole(db.CreateHeraRoleInput{OrchestratorID: o.ID, Name: "c", Kind: db.HeraKindCoordinator, ArgusProject: "p"})
	testutil.NoError(t, err)
	wkr, err := d.CreateHeraRole(db.CreateHeraRoleInput{OrchestratorID: o.ID, Name: "w", Kind: db.HeraKindWorker, ArgusProject: "p"})
	testutil.NoError(t, err)
	for _, b := range []struct {
		role int64
		task string
	}{{coord.ID, "tc"}, {wkr.ID, "tw"}} {
		testutil.NoError(t, d.Add(&model.Task{ID: b.task, Name: b.task, Status: model.StatusInProgress, Project: "p", CreatedAt: time.Now()}))
		_, err = d.CreateHeraBinding(db.CreateHeraBindingInput{RoleID: b.role, ArgusTaskID: b.task, WorktreePath: "/wt/" + b.task})
		testutil.NoError(t, err)
	}
}

// TestRefreshHeraClipboardHint asserts the per-tick hint reflects whether a
// payload is staged for the focused pane's task.
func TestRefreshHeraClipboardHint(t *testing.T) {
	d := testDB(t)
	seedHeraCoord(t, d)

	fp := newFakeProvider()
	app := New(d, fp, false)
	app.heraPage.Refresh()

	// Cursor rests on the folded orch header (the coordinator); focus its pane.
	app.heraPage.Machine().SetRegion(hera.FocusCoord)
	testutil.Equal(t, app.heraPage.FocusedTerminalTaskID(), "tc")

	// Nothing staged → hint off.
	app.refreshHeraClipboardHint()
	testutil.Equal(t, app.heraPage.ClipboardHint(), false)

	// Payload staged for the focused pane's task → hint on.
	fp.setPayload("x", true)
	app.refreshHeraClipboardHint()
	testutil.Equal(t, app.heraPage.ClipboardHint(), true)

	// Focus the rail (no terminal) → hint forced off even with a payload staged.
	app.heraPage.Machine().SetRegion(hera.FocusRail)
	app.refreshHeraClipboardHint()
	testutil.Equal(t, app.heraPage.ClipboardHint(), false)
}

func TestRefreshHeraClipboardHint_NoAccessor(t *testing.T) {
	d := testDB(t)
	seedHeraCoord(t, d)

	// Plain runner is not a clipboardAccessor → hint stays off even with a focused pane.
	app := New(d, agent.NewRunner(nil), false)
	app.heraPage.Refresh()
	app.heraPage.Machine().SetRegion(hera.FocusCoord)
	app.refreshHeraClipboardHint()
	testutil.Equal(t, app.heraPage.ClipboardHint(), false)
}

// TestCopyToClipboard_WriterError covers the early-return branch in
// copyToClipboard when the writer fails: onSuccess MUST NOT fire and no
// header notice is set.
func TestCopyToClipboard_WriterError(t *testing.T) {
	d := testDB(t)
	app := New(d, agent.NewRunner(nil), false)

	app.clipboardWriter = func(string) error { return errors.New("pbcopy failed") }

	successFired := make(chan struct{}, 1)
	app.copyToClipboard("payload", "Notice", func() {
		select {
		case successFired <- struct{}{}:
		default:
		}
	})

	select {
	case <-successFired:
		t.Fatal("onSuccess should not fire when writer returns an error")
	case <-time.After(100 * time.Millisecond):
		// expected: writer error, no callback
	}
}
