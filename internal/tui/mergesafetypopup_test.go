package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/drn/argus/internal/testutil"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

func drawMergeSafetyPopupToString(t *testing.T, m *MergeSafetyPopup, w, h int) string {
	t.Helper()
	m.SetRect(0, 0, w, h)
	screen := tcell.NewSimulationScreen("")
	testutil.NoError(t, screen.Init())
	screen.SetSize(w, h)
	m.Draw(screen)
	var b strings.Builder
	for row := range h {
		for col := range w {
			s, _, _ := screen.Get(col, row)
			b.WriteString(s)
		}
		b.WriteString("\n")
	}
	return b.String()
}

func sampleMergeSafetyCandidates() []mergeSafetyCandidate {
	return []mergeSafetyCandidate{
		{TaskID: "t1", Name: "alpha-task", Safe: true, Reason: `branch "alpha" is an ancestor of "master"`},
		{TaskID: "t2", Name: "beta-task", Safe: false, Reason: "no matching merged pull request found"},
	}
}

// TestMergeSafetyPopup_SectionsOrderedNotSafeThenSafe covers the spec
// scenario "Sections are ordered NOT-SAFE then SAFE".
func TestMergeSafetyPopup_SectionsOrderedNotSafeThenSafe(t *testing.T) {
	m := NewMergeSafetyPopup(" Cleanup ", sampleMergeSafetyCandidates())
	out := drawMergeSafetyPopupToString(t, m, 100, 24)

	notSafeIdx := strings.Index(out, "NOT-SAFE")
	safeIdx := strings.Index(out, "SAFE (1)")
	if notSafeIdx < 0 || safeIdx < 0 {
		t.Fatalf("expected both section headers to render; got:\n%s", out)
	}
	if notSafeIdx > safeIdx {
		t.Errorf("expected NOT-SAFE section before SAFE section; got NOT-SAFE at %d, SAFE at %d", notSafeIdx, safeIdx)
	}
	testutil.Contains(t, out, "beta-task")
	testutil.Contains(t, out, "alpha-task")
	// NOT-SAFE rows show the reason; the row text carries it verbatim.
	testutil.Contains(t, out, "no matching merged pull request found")
}

// TestMergeSafetyPopup_DefaultActionIsCleanSafe covers "Clean safe is the
// default-selected action".
func TestMergeSafetyPopup_DefaultActionIsCleanSafe(t *testing.T) {
	m := NewMergeSafetyPopup(" Cleanup ", sampleMergeSafetyCandidates())
	testutil.Equal(t, m.SelectedLabel(), "Clean safe")
	out := drawMergeSafetyPopupToString(t, m, 100, 24)
	testutil.Contains(t, out, "[Clean safe]")
}

// TestMergeSafetyPopup_EnterOnCleanSafeConfirms covers "Clean safe acts only
// on the SAFE section" from the widget's perspective — Confirmed()+Scope()
// report exactly the choice made; filtering to the SAFE subset is the
// caller's job (tested at the wiring layer).
func TestMergeSafetyPopup_EnterOnCleanSafeConfirms(t *testing.T) {
	m := NewMergeSafetyPopup(" Cleanup ", sampleMergeSafetyCandidates())
	m.InputHandler()(tcell.NewEventKey(tcell.KeyEnter, 0, 0), func(tview.Primitive) {})
	testutil.Equal(t, m.Confirmed(), true)
	testutil.Equal(t, m.Canceled(), false)
	testutil.Equal(t, m.Scope(), mergeSafetyScopeSafe)
}

// TestMergeSafetyPopup_RightThenEnterChoosesCleanAll covers "Clean all acts
// on every listed task".
func TestMergeSafetyPopup_RightThenEnterChoosesCleanAll(t *testing.T) {
	m := NewMergeSafetyPopup(" Cleanup ", sampleMergeSafetyCandidates())
	h := m.InputHandler()
	h(tcell.NewEventKey(tcell.KeyRight, 0, 0), func(tview.Primitive) {})
	testutil.Equal(t, m.SelectedLabel(), "Clean all")
	h(tcell.NewEventKey(tcell.KeyEnter, 0, 0), func(tview.Primitive) {})
	testutil.Equal(t, m.Confirmed(), true)
	testutil.Equal(t, m.Scope(), mergeSafetyScopeAll)
}

