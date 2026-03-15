package ui

import (
	"reflect"

	tea "github.com/charmbracelet/bubbletea"
)

// SuperToAltFilter converts Cmd+arrow keys (sent as CSI sequences with
// Super/modifier 9 by modern terminals like Ghostty and kitty) into
// Alt-modified KeyMsgs that the rest of the UI code expects.
//
// Bubbletea v1.3 doesn't recognise modifier 9 (Super), so sequences
// like \x1b[1;9C arrive as unexported unknownCSISequenceMsg. This
// filter intercepts them and re-emits standard Alt+arrow KeyMsgs.
func SuperToAltFilter(_ tea.Model, msg tea.Msg) tea.Msg {
	v := reflect.ValueOf(msg)
	if v.Kind() != reflect.Slice || v.Type().Elem().Kind() != reflect.Uint8 {
		return msg
	}
	raw := v.Bytes()

	// Match CSI sequences with Super modifier: \x1b [ 1 ; 9 <letter>
	if len(raw) == 6 &&
		raw[0] == '\x1b' && raw[1] == '[' &&
		raw[2] == '1' && raw[3] == ';' && raw[4] == '9' {
		switch raw[5] {
		case 'A':
			return tea.KeyMsg{Type: tea.KeyUp, Alt: true}
		case 'B':
			return tea.KeyMsg{Type: tea.KeyDown, Alt: true}
		case 'C':
			return tea.KeyMsg{Type: tea.KeyRight, Alt: true}
		case 'D':
			return tea.KeyMsg{Type: tea.KeyLeft, Alt: true}
		}
	}
	return msg
}
