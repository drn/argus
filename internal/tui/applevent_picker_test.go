package tui

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/drn/argus/internal/macapps"
	"github.com/drn/argus/internal/testutil"
)

func sampleApps() []macapps.App {
	return []macapps.App{
		{Name: "Finder", BundleID: "com.apple.finder", Scriptable: true},
		{Name: "Messages", BundleID: "com.apple.MobileSMS", Scriptable: true},
		{Name: "Music", BundleID: "com.apple.Music", Scriptable: true},
		{Name: "Safari", BundleID: "com.apple.Safari", Scriptable: true},
		{Name: "Terminal", BundleID: "com.apple.Terminal", Scriptable: true},
	}
}

// sendRune dispatches a printable rune key event through the modal's input
// handler. Passing nil for the setFocus callback is fine — the picker never
// transfers focus.
func sendRune(m *AppleEventsPickerModal, r rune) {
	m.InputHandler()(tcell.NewEventKey(tcell.KeyRune, r, tcell.ModNone), nil)
}

// sendKeyOnly dispatches a non-rune key (Enter, Esc, arrows, etc.).
func sendKeyOnly(m *AppleEventsPickerModal, key tcell.Key) {
	m.InputHandler()(tcell.NewEventKey(key, 0, tcell.ModNone), nil)
}

func TestAppleEventsPicker_NewPreselectsExistingValues(t *testing.T) {
	m := NewAppleEventsPickerModal("forge", sampleApps(), []string{"com.apple.MobileSMS", "com.apple.finder"})
	// Both should be present in the selected set.
	if _, ok := m.selected["com.apple.MobileSMS"]; !ok {
		t.Error("expected com.apple.MobileSMS preselected")
	}
	if _, ok := m.selected["com.apple.finder"]; !ok {
		t.Error("expected com.apple.finder preselected")
	}
	testutil.Equal(t, len(m.selected), 2)
}

func TestAppleEventsPicker_NewIgnoresEmptyAndWhitespacePreselects(t *testing.T) {
	m := NewAppleEventsPickerModal("forge", sampleApps(), []string{"", "  ", "com.apple.finder"})
	testutil.Equal(t, len(m.selected), 1)
	if _, ok := m.selected["com.apple.finder"]; !ok {
		t.Error("expected only valid preselect to survive")
	}
}

func TestAppleEventsPicker_FilterMatchesNameCaseInsensitive(t *testing.T) {
	m := NewAppleEventsPickerModal("forge", sampleApps(), nil)
	for _, r := range "MESS" {
		sendRune(m, r)
	}
	testutil.Equal(t, len(m.rows), 1)
	testutil.Equal(t, m.rows[0].App.BundleID, "com.apple.MobileSMS")
}

func TestAppleEventsPicker_FilterMatchesBundleID(t *testing.T) {
	m := NewAppleEventsPickerModal("forge", sampleApps(), nil)
	for _, r := range "MobileSMS" {
		sendRune(m, r)
	}
	testutil.Equal(t, len(m.rows), 1)
	testutil.Equal(t, m.rows[0].App.Name, "Messages")
}

func TestAppleEventsPicker_BackspaceShrinksFilter(t *testing.T) {
	m := NewAppleEventsPickerModal("forge", sampleApps(), nil)
	for _, r := range "music" {
		sendRune(m, r)
	}
	testutil.Equal(t, len(m.rows), 1)
	sendKeyOnly(m, tcell.KeyBackspace)
	// After deleting 'c', filter is "musi" — still only Music matches.
	testutil.Equal(t, len(m.rows), 1)
	// Delete enough chars to clear filter — all 5 apps return.
	for range 4 {
		sendKeyOnly(m, tcell.KeyBackspace)
	}
	testutil.Equal(t, len(m.rows), 5)
}

