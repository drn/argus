package tui

import (
	"errors"
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/testutil"
	pluginsettings "github.com/drn/argus/internal/tui/settings"
)

const sectionBody = `{
	"title": "Hello",
	"callback_url": "http://127.0.0.1/save",
	"fields": [
		{"key":"enabled","label":"Enabled","type":"bool","default":false},
		{"key":"count","label":"Count","type":"int","min":0,"max":10,"default":1},
		{"key":"name","label":"Name","type":"string","default":"default"},
		{"key":"backend","label":"Backend","type":"enum","options":["claude","codex"],"default":"claude"}
	]
}`

func seedPluginSection(t *testing.T, d *db.DB, scope, title string) {
	t.Helper()
	sec, err := pluginsettings.ParseSection(scope, []byte(sectionBody))
	testutil.NoError(t, err)
	// Patch title so tests can register multiple sections under the same scope.
	if title != "" {
		sec.Title = title
	}
	row, err := db.FromSection(sec)
	testutil.NoError(t, err)
	_, err = d.UpsertPluginSection(row)
	testutil.NoError(t, err)
}

// seedStreamSection persists a stream-type plugin section. Uses the package
// boundary that matches what the daemon's POST handler builds at runtime.
func seedStreamSection(t *testing.T, d *db.DB, scope, title, callbackURL string) {
	t.Helper()
	sec := pluginsettings.Section{
		Scope:       scope,
		Title:       title,
		Type:        pluginsettings.TypeStream,
		CallbackURL: callbackURL,
	}
	row, err := db.FromSection(sec)
	testutil.NoError(t, err)
	_, err = d.UpsertPluginSection(row)
	testutil.NoError(t, err)
}

func TestSettingsView_PluginsHeaderHiddenByDefault(t *testing.T) {
	sv := testSettingsView(t)
	entries := sv.railEntries()
	for _, e := range entries {
		if e.kind == railPluginsHeader || e.kind == railSeparator {
			t.Fatal("plugins header / separator should be hidden when no plugin sections")
		}
	}
}

func TestSettingsView_PluginsHeaderShownWithSections(t *testing.T) {
	d, _ := db.OpenInMemory()
	t.Cleanup(func() { d.Close() }) //nolint:errcheck
	seedPluginSection(t, d, "alpha", "")
	sv := NewSettingsView(d)
	sv.Refresh()

	entries := sv.railEntries()
	var header, sep, plugin bool
	for _, e := range entries {
		switch e.kind {
		case railPluginsHeader:
			header = true
		case railSeparator:
			sep = true
		case railPlugin:
			plugin = true
		}
	}
	if !sep || !header || !plugin {
		t.Fatalf("expected separator + plugins header + plugin entry; sep=%v header=%v plugin=%v", sep, header, plugin)
	}
}

func TestSettingsView_PluginSectionsSortedAlphabetically(t *testing.T) {
	d, _ := db.OpenInMemory()
	t.Cleanup(func() { d.Close() }) //nolint:errcheck
	seedPluginSection(t, d, "scope", "Bravo")
	seedPluginSection(t, d, "scope", "Alpha")
	sv := NewSettingsView(d)
	sv.Refresh()

	entries := sv.railEntries()
	var titles []string
	for _, e := range entries {
		if e.kind == railPlugin {
			titles = append(titles, e.label)
		}
	}
	testutil.Equal(t, len(titles), 2)
	testutil.Equal(t, titles[0], "Alpha")
	testutil.Equal(t, titles[1], "Bravo")
}

func TestSettingsView_SelectingPluginPopulatesRows(t *testing.T) {
	d, _ := db.OpenInMemory()
	t.Cleanup(func() { d.Close() }) //nolint:errcheck
	seedPluginSection(t, d, "scope", "")
	sv := NewSettingsView(d)
	sv.Refresh()

	entries := sv.railEntries()
	var picked railEntry
	for _, e := range entries {
		if e.kind == railPlugin {
			picked = e
			break
		}
	}
	sv.setActiveFromRail(picked)

	// One row per field + a Save row.
	testutil.Equal(t, len(sv.rows), 5)
	testutil.Equal(t, sv.rows[0].kind, srPluginField)
	testutil.Equal(t, sv.rows[len(sv.rows)-1].kind, srPluginSubmit)
}

