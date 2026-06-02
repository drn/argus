package tui

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/gdamore/tcell/v2"

	pluginsettings "github.com/drn/argus/internal/tui/settings"
	"github.com/drn/argus/internal/tui/theme"
	"github.com/drn/argus/internal/tui/widget"
	"github.com/drn/argus/internal/uxlog"
)

// pluginFieldRowLabel formats one field row: "Label: value". value is the
// current draft for the field, falling back to the field's default when the
// user hasn't touched it yet. The label width is intentionally not padded
// because the items list truncates per-row.
func pluginFieldRowLabel(sv *SettingsView, sec *pluginsettings.Section, f *pluginsettings.FormField) string {
	val := sv.pluginValueFor(sec, f)
	if sv.activeEditKey == f.Key && (f.Type == pluginsettings.FieldString || f.Type == pluginsettings.FieldInt) {
		return f.Label + ": " + sv.editPluginBuf + "▎"
	}
	return f.Label + ": " + formatFieldValue(f, val)
}

// formatFieldValue stringifies one field's stored value for the row label.
// The wire shape can hand us a float64 (JSON number) for an int default;
// format such values as ints to keep the rail looking clean.
func formatFieldValue(f *pluginsettings.FormField, v any) string {
	switch f.Type {
	case pluginsettings.FieldBool:
		if b, ok := v.(bool); ok && b {
			return "true"
		}
		return "false"
	case pluginsettings.FieldInt:
		switch n := v.(type) {
		case int:
			return strconv.Itoa(n)
		case float64:
			return strconv.Itoa(int(n))
		}
		return "0"
	case pluginsettings.FieldString:
		if s, ok := v.(string); ok {
			return s
		}
		return ""
	case pluginsettings.FieldEnum:
		if s, ok := v.(string); ok {
			return s
		}
		return ""
	}
	return ""
}

// renderPluginFieldDetail draws the per-field detail block. Shows the
// field's type, the active value, and the type-appropriate hint at the
// bottom. The pane is read-mostly: bool toggles via enter, enum cycles via
// arrow keys, string/int open an inline editor via enter.
func (sv *SettingsView) renderPluginFieldDetail(screen tcell.Screen, x, y, w, h int, row *settingsRow) {
	sec := sv.activePluginSection()
	if sec == nil || sec.Spec == nil {
		widget.DrawText(screen, x, y, w, "(no section selected)", theme.StyleDimmed)
		return
	}
	var field *pluginsettings.FormField
	for i := range sec.Spec.Fields {
		if sec.Spec.Fields[i].Key == row.key {
			field = &sec.Spec.Fields[i]
			break
		}
	}
	if field == nil {
		widget.DrawText(screen, x, y, w, "(field not found)", theme.StyleDimmed)
		return
	}

	widget.DrawText(screen, x, y, w, field.Label, theme.StyleTitle)
	r := 2
	widget.DrawText(screen, x, y+r, w, "Type: "+string(field.Type), theme.StyleDimmed)
	r++
	val := sv.pluginValueFor(sec, field)
	display := formatFieldValue(field, val)
	if sv.activeEditKey == field.Key {
		display = sv.editPluginBuf + "▎"
	}
	widget.DrawText(screen, x, y+r, w, "Value: "+display, tcell.StyleDefault.Foreground(theme.ColorComplete))
	r += 2

	// Type-specific extras.
	switch field.Type {
	case pluginsettings.FieldInt:
		if field.Min != nil || field.Max != nil {
			minS := "-∞"
			maxS := "+∞"
			if field.Min != nil {
				minS = strconv.Itoa(*field.Min)
			}
			if field.Max != nil {
				maxS = strconv.Itoa(*field.Max)
			}
			widget.DrawText(screen, x, y+r, w, "Range: ["+minS+", "+maxS+"]", theme.StyleDimmed)
			// r is intentionally not incremented here — the Range row is the
			// last per-field block and pluginFieldHint owns y+h-1.
		}
	case pluginsettings.FieldEnum:
		widget.DrawText(screen, x, y+r, w, "Options:", tcell.StyleDefault.Foreground(theme.ColorTitle))
		r++
		for _, opt := range field.Options {
			if r >= h-1 {
				break
			}
			marker := "  "
			style := theme.StyleDimmed
			if s, ok := val.(string); ok && s == opt {
				marker = "▸ "
				style = tcell.StyleDefault.Foreground(theme.ColorSelected).Bold(true)
			}
			widget.DrawText(screen, x, y+r, w, marker+opt, style)
			r++
		}
	}

	if h > 1 {
		widget.DrawText(screen, x, y+h-1, w, pluginFieldHint(field, sv.activeEditKey == field.Key), theme.StyleDimmed)
	}
}