// TestMergeSafetyPopup_CancelActionPerformsNoAction covers "Cancel performs
// no action" via the explicit Cancel button (not just Esc).
func TestMergeSafetyPopup_CancelActionPerformsNoAction(t *testing.T) {
	m := NewMergeSafetyPopup(" Cleanup ", sampleMergeSafetyCandidates())
	h := m.InputHandler()
	h(tcell.NewEventKey(tcell.KeyRight, 0, 0), func(tview.Primitive) {})
	h(tcell.NewEventKey(tcell.KeyRight, 0, 0), func(tview.Primitive) {})
	testutil.Equal(t, m.SelectedLabel(), "Cancel")
	h(tcell.NewEventKey(tcell.KeyEnter, 0, 0), func(tview.Primitive) {})
	testutil.Equal(t, m.Canceled(), true)
	testutil.Equal(t, m.Confirmed(), false)
}

// TestMergeSafetyPopup_EscCancels mirrors ConfirmModal's Esc/Ctrl+Q contract.
func TestMergeSafetyPopup_EscCancels(t *testing.T) {
	m := NewMergeSafetyPopup(" Cleanup ", sampleMergeSafetyCandidates())
	m.InputHandler()(tcell.NewEventKey(tcell.KeyEscape, 0, 0), func(tview.Primitive) {})
	testutil.Equal(t, m.Canceled(), true)
	testutil.Equal(t, m.Confirmed(), false)
}

// TestMergeSafetyPopup_ScanningStateWhenEmpty covers "First open triggers
// classification with a visible wait state".
func TestMergeSafetyPopup_ScanningStateWhenEmpty(t *testing.T) {
	m := NewMergeSafetyPopup(" Cleanup ", nil)
	m.SetScanning(true)
	out := drawMergeSafetyPopupToString(t, m, 100, 24)
	testutil.Contains(t, out, "Scanning")
	// Still defaults to Clean safe even while empty/scanning.
	testutil.Equal(t, m.SelectedLabel(), "Clean safe")
}

// TestMergeSafetyPopup_NoCandidatesMessage covers the non-scanning empty case
// (e.g. an empty backlog).
func TestMergeSafetyPopup_NoCandidatesMessage(t *testing.T) {
	m := NewMergeSafetyPopup(" Cleanup ", nil)
	out := drawMergeSafetyPopupToString(t, m, 100, 24)
	testutil.Contains(t, out, "No candidates")
}

// TestMergeSafetyPopup_SetCandidatesUpdatesLive covers the global Cleanup
// action's poll-and-refresh flow: candidates set after construction (e.g. on
// a poll tick) replace the rows and clear the scanning message once non-empty.
func TestMergeSafetyPopup_SetCandidatesUpdatesLive(t *testing.T) {
	m := NewMergeSafetyPopup(" Cleanup ", nil)
	m.SetScanning(true)
	out := drawMergeSafetyPopupToString(t, m, 100, 24)
	testutil.Contains(t, out, "Scanning")

	m.SetCandidates(sampleMergeSafetyCandidates())
	m.SetScanning(false)
	out = drawMergeSafetyPopupToString(t, m, 100, 24)
	testutil.Contains(t, out, "alpha-task")
	testutil.Contains(t, out, "beta-task")
	if strings.Contains(out, "Scanning for stuck tasks") {
		t.Errorf("expected scanning message to clear once candidates arrive; got:\n%s", out)
	}
}

// TestMergeSafetyPopup_SoleSafeCandidateScopeEquivalence documents the n=1
// design decision (design.md): at a single SAFE candidate, Clean safe and
// Clean all both resolve to a scope that would act on it — the caller (not
// the widget) implements the "equivalent at n=1" behavior, but the widget
// must not special-case n=1 itself.
func TestMergeSafetyPopup_SoleSafeCandidateScopeEquivalence(t *testing.T) {
	sole := []mergeSafetyCandidate{{TaskID: "t1", Name: "solo-task", Safe: true}}
	m := NewMergeSafetyPopup(" Nuke solo-task? ", sole)
	testutil.Equal(t, len(m.Candidates()), 1)
	testutil.Equal(t, m.SelectedLabel(), "Clean safe")
}