func TestSettingsView_PluginFieldBoolToggle(t *testing.T) {
	sv := selectFirstPluginSection(t)
	// First row is the bool field. Pressing enter toggles in place.
	sv.cursor = 0
	got := sv.handleEnter()
	testutil.Equal(t, got, true)

	sec := sv.activePluginSection()
	val := sv.pluginValueFor(sec, &sec.Spec.Fields[0])
	if v, _ := val.(bool); !v {
		t.Fatal("bool field should toggle to true on first enter")
	}
}

func TestSettingsView_PluginFieldEnumCycle(t *testing.T) {
	sv := selectFirstPluginSection(t)
	// 4th row is the enum field.
	sv.cursor = 3
	got := sv.HandleKey(tcell.NewEventKey(tcell.KeyRight, 0, 0))
	testutil.Equal(t, got, true)

	sec := sv.activePluginSection()
	val, _ := sv.pluginValueFor(sec, &sec.Spec.Fields[3]).(string)
	testutil.Equal(t, val, "codex")
}

func TestSettingsView_PluginFieldStringInlineEdit(t *testing.T) {
	sv := selectFirstPluginSection(t)
	// String field is row 2 (index 2).
	sv.cursor = 2
	got := sv.handleEnter()
	testutil.Equal(t, got, true)
	if sv.activeEditKey != "name" {
		t.Fatalf("expected activeEditKey=name, got %q", sv.activeEditKey)
	}

	// Type "hi" then commit.
	sv.HandleKey(tcell.NewEventKey(tcell.KeyRune, 'h', 0))
	sv.HandleKey(tcell.NewEventKey(tcell.KeyRune, 'i', 0))
	sv.HandleKey(tcell.NewEventKey(tcell.KeyEnter, 0, 0))

	sec := sv.activePluginSection()
	val, _ := sv.pluginValueFor(sec, &sec.Spec.Fields[2]).(string)
	testutil.Equal(t, val, "defaulthi")
}

func TestSettingsView_PluginFieldStringInlineEditCancel(t *testing.T) {
	sv := selectFirstPluginSection(t)
	sv.cursor = 2
	sv.handleEnter()
	sv.HandleKey(tcell.NewEventKey(tcell.KeyRune, 'X', 0))
	sv.HandleKey(tcell.NewEventKey(tcell.KeyEscape, 0, 0))
	if sv.activeEditKey != "" {
		t.Fatal("escape should cancel edit")
	}
	sec := sv.activePluginSection()
	val, _ := sv.pluginValueFor(sec, &sec.Spec.Fields[2]).(string)
	testutil.Equal(t, val, "default")
}

func TestSettingsView_PluginFieldIntInlineEdit(t *testing.T) {
	sv := selectFirstPluginSection(t)
	// Int field at row 1.
	sv.cursor = 1
	sv.handleEnter()
	// Replace value: backspace once to remove "1", then type "5".
	sv.HandleKey(tcell.NewEventKey(tcell.KeyBackspace, 0, 0))
	sv.HandleKey(tcell.NewEventKey(tcell.KeyRune, '5', 0))
	sv.HandleKey(tcell.NewEventKey(tcell.KeyEnter, 0, 0))

	sec := sv.activePluginSection()
	val, _ := sv.pluginValueFor(sec, &sec.Spec.Fields[1]).(int)
	testutil.Equal(t, val, 5)
}

