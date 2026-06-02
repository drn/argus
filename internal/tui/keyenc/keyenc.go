// Package keyenc maps a tcell key event to the raw bytes a terminal
// application (an agent PTY, or a plugin-backed pane) expects on its input
// stream.
//
// It is the single source of truth for key encoding, shared by the agent
// terminal (internal/tui/app.go), the plugin terminal pane
// (internal/tui/terminalpane), and the plugin stream pane
// (internal/tui/streampane). Before this package, those three sites carried
// divergent encoders — the two pane encoders silently dropped arrows and all
// modifier combos, so a plugin could never bind Ctrl/Alt/Shift+arrow.
//
// The encoding follows the standard xterm conventions:
//
//   - Runes: plain → the rune's UTF-8 bytes; with Alt → ESC-prefixed.
//   - Enter: plain → CR; with Shift or Alt → ESC+CR (newline-insert).
//   - Tab → HT; Backtab → CSI Z.
//   - Backspace → DEL (0x7f); with Alt → ESC+DEL. Delete → CSI 3 ~; with
//     Alt → ESC+DEL.
//   - Arrows / Home / End: unmodified → bare CSI final; modified → the xterm
//     form CSI 1 ; <mod> <final> where <mod> = 1 + Shift(1) + Alt(2) +
//     Ctrl(4). Ctrl+Alt yields mod 7, which iTerm2 maps Cmd+arrow onto.
//   - PgUp → CSI 5 ~; PgDn → CSI 6 ~.
//   - Ctrl+letter → the C0 control byte.
//   - Escape → ESC. Anything else → nil (dropped).
package keyenc

import "github.com/gdamore/tcell/v2"

// arrowFinal maps the cursor/navigation keys that share the CSI "1 ; mod
// final" modified form to their final byte. Home/End use H/F.
var arrowFinal = map[tcell.Key]byte{
	tcell.KeyUp:    'A',
	tcell.KeyDown:  'B',
	tcell.KeyRight: 'C',
	tcell.KeyLeft:  'D',
	tcell.KeyHome:  'H',
	tcell.KeyEnd:   'F',
}

// Encode converts a tcell key event to the raw bytes for PTY/terminal input.
// Returns nil for keys with no terminal encoding.
func Encode(ev *tcell.EventKey) []byte {
	mods := ev.Modifiers()
	alt := mods&tcell.ModAlt != 0

	if ev.Key() == tcell.KeyRune {
		r := ev.Rune()
		if alt {
			return append([]byte{0x1b}, []byte(string(r))...)
		}
		return []byte(string(r))
	}

	// Cursor / navigation keys: emit the modified xterm form when any
	// modifier is held, else the bare CSI final. This is the behavior the
	// old agent encoder had for Alt+arrow (mod 3); it now generalizes to
	// Shift / Ctrl / combinations so plugins receive Ctrl+arrow,
	// Shift+arrow, and the Ctrl+Alt (mod 7) form iTerm2 maps Cmd+arrow to.
	if final, ok := arrowFinal[ev.Key()]; ok {
		if mods == tcell.ModNone {
			return []byte{0x1b, '[', final}
		}
		return modifiedCSI(final, mods)
	}

	switch ev.Key() {
	case tcell.KeyEnter:
		// Shift+Enter / Alt+Enter → newline-insert (ESC + CR). TUIs running
		// in the PTY (ink-based Claude Code, blessed, textual) treat CR as
		// submit and ESC+CR as "insert newline".
		if mods&(tcell.ModShift|tcell.ModAlt) != 0 {
			return []byte{0x1b, '\r'}
		}
		return []byte{'\r'}
	case tcell.KeyTab:
		return []byte{'\t'}
	case tcell.KeyBacktab:
		return []byte("\x1b[Z")
	case tcell.KeyBackspace, tcell.KeyBackspace2:
		if alt {
			return []byte{0x1b, 0x7f}
		}
		return []byte{0x7f}
	case tcell.KeyDelete:
		if alt {
			return []byte{0x1b, 0x7f}
		}
		return []byte("\x1b[3~")
	case tcell.KeyPgUp:
		return []byte("\x1b[5~")
	case tcell.KeyPgDn:
		return []byte("\x1b[6~")
	case tcell.KeyCtrlA:
		return []byte{0x01}
	case tcell.KeyCtrlB:
		return []byte{0x02}
	case tcell.KeyCtrlC:
		return []byte{0x03}
	case tcell.KeyCtrlD:
		return []byte{0x04}
	case tcell.KeyCtrlE:
		return []byte{0x05}
	case tcell.KeyCtrlF:
		return []byte{0x06}
	case tcell.KeyCtrlG:
		return []byte{0x07}
	case tcell.KeyCtrlH:
		return []byte{0x08}
	case tcell.KeyCtrlK:
		return []byte{0x0b}
	case tcell.KeyCtrlL:
		return []byte{0x0c}
	case tcell.KeyCtrlN:
		return []byte{0x0e}
	case tcell.KeyCtrlO:
		return []byte{0x0f}
	case tcell.KeyCtrlP:
		return []byte{0x10}
	case tcell.KeyCtrlQ:
		return []byte{0x11}
	case tcell.KeyCtrlR:
		return []byte{0x12}
	case tcell.KeyCtrlS:
		return []byte{0x13}
	case tcell.KeyCtrlT:
		return []byte{0x14}
	case tcell.KeyCtrlU:
		return []byte{0x15}
	case tcell.KeyCtrlV:
		return []byte{0x16}
	case tcell.KeyCtrlW:
		return []byte{0x17}
	case tcell.KeyCtrlX:
		return []byte{0x18}
	case tcell.KeyCtrlY:
		return []byte{0x19}
	case tcell.KeyCtrlZ:
		return []byte{0x1a}
	case tcell.KeyEscape:
		return []byte{0x1b}
	}
	return nil
}

// modifiedCSI builds the xterm "CSI 1 ; <mod> <final>" sequence. The
// modifier parameter is 1 + Shift(1) + Alt(2) + Ctrl(4): e.g. Ctrl+Right →
// "\x1b[1;5C", Ctrl+Alt+Right → "\x1b[1;7C".
func modifiedCSI(final byte, mods tcell.ModMask) []byte {
	param := 1
	if mods&tcell.ModShift != 0 {
		param += 1
	}
	if mods&tcell.ModAlt != 0 {
		param += 2
	}
	if mods&tcell.ModCtrl != 0 {
		param += 4
	}
	return []byte{0x1b, '[', '1', ';', byte('0' + param), final}
}
