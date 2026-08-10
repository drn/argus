package tui

import (
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
