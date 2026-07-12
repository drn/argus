package widget

import (
	"testing"
	"time"

	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
	"github.com/gdamore/tcell/v2"
)

func TestStatusBar_InfoMessage(t *testing.T) {
	sb := NewStatusBar()
	sb.SetRect(0, 0, 80, 1)

	screen := tcell.NewSimulationScreen("UTF-8")
	testutil.NoError(t, screen.Init())
	screen.SetSize(80, 1)
	defer screen.Fini()

	// SetInfo shows the message on screen.
	sb.SetInfo("Creating worktree…")
	sb.Draw(screen)
	screen.Show()
	testutil.Contains(t, readScreenRow(screen, 0, 80), "Creating worktree…")

	// ClearInfo restores task counts.
	sb.ClearInfo()
	sb.Draw(screen)
	screen.Show()
	row := readScreenRow(screen, 0, 80)
	testutil.Contains(t, row, "0 active")
}

func TestStatusBar_ErrorTakesPrecedenceOverInfo(t *testing.T) {
	sb := NewStatusBar()
	sb.SetTasks([]*model.Task{
		{Status: model.StatusPending},
	})

	screen := tcell.NewSimulationScreen("UTF-8")
	testutil.NoError(t, screen.Init())
	screen.SetSize(80, 1)

	// Info message shows when no error.
	sb.SetInfo("working…")
	sb.SetRect(0, 0, 80, 1)
	sb.Draw(screen)
	screen.Show()
	content := readScreenRow(screen, 0, 80)
	testutil.Contains(t, content, "working…")

	// Error takes precedence over info.
	sb.SetError("something broke")
	sb.Draw(screen)
	screen.Show()
	content = readScreenRow(screen, 0, 80)
	testutil.Contains(t, content, "something broke")

	// Clearing error reveals info again.
	sb.ClearError()
	sb.Draw(screen)
	screen.Show()
	content = readScreenRow(screen, 0, 80)
	testutil.Contains(t, content, "working…")

	screen.Fini()
}

// TestStatusBar_ErrorAutoExpires asserts that an error notice set via SetError
// reverts to the default task counts once StatusNoticeTTL has elapsed, without
// any ClearError call (BUG-030). Uses an injectable clock — no real sleep.
func TestStatusBar_ErrorAutoExpires(t *testing.T) {
	sb := NewStatusBar()
	sb.SetRect(0, 0, 80, 1)
	base := time.Unix(1000, 0)
	sb.now = func() time.Time { return base }

	screen := tcell.NewSimulationScreen("UTF-8")
	testutil.NoError(t, screen.Init())
	screen.SetSize(80, 1)
	defer screen.Fini()

	// Notice shows instantly.
	sb.SetError("J: select a freelancer or a coordinator to adopt")
	sb.Draw(screen)
	screen.Show()
	testutil.Contains(t, readScreenRow(screen, 0, 80), "select a freelancer")

	// Still showing just before expiry.
	sb.now = func() time.Time { return base.Add(StatusNoticeTTL - time.Second) }
	sb.Draw(screen)
	screen.Show()
	testutil.Contains(t, readScreenRow(screen, 0, 80), "select a freelancer")

	// Past expiry: reverts to default counts on its own (no ClearError).
	sb.now = func() time.Time { return base.Add(StatusNoticeTTL + time.Second) }
	sb.Draw(screen)
	screen.Show()
	row := readScreenRow(screen, 0, 80)
	testutil.Contains(t, row, "0 active")
	if contains(row, "select a freelancer") {
		t.Fatalf("error notice did not auto-expire: %q", row)
	}
	// Accessor reflects the expiry too.
	testutil.Equal(t, sb.Error(), "")
}