func TestSettingsView_PluginFieldIntClampsToMax(t *testing.T) {
	sv := selectFirstPluginSection(t)
	sv.cursor = 1
	sv.handleEnter()
	sv.HandleKey(tcell.NewEventKey(tcell.KeyBackspace, 0, 0))
	for _, r := range "9999" {
		sv.HandleKey(tcell.NewEventKey(tcell.KeyRune, r, 0))
	}
	sv.HandleKey(tcell.NewEventKey(tcell.KeyEnter, 0, 0))

	sec := sv.activePluginSection()
	val, _ := sv.pluginValueFor(sec, &sec.Spec.Fields[1]).(int)
	testutil.Equal(t, val, 10)
}

func TestSettingsView_PluginFieldIntRejectsNonNumeric(t *testing.T) {
	sv := selectFirstPluginSection(t)
	sv.cursor = 1
	sv.handleEnter()
	// Clear and type "abc".
	sv.HandleKey(tcell.NewEventKey(tcell.KeyBackspace, 0, 0))
	for _, r := range "abc" {
		sv.HandleKey(tcell.NewEventKey(tcell.KeyRune, r, 0))
	}
	// Enter must not commit a non-numeric int.
	sv.HandleKey(tcell.NewEventKey(tcell.KeyEnter, 0, 0))
	if sv.activeEditKey == "" {
		t.Fatal("editor should remain open on invalid int")
	}
}

func TestSettingsView_PluginSubmitWithDefaults(t *testing.T) {
	sv := selectFirstPluginSection(t)
	var seenScope, seenTitle string
	var seenValues map[string]any
	sv.SetPluginSubmit(func(scope, title string, values map[string]any) error {
		seenScope = scope
		seenTitle = title
		seenValues = values
		return nil
	})
	// Cursor onto the Save row (last row).
	sv.cursor = len(sv.rows) - 1
	got := sv.handleEnter()
	testutil.Equal(t, got, true)
	testutil.Equal(t, seenScope, "scope")
	testutil.Equal(t, seenTitle, "Hello")
	testutil.Equal(t, len(seenValues), 4)
	// Defaults round-trip through the submitter.
	testutil.Equal(t, seenValues["enabled"].(bool), false)
	testutil.Equal(t, seenValues["count"].(int), 1)
}

func TestSettingsView_PluginSubmitError(t *testing.T) {
	sv := selectFirstPluginSection(t)
	sv.SetPluginSubmit(func(string, string, map[string]any) error {
		return errors.New("upstream 502")
	})
	sv.cursor = len(sv.rows) - 1
	sv.handleEnter()

	sec := sv.activePluginSection()
	key := pluginKey{scope: sec.Scope, title: sec.Title}
	status := sv.pluginSubmitStatus[key]
	if status == "" || status[:6] != "Failed" {
		t.Fatalf("expected Failed status, got %q", status)
	}
}

func TestSettingsView_PluginSubmitWithoutHook(t *testing.T) {
	sv := selectFirstPluginSection(t)
	sv.SetPluginSubmit(nil)
	sv.cursor = len(sv.rows) - 1
	got := sv.handleEnter()
	testutil.Equal(t, got, true)
	sec := sv.activePluginSection()
	key := pluginKey{scope: sec.Scope, title: sec.Title}
	if sv.pluginSubmitStatus[key] == "" {
		t.Fatal("status should be set even without a submitter")
	}
}

func TestSettingsView_PluginEditTracksIsEditing(t *testing.T) {
	sv := selectFirstPluginSection(t)
	sv.cursor = 2 // string field
	sv.handleEnter()
	testutil.Equal(t, sv.IsEditing(), true)
	sv.HandleKey(tcell.NewEventKey(tcell.KeyEscape, 0, 0))
	testutil.Equal(t, sv.IsEditing(), false)
}

func TestSettingsView_PluginUnregisterResetsState(t *testing.T) {
	d, _ := db.OpenInMemory()
	t.Cleanup(func() { d.Close() }) //nolint:errcheck
	seedPluginSection(t, d, "scope", "")
	sv := NewSettingsView(d)
	sv.Refresh()

	entries := sv.railEntries()
	for _, e := range entries {
		if e.kind == railPlugin {
			sv.setActiveFromRail(e)
			break
		}
	}
	// Touch a value so prunePluginValues has something to clean.
	sec := sv.activePluginSection()
	sv.setPluginValue(sec, "name", "edited")

	// Unregister the section and refresh.
	_, _ = d.DeletePluginSection("scope", "Hello")
	sv.Refresh()

	if len(sv.pluginValues) != 0 {
		t.Fatal("plugin values should be pruned on unregister")
	}
}