func TestAppleEventsPicker_SpaceTogglesCursorRow(t *testing.T) {
	m := NewAppleEventsPickerModal("forge", sampleApps(), nil)
	// Cursor starts at row 0 (Finder).
	testutil.Equal(t, m.cursor, 0)
	testutil.Equal(t, m.rows[0].App.Name, "Finder")

	sendRune(m, ' ')
	if _, ok := m.selected["com.apple.finder"]; !ok {
		t.Error("expected Finder selected after space")
	}

	sendRune(m, ' ')
	if _, ok := m.selected["com.apple.finder"]; ok {
		t.Error("expected Finder deselected after second space")
	}
}

func TestAppleEventsPicker_ArrowsAndJKNavigate(t *testing.T) {
	m := NewAppleEventsPickerModal("forge", sampleApps(), nil)
	testutil.Equal(t, m.cursor, 0)

	sendKeyOnly(m, tcell.KeyDown)
	testutil.Equal(t, m.cursor, 1)

	sendKeyOnly(m, tcell.KeyDown)
	testutil.Equal(t, m.cursor, 2)

	sendKeyOnly(m, tcell.KeyUp)
	testutil.Equal(t, m.cursor, 1)

	// Cursor clamps at top.
	sendKeyOnly(m, tcell.KeyUp)
	sendKeyOnly(m, tcell.KeyUp)
	testutil.Equal(t, m.cursor, 0)

	// Cursor clamps at bottom.
	for range 10 {
		sendKeyOnly(m, tcell.KeyDown)
	}
	testutil.Equal(t, m.cursor, len(m.rows)-1)
}

func TestAppleEventsPicker_HomeAndEnd(t *testing.T) {
	m := NewAppleEventsPickerModal("forge", sampleApps(), nil)
	sendKeyOnly(m, tcell.KeyEnd)
	testutil.Equal(t, m.cursor, len(m.rows)-1)
	sendKeyOnly(m, tcell.KeyHome)
	testutil.Equal(t, m.cursor, 0)
}

func TestAppleEventsPicker_EnterConfirms(t *testing.T) {
	m := NewAppleEventsPickerModal("forge", sampleApps(), nil)
	sendKeyOnly(m, tcell.KeyEnter)
	testutil.Equal(t, m.Done(), true)
	testutil.Equal(t, m.Canceled(), false)
}

func TestAppleEventsPicker_EscapeCancels(t *testing.T) {
	m := NewAppleEventsPickerModal("forge", sampleApps(), nil)
	sendKeyOnly(m, tcell.KeyEscape)
	testutil.Equal(t, m.Canceled(), true)
	testutil.Equal(t, m.Done(), false)
}

func TestAppleEventsPicker_CtrlQCancels(t *testing.T) {
	m := NewAppleEventsPickerModal("forge", sampleApps(), nil)
	sendKeyOnly(m, tcell.KeyCtrlQ)
	testutil.Equal(t, m.Canceled(), true)
}

func TestAppleEventsPicker_ResultIsSorted(t *testing.T) {
	m := NewAppleEventsPickerModal("forge", sampleApps(), []string{"com.apple.Safari", "com.apple.finder", "com.apple.MobileSMS"})
	got := m.Result()
	// Sorted alphabetically by bundle ID for deterministic CSV writes.
	want := []string{"com.apple.MobileSMS", "com.apple.Safari", "com.apple.finder"}
	testutil.DeepEqual(t, got, want)
}

func TestAppleEventsPicker_CustomEntryAppearsForUnknownValidID(t *testing.T) {
	// The Messages.app legacy AppleEvent target — com.apple.iChat — has no
	// .app bundle on disk, so the picker must offer it as a custom entry
	// when the user types it. This is the core gotcha the picker was
	// designed to handle.
	m := NewAppleEventsPickerModal("forge", sampleApps(), nil)
	for _, r := range "com.apple.iChat" {
		sendRune(m, r)
	}
	// No app matches, but the query is a valid bundle ID → synthetic row.
	if len(m.rows) != 1 {
		t.Fatalf("expected 1 row (custom entry), got %d: %#v", len(m.rows), m.rows)
	}
	if !m.rows[0].IsCustom {
		t.Errorf("expected row to be custom, got %#v", m.rows[0])
	}
	testutil.Equal(t, m.rows[0].BundleID, "com.apple.iChat")

	// Space toggles it just like a real app row.
	sendRune(m, ' ')
	if _, ok := m.selected["com.apple.iChat"]; !ok {
		t.Error("expected custom-entry bundle ID in selected set")
	}
}

