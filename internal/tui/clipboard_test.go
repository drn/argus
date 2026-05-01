package tui

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/testutil"
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

	app.clipboardPending = "x"
	app.clipboardPendingTask = "abc"

	// Should not panic even when ClipboardClear errors.
	if !app.copyStagedClipboard() {
		t.Fatal("expected true")
	}
}