// TestStatusBar_InfoAutoExpires mirrors the error case for SetInfo.
func TestStatusBar_InfoAutoExpires(t *testing.T) {
	sb := NewStatusBar()
	sb.SetRect(0, 0, 80, 1)
	base := time.Unix(2000, 0)
	sb.now = func() time.Time { return base }

	screen := tcell.NewSimulationScreen("UTF-8")
	testutil.NoError(t, screen.Init())
	screen.SetSize(80, 1)
	defer screen.Fini()

	sb.SetInfo("Forked (remote: source context not carried)")
	sb.Draw(screen)
	screen.Show()
	testutil.Contains(t, readScreenRow(screen, 0, 80), "Forked")

	sb.now = func() time.Time { return base.Add(StatusNoticeTTL + time.Second) }
	sb.Draw(screen)
	screen.Show()
	row := readScreenRow(screen, 0, 80)
	testutil.Contains(t, row, "0 active")
	if contains(row, "Forked") {
		t.Fatalf("info notice did not auto-expire: %q", row)
	}
	testutil.Equal(t, sb.Info(), "")
}

// TestStatusBar_SecondNoticeResetsExpiry asserts the expiry window is reset
// (not stacked, not cleared early) when a second notice arrives mid-window:
// the second notice lives a full StatusNoticeTTL from when IT was set.
func TestStatusBar_SecondNoticeResetsExpiry(t *testing.T) {
	sb := NewStatusBar()
	sb.SetRect(0, 0, 80, 1)
	base := time.Unix(3000, 0)
	sb.now = func() time.Time { return base }

	screen := tcell.NewSimulationScreen("UTF-8")
	testutil.NoError(t, screen.Init())
	screen.SetSize(80, 1)
	defer screen.Fini()

	sb.SetError("first")

	// Halfway through the first window, set a second notice — resets the clock.
	half := base.Add(StatusNoticeTTL / 2)
	sb.now = func() time.Time { return half }
	sb.SetError("second")

	// At base+TTL+1s the FIRST window would have lapsed, but the second notice
	// was set at base+TTL/2 so it is still well within its own window.
	sb.now = func() time.Time { return base.Add(StatusNoticeTTL + time.Second) }
	sb.Draw(screen)
	screen.Show()
	testutil.Contains(t, readScreenRow(screen, 0, 80), "second")

	// Only after a full TTL past the SECOND set does it expire.
	sb.now = func() time.Time { return half.Add(StatusNoticeTTL + time.Second) }
	sb.Draw(screen)
	screen.Show()
	row := readScreenRow(screen, 0, 80)
	if contains(row, "second") {
		t.Fatalf("second notice did not honor its reset window: %q", row)
	}
}

func TestStatusBar_PluginMode_RendersBarHintsAndExitHint(t *testing.T) {
	sb := NewStatusBar()
	sb.SetRect(0, 0, 80, 1)

	screen := tcell.NewSimulationScreen("UTF-8")
	testutil.NoError(t, screen.Init())
	screen.SetSize(80, 1)
	defer screen.Fini()

	sb.SetPluginMode(true, "Ludwig", []PluginHint{
		{Key: "j", Label: "down"},
		{Key: "k", Label: "up"},
	})
	sb.Draw(screen)
	screen.Show()
	row := readScreenRow(screen, 0, 80)
	testutil.Contains(t, row, "j")
	testutil.Contains(t, row, "down")
	testutil.Contains(t, row, "k")
	testutil.Contains(t, row, "up")
	// Reserved exit hint always present.
	testutil.Contains(t, row, "^Q^Q")
	testutil.Contains(t, row, "argus")
}

func TestStatusBar_PluginMode_ExitHintSurvivesManyLongHints(t *testing.T) {
	sb := NewStatusBar()
	sb.SetRect(0, 0, 80, 1)

	screen := tcell.NewSimulationScreen("UTF-8")
	testutil.NoError(t, screen.Init())
	screen.SetSize(80, 1)
	defer screen.Fini()

	var hints []PluginHint
	for i := 0; i < 40; i++ {
		hints = append(hints, PluginHint{
			Key:   "Ctrl+Shift+F" + string(rune('0'+i%10)),
			Label: "do a very long thing number " + string(rune('0'+i%10)),
		})
	}
	sb.SetPluginMode(true, "Ludwig", hints)
	sb.Draw(screen)
	screen.Show()
	row := readScreenRow(screen, 0, 80)
	// Even with far more plugin hints than fit, the reserved exit hint is
	// never dropped or truncated away.
	testutil.Contains(t, row, "^Q^Q")
	testutil.Contains(t, row, "argus")
}