// --- BUG 1 (fix-hera-reclaim-status): the popup must never render an
// unclassified candidate as though it were a confirmed NOT-SAFE verdict, and
// should show real "X of Y classified" progress while a compute pass is
// still in flight. ---

// TestMergeSafetyPopup_PendingCandidatesGetTheirOwnSection covers the core
// framing bug: opening the popup over a freshly-computed backlog used to
// render every not-yet-classified task under "NOT-SAFE (N)" from tick zero,
// implying N confirmed-unsafe verdicts when zero classification had actually
// happened. Pending candidates now get their own PENDING section instead.
func TestMergeSafetyPopup_PendingCandidatesGetTheirOwnSection(t *testing.T) {
	cands := []mergeSafetyCandidate{
		{TaskID: "p1", Name: "pending-task", Pending: true, Reason: "not yet classified"},
		{TaskID: "n1", Name: "unsafe-task", Safe: false, Reason: "no matching merged pull request found"},
	}
	m := NewMergeSafetyPopup(" Cleanup ", cands)
	out := drawMergeSafetyPopupToString(t, m, 100, 24)

	pendingIdx := strings.Index(out, "PENDING (1)")
	notSafeIdx := strings.Index(out, "NOT-SAFE (1)")
	if pendingIdx < 0 {
		t.Fatalf("expected a PENDING (1) section header; got:\n%s", out)
	}
	if notSafeIdx < 0 {
		t.Fatalf("expected NOT-SAFE (1) to count only the genuinely-classified unsafe task; got:\n%s", out)
	}
	if pendingIdx > notSafeIdx {
		t.Errorf("expected PENDING section before NOT-SAFE; got PENDING at %d, NOT-SAFE at %d", pendingIdx, notSafeIdx)
	}
	testutil.Contains(t, out, "pending-task")
	testutil.Contains(t, out, "unsafe-task")
}

// TestMergeSafetyPopup_PendingCandidateNeverInflatesNotSafeCount is the
// sharpest regression guard for the framing bug: a pending-only candidate
// set must show ZERO NOT-SAFE entries, not a misleading "NOT-SAFE (N)" for
// tasks nobody has actually checked yet.
func TestMergeSafetyPopup_PendingCandidateNeverInflatesNotSafeCount(t *testing.T) {
	cands := []mergeSafetyCandidate{
		{TaskID: "p1", Name: "pending-one", Pending: true, Reason: "not yet classified"},
		{TaskID: "p2", Name: "pending-two", Pending: true, Reason: "not yet classified"},
	}
	m := NewMergeSafetyPopup(" Cleanup ", cands)
	out := drawMergeSafetyPopupToString(t, m, 100, 24)

	testutil.Contains(t, out, "PENDING (2)")
	if strings.Contains(out, "NOT-SAFE") {
		t.Errorf("expected no NOT-SAFE section for purely-pending candidates; got:\n%s", out)
	}
}

// TestMergeSafetyPopup_ScanningFooterShowsClassifiedProgress covers the
// "ideally show real progress" ask: while scanning, the footer reports how
// many of the known candidates have actually been classified so far.
func TestMergeSafetyPopup_ScanningFooterShowsClassifiedProgress(t *testing.T) {
	cands := []mergeSafetyCandidate{
		{TaskID: "p1", Name: "pending-one", Pending: true, Reason: "not yet classified"},
		{TaskID: "n1", Name: "unsafe-task", Safe: false, Reason: "no matching merged pull request found"},
		{TaskID: "s1", Name: "safe-task", Safe: true, Reason: "confirmed merged"},
	}
	m := NewMergeSafetyPopup(" Cleanup ", cands)
	m.SetScanning(true)
	out := drawMergeSafetyPopupToString(t, m, 100, 24)
	testutil.Contains(t, out, "2 of 3 classified")
}

// TestMergeSafetyPopup_ScanningFooterOmitsProgressBeforeFirstCandidates
// covers the very-first-tick case (before the first poll response has even
// arrived): with zero candidates known yet, the footer must not claim "0 of
// 0 classified" alongside the body's own "Scanning for stuck tasks…"
// message.
func TestMergeSafetyPopup_ScanningFooterOmitsProgressBeforeFirstCandidates(t *testing.T) {
	m := NewMergeSafetyPopup(" Cleanup ", nil)
	m.SetScanning(true)
	out := drawMergeSafetyPopupToString(t, m, 100, 24)
	if strings.Contains(out, "of 0 classified") {
		t.Errorf("expected no classified-count claim with zero known candidates; got:\n%s", out)
	}
	testutil.Contains(t, out, "Scanning")
}

