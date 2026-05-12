package dagview

import (
	"testing"

	"github.com/drn/argus/internal/testutil"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// noFocus is the test setFocus shim — handlers don't actually move focus
// during unit tests, so a no-op is sufficient.
func noFocus(_ tview.Primitive) {}

// fakeKey wraps tcell.EventKey for ergonomic test input.
func runeKey(r rune) *tcell.EventKey {
	return tcell.NewEventKey(tcell.KeyRune, r, tcell.ModNone)
}

func TestWidget_SetNodesClampsCursor(t *testing.T) {
	w := New()
	w.SetNodes([]Node{{ID: "A"}, {ID: "B", DependsOn: []string{"A"}}})
	w.cursor = "ghost"
	w.SetNodes([]Node{{ID: "A"}})
	testutil.Equal(t, w.CurrentTask(), "A")
}

func TestWidget_SetNodesEmptyClearsCursor(t *testing.T) {
	w := New()
	w.SetNodes([]Node{{ID: "A"}})
	w.cursor = "A"
	w.SetNodes(nil)
	testutil.Equal(t, w.CurrentTask(), "")
}

func TestWidget_BranchChangeFiresOnSetNodes(t *testing.T) {
	w := New()
	calls := 0
	w.OnBranchChange = func() { calls++ }
	w.SetNodes([]Node{{ID: "A"}})
	if calls == 0 {
		t.Fatal("expected OnBranchChange to fire on first SetNodes")
	}
	prior := calls
	// Same shape — no fire.
	w.SetNodes([]Node{{ID: "A"}})
	if calls != prior {
		t.Fatalf("expected no fire on identical SetNodes; got %d", calls-prior)
	}
	// Different node count — fires.
	w.SetNodes([]Node{{ID: "A"}, {ID: "B"}})
	if calls == prior {
		t.Fatal("expected fire on node count change")
	}
}

// TestWidget_BranchChangeFiresOnFailedFlip guards the branchShape failed-bit
// regression: previously the bit packed `len(w.failed)` (the map size, which
// equals node count regardless of failure state) instead of the count of
// `true` values. As a result, flipping a node from healthy to failed did NOT
// fire OnBranchChange, leaving stale glyph cells under tmux/iTerm2.
func TestWidget_BranchChangeFiresOnFailedFlip(t *testing.T) {
	w := New()
	w.SetNodes([]Node{{ID: "A", Name: "alpha", Status: "in_progress"}})
	calls := 0
	w.OnBranchChange = func() { calls++ }
	// Same node count, but result now reports failure.
	w.SetNodes([]Node{{ID: "A", Name: "alpha", Status: "in_progress", Result: `{"failed":true}`}})
	if calls == 0 {
		t.Fatal("expected OnBranchChange to fire when a node transitions to failed")
	}
}

func TestWidget_BranchChangeFiresOnFocusFlip(t *testing.T) {
	w := New()
	w.SetNodes([]Node{{ID: "A"}})
	calls := 0
	w.OnBranchChange = func() { calls++ }
	w.SetFocused(true)
	w.SetFocused(true) // no-op
	w.SetFocused(false)
	if calls != 2 {
		t.Fatalf("expected 2 fires for focus flip, got %d", calls)
	}
}

func TestWidget_MoveCursorClamps(t *testing.T) {
	w := New()
	w.SetNodes([]Node{
		{ID: "A"},
		{ID: "B", DependsOn: []string{"A"}},
	})
	w.cursor = "A"
	w.MoveCursor(0, 1) // down to B
	testutil.Equal(t, w.CurrentTask(), "B")
	w.MoveCursor(0, 1) // clamp at last layer
	testutil.Equal(t, w.CurrentTask(), "B")
	w.MoveCursor(0, -1) // back up
	testutil.Equal(t, w.CurrentTask(), "A")
	w.MoveCursor(0, -1) // clamp at first layer
	testutil.Equal(t, w.CurrentTask(), "A")
}

func TestWidget_MoveCursor_FanOut(t *testing.T) {
	// A has B/C/D as children — left/right within layer 1 should walk between them.
	w := New()
	w.SetNodes([]Node{
		{ID: "A"},
		{ID: "B", DependsOn: []string{"A"}},
		{ID: "C", DependsOn: []string{"A"}},
		{ID: "D", DependsOn: []string{"A"}},
	})
	w.cursor = "A"
	w.MoveCursor(0, 1)
	// Initial child is whichever ended up in col 0 — order is deterministic
	// but algorithm-dependent. Just assert we moved into layer 1.
	first := w.CurrentTask()
	if first == "" || first == "A" {
		t.Fatalf("expected to move into layer 1, got %q", first)
	}
	// Move right; should land on a different sibling.
	w.MoveCursor(1, 0)
	if w.CurrentTask() == first {
		t.Fatalf("expected sibling movement to change cursor, stayed %q", first)
	}
}

func TestWidget_KeyEnterFiresOnEnter(t *testing.T) {
	w := New()
	w.SetNodes([]Node{{ID: "A"}})
	got := ""
	w.OnEnter = func(id string) { got = id }
	handler := w.InputHandler()
	handler(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone), noFocus)
	testutil.Equal(t, got, "A")
}

func TestWidget_LinkUnlinkHaltCallbacks(t *testing.T) {
	w := New()
	w.SetNodes([]Node{{ID: "A"}})
	var linkID, unlinkID, haltID string
	w.OnLink = func(id string) { linkID = id }
	w.OnUnlink = func(id string) { unlinkID = id }
	w.OnHalt = func(id string) { haltID = id }
	handler := w.InputHandler()

	handler(runeKey('l'), noFocus)
	testutil.Equal(t, linkID, "A")

	handler(runeKey('L'), noFocus)
	testutil.Equal(t, unlinkID, "A")

	handler(runeKey('h'), noFocus)
	testutil.Equal(t, haltID, "A")
}

func TestWidget_KeyJKMovesCursor(t *testing.T) {
	w := New()
	w.SetNodes([]Node{
		{ID: "A"},
		{ID: "B", DependsOn: []string{"A"}},
	})
	w.cursor = "A"
	handler := w.InputHandler()
	handler(runeKey('j'), noFocus)
	testutil.Equal(t, w.CurrentTask(), "B")
	handler(runeKey('k'), noFocus)
	testutil.Equal(t, w.CurrentTask(), "A")
}

func TestParseFailed(t *testing.T) {
	cases := []struct {
		raw  string
		want bool
	}{
		{"", false},
		{"not-json", false},
		{`{"pr_url":"x"}`, false},
		{`{"failed":true}`, true},
		{`{"failed":true,"reason":"oops"}`, true},
		{`{"failed":false}`, false},
	}
	for _, tc := range cases {
		if got := parseFailed(tc.raw); got != tc.want {
			t.Errorf("parseFailed(%q): got %v, want %v", tc.raw, got, tc.want)
		}
	}
}