func TestStatusBar_PluginMode_EmptyHintsShowsAffordance(t *testing.T) {
	sb := NewStatusBar()
	sb.SetRect(0, 0, 80, 1)

	screen := tcell.NewSimulationScreen("UTF-8")
	testutil.NoError(t, screen.Init())
	screen.SetSize(80, 1)
	defer screen.Fini()

	sb.SetPluginMode(true, "Ludwig", nil)
	sb.Draw(screen)
	screen.Show()
	row := readScreenRow(screen, 0, 80)
	testutil.Contains(t, row, "Ludwig")
	testutil.Contains(t, row, "has the keyboard")
	// Exit hint still present in the fallback.
	testutil.Contains(t, row, "^Q^Q")
	testutil.Contains(t, row, "argus")
}

func TestStatusBar_PluginMode_OffRestoresTabHints(t *testing.T) {
	const w = 160
	sb := NewStatusBar()
	sb.SetRect(0, 0, w, 1)
	sb.SetTab(TabTasks)

	screen := tcell.NewSimulationScreen("UTF-8")
	testutil.NoError(t, screen.Init())
	screen.SetSize(w, 1)
	defer screen.Fini()

	// Enter plugin mode.
	sb.SetPluginMode(true, "Ludwig", []PluginHint{{Key: "j", Label: "down"}})
	sb.Draw(screen)
	screen.Show()
	row := readScreenRow(screen, 0, w)
	testutil.Contains(t, row, "^Q^Q")

	// Leave plugin mode — argus's own tab hints return.
	sb.SetPluginMode(false, "", nil)
	sb.Draw(screen)
	screen.Show()
	row = readScreenRow(screen, 0, w)
	testutil.Contains(t, row, "attach")
	testutil.Contains(t, row, "quit")
	if contains(row, "^Q^Q") {
		t.Fatalf("plugin exit hint leaked into tab hints: %q", row)
	}
}

func TestStatusBar_PluginMode_NarrowWidthClampsAndTruncatesAffordance(t *testing.T) {
	// A width narrower than the reserved exit hint forces the limit/rc clamps
	// and truncates the affordance to nothing. Must not panic or draw out of
	// bounds.
	const w = 4
	sb := NewStatusBar()
	sb.SetRect(0, 0, w, 1)

	screen := tcell.NewSimulationScreen("UTF-8")
	testutil.NoError(t, screen.Init())
	screen.SetSize(w, 1)
	defer screen.Fini()

	// Empty hints → affordance branch with a tiny width (truncates immediately).
	sb.SetPluginMode(true, "Ludwig", nil)
	sb.Draw(screen)
	screen.Show()

	// Non-empty hints → hint branch with a tiny width (limit clamp).
	sb.SetPluginMode(true, "Ludwig", []PluginHint{{Key: "j", Label: "down"}})
	sb.Draw(screen)
	screen.Show()
}

func TestStatusBar_PluginMode_Accessor(t *testing.T) {
	sb := NewStatusBar()
	sb.SetPluginMode(true, "Ludwig", []PluginHint{{Key: "j", Label: "down"}})
	active, title, hints := sb.PluginMode()
	testutil.Equal(t, active, true)
	testutil.Equal(t, title, "Ludwig")
	testutil.Equal(t, len(hints), 1)

	sb.SetPluginMode(false, "", nil)
	active, title, hints = sb.PluginMode()
	testutil.Equal(t, active, false)
	testutil.Equal(t, title, "")
	testutil.Equal(t, len(hints), 0)
}

func TestStatusBar_PluginMode_ZeroWidthIsNoOp(t *testing.T) {
	sb := NewStatusBar()
	sb.SetRect(0, 0, 0, 1)
	screen := tcell.NewSimulationScreen("UTF-8")
	testutil.NoError(t, screen.Init())
	screen.SetSize(1, 1)
	defer screen.Fini()
	sb.SetPluginMode(true, "Ludwig", []PluginHint{{Key: "j", Label: "down"}})
	sb.Draw(screen) // width<=0 → early return, no panic
}