// pluginFieldHint returns the bottom-row hint for the field's active state.
func pluginFieldHint(f *pluginsettings.FormField, editing bool) string {
	if editing {
		return "[enter] save  [esc] cancel"
	}
	switch f.Type {
	case pluginsettings.FieldBool:
		return "[enter] toggle"
	case pluginsettings.FieldEnum:
		return "[◀/▶] cycle  [enter] cycle"
	case pluginsettings.FieldInt, pluginsettings.FieldString:
		return "[enter] edit value"
	}
	return ""
}

// renderPluginSubmitDetail shows the section header + most recent submit
// status. Pressing enter on this row fires the section's submit hook.
func (sv *SettingsView) renderPluginSubmitDetail(screen tcell.Screen, x, y, w, h int) {
	sec := sv.activePluginSection()
	widget.DrawText(screen, x, y, w, "Save", theme.StyleTitle)
	if sec == nil {
		return
	}
	r := 2
	widget.DrawText(screen, x, y+r, w, "Scope:  "+sec.Scope, theme.StyleDimmed)
	r++
	widget.DrawText(screen, x, y+r, w, "Target: "+truncateForPane(sec.CallbackURL, w-8), theme.StyleDimmed)
	r += 2

	key := pluginKey{scope: sec.Scope, title: sec.Title}
	if status := sv.pluginSubmitStatus[key]; status != "" {
		color := theme.ColorComplete
		if strings.HasPrefix(status, "Failed") {
			color = theme.ColorError
		}
		widget.DrawText(screen, x, y+r, w, status, tcell.StyleDefault.Foreground(color))
		r += 2
	}
	if r < h-1 {
		widget.DrawText(screen, x, y+r, w, "Posts the current field values to the", theme.StyleDimmed)
		r++
		widget.DrawText(screen, x, y+r, w, "plugin's callback URL.", theme.StyleDimmed)
	}
	if h > 1 {
		widget.DrawText(screen, x, y+h-1, w, "[enter] save", theme.StyleDimmed)
	}
}

// truncateForPane returns s clipped to budget runes, suffixed with "…" when
// truncated. A non-positive budget yields "".
func truncateForPane(s string, budget int) string {
	if budget <= 0 {
		return ""
	}
	if utf8.RuneCountInString(s) <= budget {
		return s
	}
	return truncRunes(s, budget-1) + "…"
}

// handlePluginFieldEnter is fired by handleEnter when the cursor sits on a
// srPluginField row. For bool/enum it toggles in place; for string/int it
// opens the inline editor. Returns true so the caller skips its default
// behavior.
func (sv *SettingsView) handlePluginFieldEnter() bool {
	sec := sv.activePluginSection()
	if sec == nil {
		return false
	}
	row := sv.SelectedRow()
	if row == nil || row.kind != srPluginField {
		return false
	}
	var field *pluginsettings.FormField
	for i := range sec.Spec.Fields {
		if sec.Spec.Fields[i].Key == row.key {
			field = &sec.Spec.Fields[i]
			break
		}
	}
	if field == nil {
		return false
	}
	switch field.Type {
	case pluginsettings.FieldBool:
		cur, _ := sv.pluginValueFor(sec, field).(bool)
		sv.setPluginValue(sec, field.Key, !cur)
		sv.rebuildRows()
		return true
	case pluginsettings.FieldEnum:
		sv.cyclePluginEnum(sec, field, 1)
		return true
	case pluginsettings.FieldInt, pluginsettings.FieldString:
		sv.activeEditKey = field.Key
		sv.editPluginBuf = formatFieldValue(field, sv.pluginValueFor(sec, field))
		sv.rebuildRows()
		return true
	}
	return false
}

// cyclePluginEnum advances the enum value by dir. Index resets to 0 when
// the current value isn't in the option set (defensive: a wire-shape
// mismatch could land us out of range).
func (sv *SettingsView) cyclePluginEnum(sec *pluginsettings.Section, field *pluginsettings.FormField, dir int) {
	if len(field.Options) == 0 {
		return
	}
	cur, _ := sv.pluginValueFor(sec, field).(string)
	idx := -1
	for i, opt := range field.Options {
		if opt == cur {
			idx = i
			break
		}
	}
	n := len(field.Options)
	if idx < 0 {
		if dir > 0 {
			idx = 0
		} else {
			idx = n - 1
		}
	} else {
		idx = (idx + dir + n) % n
	}
	sv.setPluginValue(sec, field.Key, field.Options[idx])
	sv.rebuildRows()
}