func TestSettingsView_RailNavigationSkipsHeader(t *testing.T) {
	d, _ := db.OpenInMemory()
	t.Cleanup(func() { d.Close() }) //nolint:errcheck
	seedPluginSection(t, d, "scope", "")
	sv := NewSettingsView(d)
	sv.Refresh()
	sv.setFocus(focusRail)
	sv.setCategory(catLogs)

	// Walk Down — should advance into the plugin section (railLayouts is hidden;
	// after catLogs comes the separator + Plugins header which must be skipped).
	got := sv.HandleKey(tcell.NewEventKey(tcell.KeyDown, 0, 0))
	testutil.Equal(t, got, true)
	testutil.Equal(t, sv.category, catPlugin)
	testutil.Equal(t, sv.activePlugin, pluginKey{scope: "scope", title: "Hello"})
}

func TestSettingsView_PasteIntoPluginField(t *testing.T) {
	sv := selectFirstPluginSection(t)
	sv.cursor = 2 // string field
	sv.handleEnter()
	sv.PasteHandler()("pasted", func(p tview.Primitive) {})
	if sv.editPluginBuf != "defaultpasted" {
		t.Fatalf("paste did not append, got %q", sv.editPluginBuf)
	}
}

func TestSettings_RenderPluginFieldDetail(t *testing.T) {
	sv := selectFirstPluginSection(t)
	// Walk through every field row to exercise the type-specific branches.
	for i, r := range sv.rows {
		if r.kind != srPluginField {
			continue
		}
		sv.cursor = i
		sv.SetRect(0, 0, 100, 30)
		sv.Draw(drawSim(t))
	}
	// And the Save row to exercise renderPluginSubmitDetail (no-status + with-status).
	sv.cursor = len(sv.rows) - 1
	sv.Draw(drawSim(t))

	// Inject a status (success and error variants) and re-render.
	sec := sv.activePluginSection()
	key := pluginKey{scope: sec.Scope, title: sec.Title}
	sv.pluginSubmitStatus[key] = "Saved 4 field(s)"
	sv.Draw(drawSim(t))
	sv.pluginSubmitStatus[key] = "Failed: boom"
	sv.Draw(drawSim(t))
}

func TestSettings_PluginFieldHintAndTrunc(t *testing.T) {
	// Cover the standalone helpers directly so test-driven render exercise
	// doesn't need to scoot through every (type, editing) combination.
	cases := []struct {
		f       pluginsettings.FormField
		editing bool
		want    string
	}{
		{pluginsettings.FormField{Type: pluginsettings.FieldBool}, false, "[enter] toggle"},
		{pluginsettings.FormField{Type: pluginsettings.FieldEnum}, false, "[◀/▶] cycle  [enter] cycle"},
		{pluginsettings.FormField{Type: pluginsettings.FieldInt}, false, "[enter] edit value"},
		{pluginsettings.FormField{Type: pluginsettings.FieldString}, true, "[enter] save  [esc] cancel"},
		{pluginsettings.FormField{Type: pluginsettings.FieldType("unknown")}, false, ""},
	}
	for _, c := range cases {
		got := pluginFieldHint(&c.f, c.editing)
		testutil.Equal(t, got, c.want)
	}
	testutil.Equal(t, truncateForPane("hello", 0), "")
	testutil.Equal(t, truncateForPane("short", 10), "short")
	testutil.Equal(t, truncateForPane("supercalifragilistic", 5), "supe…")
}