// TestStatusBar_HeraFocus_RailHints asserts that SetHeraFocus(0) (rail focus)
// renders rail-specific mutation hints (spawn, filter, retire).
func TestStatusBar_HeraFocus_RailHints(t *testing.T) {
	const w = 240
	sb := NewStatusBar()
	sb.SetRect(0, 0, w, 1)
	sb.SetTab(TabHera)
	sb.SetHeraFocus(0) // 0 = FocusRail

	screen := tcell.NewSimulationScreen("UTF-8")
	testutil.NoError(t, screen.Init())
	screen.SetSize(w, 1)
	defer screen.Fini()

	sb.Draw(screen)
	screen.Show()
	row := readScreenRow(screen, 0, w)
	testutil.Contains(t, row, "spawn")
	testutil.Contains(t, row, "filter")
	testutil.Contains(t, row, "fold")
	testutil.Contains(t, row, "retire")
	// BUG-016: Left parent-nav must appear in the rail hint bar — fail if dropped.
	testutil.Contains(t, row, "parent")
}

// TestStatusBar_HeraFocus_PaneHints asserts that SetHeraFocus(1) (coord pane)
// renders pane hints (rail, scroll) and NOT rail-only keys (spawn, filter).
func TestStatusBar_HeraFocus_PaneHints(t *testing.T) {
	const w = 240
	sb := NewStatusBar()
	sb.SetRect(0, 0, w, 1)
	sb.SetTab(TabHera)
	sb.SetHeraFocus(1) // 1 = FocusCoord

	screen := tcell.NewSimulationScreen("UTF-8")
	testutil.NoError(t, screen.Init())
	screen.SetSize(w, 1)
	defer screen.Fini()

	sb.Draw(screen)
	screen.Show()
	row := readScreenRow(screen, 0, w)
	testutil.Contains(t, row, "rail")
	testutil.Contains(t, row, "scroll")
	if contains(row, "spawn") {
		t.Fatalf("rail key 'spawn' shown in pane hints: %q", row)
	}
	if contains(row, "filter") {
		t.Fatalf("rail key 'filter' shown in pane hints: %q", row)
	}
}

// TestStatusBar_HeraFocus_AgentPaneHints asserts that SetHeraFocus(2) (agent pane)
// shows the same pane hints as FocusCoord.
func TestStatusBar_HeraFocus_AgentPaneHints(t *testing.T) {
	const w = 240
	sb := NewStatusBar()
	sb.SetRect(0, 0, w, 1)
	sb.SetTab(TabHera)
	sb.SetHeraFocus(2) // 2 = FocusAgent

	screen := tcell.NewSimulationScreen("UTF-8")
	testutil.NoError(t, screen.Init())
	screen.SetSize(w, 1)
	defer screen.Fini()

	sb.Draw(screen)
	screen.Show()
	row := readScreenRow(screen, 0, w)
	testutil.Contains(t, row, "rail")
	testutil.Contains(t, row, "scroll")
}

// TestStatusBar_HeraFocus_DefaultIsRail asserts that a fresh StatusBar on the
// Hera tab (heraFocus zero value) shows rail hints.
func TestStatusBar_HeraFocus_DefaultIsRail(t *testing.T) {
	const w = 240
	sb := NewStatusBar()
	sb.SetRect(0, 0, w, 1)
	sb.SetTab(TabHera)
	// No SetHeraFocus call — should default to 0 (FocusRail).

	screen := tcell.NewSimulationScreen("UTF-8")
	testutil.NoError(t, screen.Init())
	screen.SetSize(w, 1)
	defer screen.Fini()

	sb.Draw(screen)
	screen.Show()
	row := readScreenRow(screen, 0, w)
	testutil.Contains(t, row, "spawn")
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// readScreenRow reads a row of cells from a SimulationScreen into a string.
func readScreenRow(screen tcell.SimulationScreen, row, width int) string {
	var runes []rune
	for col := 0; col < width; col++ {
		r, _, _, _ := screen.GetContent(col, row)
		runes = append(runes, r)
	}
	return string(runes)
}