// handlePluginFieldEditKey is fired when the inline editor is active. Enter
// commits; Esc cancels; Backspace/Rune mutate the buffer.
func (sv *SettingsView) handlePluginFieldEditKey(ev *tcell.EventKey) bool {
	sec := sv.activePluginSection()
	if sec == nil {
		sv.activeEditKey = ""
		return true
	}
	var field *pluginsettings.FormField
	for i := range sec.Spec.Fields {
		if sec.Spec.Fields[i].Key == sv.activeEditKey {
			field = &sec.Spec.Fields[i]
			break
		}
	}
	if field == nil {
		sv.activeEditKey = ""
		return true
	}
	switch ev.Key() {
	case tcell.KeyEnter:
		if field.Type == pluginsettings.FieldInt {
			n, err := strconv.Atoi(strings.TrimSpace(sv.editPluginBuf))
			if err != nil {
				// Don't commit a non-numeric value; keep the editor open so
				// the user sees their typo.
				return true
			}
			if field.Min != nil && n < *field.Min {
				n = *field.Min
			}
			if field.Max != nil && n > *field.Max {
				n = *field.Max
			}
			sv.setPluginValue(sec, field.Key, n)
		} else {
			sv.setPluginValue(sec, field.Key, sv.editPluginBuf)
		}
		sv.activeEditKey = ""
		sv.editPluginBuf = ""
		sv.rebuildRows()
		return true
	case tcell.KeyEscape:
		sv.activeEditKey = ""
		sv.editPluginBuf = ""
		sv.rebuildRows()
		return true
	case tcell.KeyBackspace, tcell.KeyBackspace2:
		if len(sv.editPluginBuf) > 0 {
			_, size := utf8.DecodeLastRuneInString(sv.editPluginBuf)
			sv.editPluginBuf = sv.editPluginBuf[:len(sv.editPluginBuf)-size]
			sv.rebuildRows()
		}
		return true
	case tcell.KeyRune:
		sv.editPluginBuf += string(ev.Rune())
		sv.rebuildRows()
		return true
	case tcell.KeyUp, tcell.KeyDown, tcell.KeyLeft, tcell.KeyRight:
		// Consume nav keys while editing so the cursor doesn't change rows
		// mid-edit; matches the vault path editor's behavior.
		return true
	}
	return false
}

// handlePluginCycle is fired by left/right arrows over a plugin field row.
// Bool/enum cycle; int/string fall through (false return) so the global
// handler can do its own thing.
func (sv *SettingsView) handlePluginCycle(dir int) bool {
	sec := sv.activePluginSection()
	if sec == nil {
		return false
	}
	row := sv.SelectedRow()
	if row == nil || row.kind != srPluginField {
		return false
	}
	var field *pluginsettings.FormField
	for i := range sec.Spec.Fields {
		if sec.Spec.Fields[i].Key == row.key {
			field = &sec.Spec.Fields[i]
			break
		}
	}
	if field == nil {
		return false
	}
	switch field.Type {
	case pluginsettings.FieldBool:
		cur, _ := sv.pluginValueFor(sec, field).(bool)
		sv.setPluginValue(sec, field.Key, !cur)
		sv.rebuildRows()
		return true
	case pluginsettings.FieldEnum:
		sv.cyclePluginEnum(sec, field, dir)
		return true
	}
	return false
}

// handlePluginSubmit gathers the current draft values + every untouched
// field's default and dispatches to the section's submit hook. The hook is
// the test seam; production wires it to the daemon's submit endpoint.
func (sv *SettingsView) handlePluginSubmit() bool {
	sec := sv.activePluginSection()
	if sec == nil || sec.Spec == nil {
		return false
	}
	values := make(map[string]any, len(sec.Spec.Fields))
	for i := range sec.Spec.Fields {
		f := &sec.Spec.Fields[i]
		values[f.Key] = sv.pluginValueFor(sec, f)
	}
	key := pluginKey{scope: sec.Scope, title: sec.Title}
	if sv.pluginSubmit == nil {
		sv.pluginSubmitStatus[key] = "Saved (no submitter wired)"
		uxlog.Log("[settings] plugin submit scope=%q title=%q (no submitter)", sec.Scope, sec.Title)
		sv.rebuildRows()
		return true
	}
	err := sv.pluginSubmit(sec.Scope, sec.Title, values)
	if err != nil {
		sv.pluginSubmitStatus[key] = "Failed: " + err.Error()
		uxlog.Log("[settings] plugin submit scope=%q title=%q failed: %v", sec.Scope, sec.Title, err)
	} else {
		sv.pluginSubmitStatus[key] = fmt.Sprintf("Saved %d field(s)", len(values))
		uxlog.Log("[settings] plugin submit scope=%q title=%q ok", sec.Scope, sec.Title)
	}
	sv.rebuildRows()
	return true
}