func TestSettings_FormatFieldValueDefensive(t *testing.T) {
	// Defaults defensively map mismatched stored types to type-appropriate
	// zero values.
	tests := []struct {
		f   pluginsettings.FormField
		v   any
		out string
	}{
		{pluginsettings.FormField{Type: pluginsettings.FieldBool}, "not a bool", "false"},
		{pluginsettings.FormField{Type: pluginsettings.FieldInt}, "not an int", "0"},
		{pluginsettings.FormField{Type: pluginsettings.FieldInt}, float64(42), "42"},
		{pluginsettings.FormField{Type: pluginsettings.FieldString}, 1, ""},
		{pluginsettings.FormField{Type: pluginsettings.FieldEnum}, 1, ""},
		{pluginsettings.FormField{Type: pluginsettings.FieldType("unknown")}, "x", ""},
	}
	for _, c := range tests {
		testutil.Equal(t, formatFieldValue(&c.f, c.v), c.out)
	}
}

func TestSettings_PluginCycleNoMatch(t *testing.T) {
	sv := selectFirstPluginSection(t)
	// Cursor on the Save row — cycle should be a no-op.
	sv.cursor = len(sv.rows) - 1
	testutil.Equal(t, sv.handlePluginCycle(1), false)
	// Cursor on a string field — cycle should also be a no-op (string isn't cycleable).
	sv.cursor = 2
	testutil.Equal(t, sv.handlePluginCycle(1), false)
}

func TestSettings_PluginEnterOnUnknownField(t *testing.T) {
	sv := selectFirstPluginSection(t)
	// Manually inject a row pointing at a missing key.
	sv.rows = append(sv.rows[:1:1], settingsRow{kind: srPluginField, key: "_nope"})
	sv.cursor = 1
	testutil.Equal(t, sv.handlePluginFieldEnter(), false)
}

func TestSettings_PluginEnumCycleNoOptions(t *testing.T) {
	// Defensive: cyclePluginEnum on a field with no options must not panic.
	sv := selectFirstPluginSection(t)
	sec := sv.activePluginSection()
	empty := pluginsettings.FormField{Type: pluginsettings.FieldEnum, Key: "x"}
	sv.cyclePluginEnum(sec, &empty, 1)
}

func TestSettings_PluginEnumCycleWrapsBackwards(t *testing.T) {
	sv := selectFirstPluginSection(t)
	sv.cursor = 3 // enum field
	// Going backwards from "claude" should land on "codex" (last).
	sv.handlePluginCycle(-1)
	sec := sv.activePluginSection()
	val, _ := sv.pluginValueFor(sec, &sec.Spec.Fields[3]).(string)
	testutil.Equal(t, val, "codex")
}

func TestSettings_MoveCategoryNoOpInForward(t *testing.T) {
	d, _ := db.OpenInMemory()
	t.Cleanup(func() { d.Close() }) //nolint:errcheck
	sv := NewSettingsView(d)
	sv.Refresh()
	// Park on the last selectable rail entry and try to go further.
	sv.setCategory(catLogs)
	moved := sv.moveCategory(1)
	testutil.Equal(t, moved, false)
}

func TestSettings_MoveCategoryRecoversFromStaleActive(t *testing.T) {
	d, _ := db.OpenInMemory()
	t.Cleanup(func() { d.Close() }) //nolint:errcheck
	seedPluginSection(t, d, "scope", "")
	sv := NewSettingsView(d)
	sv.Refresh()
	for _, e := range sv.railEntries() {
		if e.kind == railPlugin {
			sv.setActiveFromRail(e)
			break
		}
	}
	// Unregister out from under the cursor.
	_, _ = d.DeletePluginSection("scope", "Hello")
	sv.Refresh()
	// Move now — moveCategory should rescue the cursor to catSystem.
	moved := sv.moveCategory(1)
	testutil.Equal(t, moved, true)
	testutil.Equal(t, sv.category, catSystem)
}