func TestAppleEventsPicker_CustomEntryNotShownWhenInvalidQuery(t *testing.T) {
	m := NewAppleEventsPickerModal("forge", sampleApps(), nil)
	for _, r := range "zzz nope" {
		sendRune(m, r)
	}
	// Query has a space → not a valid bundle ID → no custom-entry row.
	// Also matches no app. Result: empty rows.
	testutil.Equal(t, len(m.rows), 0)
}

func TestAppleEventsPicker_CustomEntryHiddenWhenScannedAppMatchesExactly(t *testing.T) {
	m := NewAppleEventsPickerModal("forge", sampleApps(), nil)
	for _, r := range "com.apple.finder" {
		sendRune(m, r)
	}
	// Typing the exact bundle ID of a scanned app must NOT add a duplicate
	// custom entry — only the real app row should appear.
	testutil.Equal(t, len(m.rows), 1)
	testutil.Equal(t, m.rows[0].IsCustom, false)
	testutil.Equal(t, m.rows[0].App.Name, "Finder")
}

func TestAppleEventsPicker_SelectedFilteredOutRowsRemainVisible(t *testing.T) {
	// Toggle Music selected, then filter to "fin" — Music must STILL appear
	// in the list (as an extra row) so the user can deselect it without
	// first clearing the filter.
	m := NewAppleEventsPickerModal("forge", sampleApps(), nil)
	// Move cursor to Music (sampleApps sorted: Finder, Messages, Music, Safari, Terminal).
	sendKeyOnly(m, tcell.KeyDown)
	sendKeyOnly(m, tcell.KeyDown)
	testutil.Equal(t, m.rows[m.cursor].App.Name, "Music")
	sendRune(m, ' ')
	if _, ok := m.selected["com.apple.Music"]; !ok {
		t.Fatal("expected Music selected")
	}
	// Filter to "fin" — only Finder normally matches, but Music must remain visible.
	for _, r := range "fin" {
		sendRune(m, r)
	}
	var foundMusic bool
	for _, row := range m.rows {
		if row.BundleID == "com.apple.Music" {
			foundMusic = true
		}
	}
	if !foundMusic {
		t.Errorf("selected Music must remain visible under filter; rows=%#v", m.rows)
	}
}

func TestAppleEventsPicker_SetAppsRefreshesList(t *testing.T) {
	m := NewAppleEventsPickerModal("forge", sampleApps(), nil)
	testutil.Equal(t, len(m.rows), 5)

	m.SetApps([]macapps.App{
		{Name: "Calculator", BundleID: "com.apple.calculator"},
	})
	testutil.Equal(t, len(m.rows), 1)
	testutil.Equal(t, m.rows[0].App.Name, "Calculator")
}

func TestAppleEventsPicker_PasteHandlerAppendsToFilter(t *testing.T) {
	m := NewAppleEventsPickerModal("forge", sampleApps(), nil)
	paste := m.PasteHandler()
	// Newlines/carriage returns must be stripped to keep the filter single-line.
	paste("mess\nlines\rhere", nil)
	if string(m.filter) != "messlineshere" {
		t.Errorf("filter = %q, want %q", string(m.filter), "messlineshere")
	}
}