// --- BUG 2 (fix-hera-reclaim-status): mouse-wheel scroll was a complete
// no-op (no MouseHandler at all), and the keyboard scroll's own clamp left a
// dead zone once scrolled past the point where the last screenful fills the
// body. ---

// manyNotSafeCandidates builds n distinct NOT-SAFE candidates, named so each
// row is uniquely identifiable in rendered output.
func manyNotSafeCandidates(n int) []mergeSafetyCandidate {
	cands := make([]mergeSafetyCandidate, n)
	for i := range cands {
		cands[i] = mergeSafetyCandidate{TaskID: fmt.Sprintf("t%02d", i), Name: fmt.Sprintf("task-%02d", i), Safe: false, Reason: "not safe"}
	}
	return cands
}

// TestMergeSafetyPopup_MouseWheelScrollsDown covers BUG 2's core defect: the
// widget previously had no MouseHandler at all, so wheel scroll was a
// complete no-op. One notch of MouseScrollDown must move the viewport
// exactly like one KeyDown press.
func TestMergeSafetyPopup_MouseWheelScrollsDown(t *testing.T) {
	m := NewMergeSafetyPopup(" Cleanup ", manyNotSafeCandidates(30))
	m.SetRect(0, 0, 100, 24)

	// At scrollOff=0 the top row is the "NOT-SAFE (30)" header, not task-00
	// (the header takes the first slot); it's the row that must scroll out
	// of view after exactly one notch.
	before := drawMergeSafetyPopupToString(t, m, 100, 24)
	testutil.Contains(t, before, "NOT-SAFE (30)")

	mh := m.MouseHandler()
	consumed, _ := mh(tview.MouseScrollDown, tcell.NewEventMouse(1, 1, tcell.ButtonNone, tcell.ModNone), func(tview.Primitive) {})
	testutil.Equal(t, consumed, true)

	after := drawMergeSafetyPopupToString(t, m, 100, 24)
	if strings.Contains(after, "NOT-SAFE (30)") {
		t.Errorf("expected the header row to have scrolled out of view after one wheel notch; got:\n%s", after)
	}
	testutil.Contains(t, after, "task-14") // one further row now fits at the bottom
}

// TestMergeSafetyPopup_MouseWheelScrollsUp covers the reverse direction, and
// that it stops at the top (scrollOff can't go negative).
func TestMergeSafetyPopup_MouseWheelScrollsUp(t *testing.T) {
	m := NewMergeSafetyPopup(" Cleanup ", manyNotSafeCandidates(30))
	m.SetRect(0, 0, 100, 24)
	mh := m.MouseHandler()

	// Already at the top: scrolling up must be a no-op on content, but still
	// consumed (mirrors the keyboard KeyUp bound-check).
	consumed, _ := mh(tview.MouseScrollUp, tcell.NewEventMouse(1, 1, tcell.ButtonNone, tcell.ModNone), func(tview.Primitive) {})
	testutil.Equal(t, consumed, true)
	testutil.Contains(t, drawMergeSafetyPopupToString(t, m, 100, 24), "task-00")

	// Scroll down a few notches, then back up — must return to the top.
	for range 3 {
		mh(tview.MouseScrollDown, tcell.NewEventMouse(1, 1, tcell.ButtonNone, tcell.ModNone), func(tview.Primitive) {})
	}
	for range 3 {
		mh(tview.MouseScrollUp, tcell.NewEventMouse(1, 1, tcell.ButtonNone, tcell.ModNone), func(tview.Primitive) {})
	}
	testutil.Contains(t, drawMergeSafetyPopupToString(t, m, 100, 24), "task-00")
}