func TestSettings_HandleClickRailSkipsHeader(t *testing.T) {
	d, _ := db.OpenInMemory()
	t.Cleanup(func() { d.Close() }) //nolint:errcheck
	seedPluginSection(t, d, "scope", "")
	sv := NewSettingsView(d)
	sv.Refresh()
	sv.SetRect(0, 0, 100, 30)
	sv.setCategory(catSystem)

	// Find the row index of the separator + Plugins header by scanning rail entries.
	entries := sv.railEntries()
	var headerRow int
	for i, e := range entries {
		if e.kind == railPluginsHeader {
			headerRow = i
			break
		}
	}
	// Click on the header row — should NOT change the category.
	prev := sv.category
	sv.HandleClick(2, 1+headerRow) // y = iy + row, iy = 1
	testutil.Equal(t, sv.category, prev)
}

func TestSettings_SetActiveFromRailSamePluginNoOp(t *testing.T) {
	sv := selectFirstPluginSection(t)
	// Calling setActiveFromRail with the already-active key is a no-op.
	var picked railEntry
	for _, e := range sv.railEntries() {
		if e.kind == railPlugin && e.key == sv.activePlugin {
			picked = e
			break
		}
	}
	sv.setActiveFromRail(picked) // should not bump anything
	testutil.Equal(t, sv.category, catPlugin)
}

func TestSettings_PluginEditKeyConsumesNavKeys(t *testing.T) {
	sv := selectFirstPluginSection(t)
	sv.cursor = 2 // string field
	sv.handleEnter()
	for _, k := range []tcell.Key{tcell.KeyUp, tcell.KeyDown, tcell.KeyLeft, tcell.KeyRight} {
		got := sv.HandleKey(tcell.NewEventKey(k, 0, 0))
		testutil.Equal(t, got, true)
	}
	if sv.activeEditKey == "" {
		t.Fatal("nav keys must not exit edit mode")
	}
}

func TestSettings_PluginEditBackspace(t *testing.T) {
	sv := selectFirstPluginSection(t)
	sv.cursor = 2
	sv.handleEnter()
	// editPluginBuf starts as "default" (the default value).
	for i := 0; i < 3; i++ {
		sv.HandleKey(tcell.NewEventKey(tcell.KeyBackspace, 0, 0))
	}
	testutil.Equal(t, sv.editPluginBuf, "defa")
	// Backspace on empty must not panic.
	for i := 0; i < 10; i++ {
		sv.HandleKey(tcell.NewEventKey(tcell.KeyBackspace, 0, 0))
	}
	testutil.Equal(t, sv.editPluginBuf, "")
}

func TestSettings_PluginEditKeyRejectsUnknownKey(t *testing.T) {
	sv := selectFirstPluginSection(t)
	sv.cursor = 2
	sv.handleEnter()
	// Tab is neither nav nor commit nor cancel — the editor should pass.
	got := sv.HandleKey(tcell.NewEventKey(tcell.KeyTab, 0, 0))
	testutil.Equal(t, got, false)
}

// selectFirstPluginSection sets up a SettingsView with one seeded plugin
// section selected as the active category. Used by every plugin-section
// unit test below.
func selectFirstPluginSection(t *testing.T) *SettingsView {
	t.Helper()
	d, err := db.OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { d.Close() }) //nolint:errcheck
	seedPluginSection(t, d, "scope", "")
	sv := NewSettingsView(d)
	sv.Refresh()

	for _, e := range sv.railEntries() {
		if e.kind == railPlugin {
			sv.setActiveFromRail(e)
			break
		}
	}
	if sv.category != catPlugin {
		t.Fatal("failed to select plugin section")
	}
	return sv
}

func TestSettingsView_StreamSection_RebuildRowsHasNoRows(t *testing.T) {
	d, _ := db.OpenInMemory()
	t.Cleanup(func() { d.Close() }) //nolint:errcheck
	seedStreamSection(t, d, "ludwig", "Orchestrators", "ws://127.0.0.1:9991/live")
	sv := NewSettingsView(d)
	sv.Refresh()

	for _, e := range sv.railEntries() {
		if e.kind == railPlugin {
			sv.setActiveFromRail(e)
			break
		}
	}
	testutil.Equal(t, sv.category, catPlugin)
	// Stream sections render via streampane, not row-by-row, so the rows
	// list must be empty even though the active section exists.
	testutil.Equal(t, len(sv.rows), 0)
}