// TestAppleEventsPicker_DrawDoesNotPanic_VariousSizes is a smoke test that
// the Draw path handles tiny / typical / huge viewport sizes without
// panicking. Per CLAUDE.md, major UI paths need a smoke test that exercises
// the real Draw against a SimulationScreen.
func TestAppleEventsPicker_DrawDoesNotPanic_VariousSizes(t *testing.T) {
	cases := []struct {
		name string
		w, h int
	}{
		{"tiny", 30, 5},
		{"typical", 80, 24},
		{"large", 200, 60},
		{"zero", 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := NewAppleEventsPickerModal("forge", sampleApps(), []string{"com.apple.finder"})
			m.SetRect(0, 0, tc.w, tc.h)
			screen := tcell.NewSimulationScreen("")
			testutil.NoError(t, screen.Init())
			screen.SetSize(tc.w, tc.h)
			m.Draw(screen)
		})
	}
}

// TestAppleEventsPicker_DrawRendersExpectedContent reads back simulation
// screen contents to confirm the title, filter, checkboxes, and bundle IDs
// land on screen. Catches regressions where the layout math accidentally
// hides selected rows or the title.
func TestAppleEventsPicker_DrawRendersExpectedContent(t *testing.T) {
	m := NewAppleEventsPickerModal("forge", sampleApps(), []string{"com.apple.finder"})
	for _, r := range "mess" {
		sendRune(m, r)
	}
	m.SetRect(0, 0, 100, 30)

	screen := tcell.NewSimulationScreen("")
	testutil.NoError(t, screen.Init())
	screen.SetSize(100, 30)
	m.Draw(screen)

	// Read back the screen as a string.
	var lines []string
	for row := range 30 {
		var b strings.Builder
		for col := range 100 {
			s, _, _ := screen.Get(col, row)
			b.WriteString(s)
		}
		lines = append(lines, b.String())
	}
	out := strings.Join(lines, "\n")

	testutil.Contains(t, out, "AppleEvents Allowlist")
	testutil.Contains(t, out, "forge")
	testutil.Contains(t, out, "Filter: mess")
	testutil.Contains(t, out, "Messages")
	testutil.Contains(t, out, "com.apple.MobileSMS")
	// Pre-selected Finder should still appear despite the filter (it survives
	// filter-out via the selected-but-hidden visibility rule).
	testutil.Contains(t, out, "com.apple.finder")
	// Help footer.
	testutil.Contains(t, out, "space toggle")
}

// TestAppleEventsPicker_DrawRendersCustomEntryRow covers the Messages-iChat
// scenario explicitly: typing the legacy bundle ID surfaces an "Add custom"
// row that the user can toggle.
func TestAppleEventsPicker_DrawRendersCustomEntryRow(t *testing.T) {
	m := NewAppleEventsPickerModal("forge", sampleApps(), nil)
	for _, r := range "com.apple.iChat" {
		sendRune(m, r)
	}
	m.SetRect(0, 0, 100, 20)
	screen := tcell.NewSimulationScreen("")
	testutil.NoError(t, screen.Init())
	screen.SetSize(100, 20)
	m.Draw(screen)

	var lines []string
	for row := range 20 {
		var b strings.Builder
		for col := range 100 {
			s, _, _ := screen.Get(col, row)
			b.WriteString(s)
		}
		lines = append(lines, b.String())
	}
	out := strings.Join(lines, "\n")
	testutil.Contains(t, out, "Add custom")
	testutil.Contains(t, out, "com.apple.iChat")
}

// TestAppleEventsPicker_RoundTripEmptySelections defends the no-edit path:
// open the modal, confirm without toggling anything, result must equal the
// preselected set (sorted). Pinning prevents a regression where Confirm
// silently re-orders or drops entries.
func TestAppleEventsPicker_RoundTripEmptySelections(t *testing.T) {
	pre := []string{"com.apple.MobileSMS", "com.apple.iChat", "com.apple.finder"}
	m := NewAppleEventsPickerModal("forge", sampleApps(), pre)
	sendKeyOnly(m, tcell.KeyEnter)
	testutil.Equal(t, m.Done(), true)
	got := m.Result()
	// Returned in alphabetical bundle-ID order.
	want := []string{"com.apple.MobileSMS", "com.apple.finder", "com.apple.iChat"}
	testutil.DeepEqual(t, got, want)
}
