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

// fakeProvider satisfies agent.SessionProvider + clipboardAccessor.
type fakeProvider struct {
	*agent.Runner

	mu          sync.Mutex
	clipText    string
	clipPresent bool
	clearedFor  []string
	clearErr    error
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
	return f.clearErr
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

func TestCopyStagedClipboard_ClearsLocalStateAndFiresClearRPC(t *testing.T) {
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

	// Local state cleared synchronously.
	testutil.Equal(t, app.clipboardPending, "")
	testutil.Equal(t, app.clipboardPendingTask, "")
	testutil.Equal(t, app.agentHeader.ClipboardHint(), false)

	// Clear RPC fires asynchronously; spin briefly waiting for it.
	var seen []string
	for d := 200; d > 0; d-- {
		seen = fp.clearedSnapshot()
		if len(seen) > 0 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if len(seen) != 1 || seen[0] != "abc123" {
		t.Errorf("expected ClipboardClear(\"abc123\") once, got %v", seen)
	}
}

func TestCopyStagedClipboard_ClearError_LoggedNotPanicked(t *testing.T) {
	d := testDB(t)
	fp := newFakeProvider()
	fp.clearErr = errors.New("rpc broken")
	app := New(d, fp, false)
	app.clipboardWriter = func(string) error { return nil }

	app.clipboardPending = "x"
	app.clipboardPendingTask = "abc"

	// Should not panic even when ClipboardClear errors.
	if !app.copyStagedClipboard() {
		t.Fatal("expected true")
	}
}

func TestCopyStagedClipboardForHeraPane_NoTaskOrAccessor(t *testing.T) {
	d := testDB(t)
	// Empty task → no-op, no panic.
	app := New(d, agent.NewRunner(nil), false)
	app.copyStagedClipboardForHeraPane("")
	// Plain runner is not a clipboardAccessor → logged no-op, no panic.
	app.copyStagedClipboardForHeraPane("task1")
}

func TestCopyStagedClipboardForHeraPane_AbsentNoCopy(t *testing.T) {
	d := testDB(t)
	fp := newFakeProvider()
	fp.setPayload("", false) // nothing staged
	app := New(d, fp, false)
	app.clipboardWriter = func(string) error { return nil }

	app.copyStagedClipboardForHeraPane("task1")
	// Nothing staged → no clear RPC fired.
	time.Sleep(20 * time.Millisecond)
	testutil.Equal(t, len(fp.clearedSnapshot()), 0)
}

func TestCopyStagedClipboardForHeraPane_PresentCopiesAndClears(t *testing.T) {
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

	// Clear RPC fires asynchronously for that task.
	var seen []string
	for i := 200; i > 0; i-- {
		seen = fp.clearedSnapshot()
		if len(seen) > 0 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if len(seen) != 1 || seen[0] != "wkr-task" {
		t.Errorf("expected ClipboardClear(\"wkr-task\") once, got %v", seen)
	}
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