func TestSettingsView_StreamSection_FiresFocusAndBlur(t *testing.T) {
	d, _ := db.OpenInMemory()
	t.Cleanup(func() { d.Close() }) //nolint:errcheck
	seedStreamSection(t, d, "ludwig", "Orchestrators", "ws://x")
	sv := NewSettingsView(d)
	sv.Refresh()

	var focusedScope, focusedTitle, focusedURL string
	var blurredScope, blurredTitle string
	sv.OnStreamFocus = func(scope, title, url string, _ chan<- []byte, _ <-chan []byte) {
		focusedScope = scope
		focusedTitle = title
		focusedURL = url
	}
	sv.OnStreamBlur = func(scope, title string) {
		blurredScope = scope
		blurredTitle = title
	}

	// Enter the stream section.
	for _, e := range sv.railEntries() {
		if e.kind == railPlugin {
			sv.setActiveFromRail(e)
			break
		}
	}
	testutil.Equal(t, focusedScope, "ludwig")
	testutil.Equal(t, focusedTitle, "Orchestrators")
	testutil.Equal(t, focusedURL, "ws://x")

	// Switching away fires blur.
	sv.setCategory(catSystem)
	testutil.Equal(t, blurredScope, "ludwig")
	testutil.Equal(t, blurredTitle, "Orchestrators")
}

func TestSettingsView_StreamSection_PreservesMountAcrossFocusToggle(t *testing.T) {
	d, _ := db.OpenInMemory()
	t.Cleanup(func() { d.Close() }) //nolint:errcheck
	seedStreamSection(t, d, "ludwig", "Live", "ws://x")
	sv := NewSettingsView(d)
	sv.Refresh()

	var focusCount int
	sv.OnStreamFocus = func(_, _, _ string, _ chan<- []byte, _ <-chan []byte) {
		focusCount++
	}

	enter := func() {
		for _, e := range sv.railEntries() {
			if e.kind == railPlugin {
				sv.setActiveFromRail(e)
				break
			}
		}
	}
	enter()
	mount1, ok := sv.streamMounts[pluginKey{scope: "ludwig", title: "Live"}]
	testutil.Equal(t, ok, true)
	sv.setCategory(catSystem)
	enter()
	mount2, ok := sv.streamMounts[pluginKey{scope: "ludwig", title: "Live"}]
	testutil.Equal(t, ok, true)
	if mount1 != mount2 {
		t.Fatal("streampane mount should survive focus toggle")
	}
	testutil.Equal(t, focusCount, 2)
}

func TestSettingsView_StreamSection_UnregisterFiresBlur(t *testing.T) {
	d, _ := db.OpenInMemory()
	t.Cleanup(func() { d.Close() }) //nolint:errcheck
	seedStreamSection(t, d, "ludwig", "Live", "ws://x")
	sv := NewSettingsView(d)
	sv.Refresh()

	var blurredScope string
	sv.OnStreamBlur = func(scope, _ string) { blurredScope = scope }
	sv.OnStreamFocus = func(_, _, _ string, _ chan<- []byte, _ <-chan []byte) {}

	for _, e := range sv.railEntries() {
		if e.kind == railPlugin {
			sv.setActiveFromRail(e)
			break
		}
	}

	// Plugin unregisters in the daemon — DB row removed.
	_, err := d.DeletePluginSection("ludwig", "Live")
	testutil.NoError(t, err)
	sv.Refresh()

	testutil.Equal(t, blurredScope, "ludwig")
	if _, ok := sv.streamMounts[pluginKey{scope: "ludwig", title: "Live"}]; ok {
		t.Fatal("stream mount should be cleaned up after unregister")
	}
}