// TestMergeSafetyPopup_MouseWheelOutsideRectIsNoOp mirrors Rail's own
// InRect guard: a scroll event whose position falls outside the popup's
// current rect must not be consumed or move the viewport.
func TestMergeSafetyPopup_MouseWheelOutsideRectIsNoOp(t *testing.T) {
	m := NewMergeSafetyPopup(" Cleanup ", manyNotSafeCandidates(30))
	m.SetRect(0, 0, 100, 24)
	mh := m.MouseHandler()

	consumed, _ := mh(tview.MouseScrollDown, tcell.NewEventMouse(500, 500, tcell.ButtonNone, tcell.ModNone), func(tview.Primitive) {})
	testutil.Equal(t, consumed, false)
	testutil.Contains(t, drawMergeSafetyPopupToString(t, m, 100, 24), "task-00")
}

// TestMergeSafetyPopup_ScrollToBottomFillsViewport is the keyboard-scroll
// render-correctness check the mission asked for explicitly: the OLD clamp
// (len(rows)-1, the last row INDEX rather than "rows that still fit") meant
// scrolling to the bottom of a list taller than the visible body left only
// the single final row rendered, with the rest of the body blank. With 30
// NOT-SAFE candidates (31 rows: 1 header + 30 items) in a 24-row popup (body
// height 15), the corrected max offset is 31-15=16, so scrolling past it
// must still render a FULL screenful ending on the last candidate.
func TestMergeSafetyPopup_ScrollToBottomFillsViewport(t *testing.T) {
	m := NewMergeSafetyPopup(" Cleanup ", manyNotSafeCandidates(30))
	h := m.InputHandler()
	for range 100 { // scroll far past the bottom
		h(tcell.NewEventKey(tcell.KeyDown, 0, 0), func(tview.Primitive) {})
	}

	out := drawMergeSafetyPopupToString(t, m, 100, 24)
	lines := strings.Split(out, "\n")

	firstContentLine := -1
	lastContentLine := -1
	for i, line := range lines {
		if strings.Contains(line, "task-") {
			if firstContentLine == -1 {
				firstContentLine = i
			}
			lastContentLine = i
		}
	}
	if firstContentLine == -1 {
		t.Fatalf("expected candidate rows to be visible after scrolling to bottom; got:\n%s", out)
	}
	// The body window is 15 rows tall; a fully-filled screenful scrolled to
	// the bottom must show 15 consecutive candidate rows ending on task-29 —
	// the OLD bug rendered exactly ONE row (task-29) with 14 blank rows
	// trailing it inside the same window instead.
	testutil.Equal(t, lastContentLine-firstContentLine+1, 15)
	testutil.Contains(t, out, "task-29")
	testutil.Contains(t, out, "task-15") // first of the final 15-row screenful
}

// TestMergeSafetyPopup_ScrollUpAfterOvershootMovesImmediately proves the
// "dead zone" is gone: after scrolling down far past the true bottom (which
// used to leave m.scrollOff sitting well beyond the point drawRows actually
// needs), a render pass self-heals the field, so a SINGLE subsequent KeyUp
// immediately moves the visible window — no repeated "dead" presses needed
// first.
func TestMergeSafetyPopup_ScrollUpAfterOvershootMovesImmediately(t *testing.T) {
	m := NewMergeSafetyPopup(" Cleanup ", manyNotSafeCandidates(30))
	h := m.InputHandler()
	for range 100 {
		h(tcell.NewEventKey(tcell.KeyDown, 0, 0), func(tview.Primitive) {})
	}
	// Render once so the overshot scrollOff self-heals to the true max (16),
	// mirroring the real app where a redraw follows every keystroke.
	atBottom := drawMergeSafetyPopupToString(t, m, 100, 24)
	testutil.Contains(t, atBottom, "task-15") // top of the final full screenful

	h(tcell.NewEventKey(tcell.KeyUp, 0, 0), func(tview.Primitive) {})
	afterOneUp := drawMergeSafetyPopupToString(t, m, 100, 24)
	testutil.Contains(t, afterOneUp, "task-14") // the row that just scrolled into view
	if strings.Contains(afterOneUp, "task-29") {
		// task-29 (the bottom-most row before this KeyUp) must have scrolled
		// out — under the old "dead zone" bug it would still be showing,
		// since the FIRST several KeyUp presses after an overshoot were
		// no-ops on the rendered content.
		t.Errorf("expected the view to move after a single KeyUp; got:\n%s", afterOneUp)
	}
}
