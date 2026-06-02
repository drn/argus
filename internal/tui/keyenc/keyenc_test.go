package keyenc

import (
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/drn/argus/internal/testutil"
)

// TestEncode_AllCases is the single authoritative table for terminal key
// encoding. It ports every case previously covered by the three divergent
// encoders (app.go tcellKeyToBytes, terminalpane.eventBytes,
// streampane.eventBytes) and adds the modified-arrow cases that the two
// pane encoders silently dropped.
func TestEncode_AllCases(t *testing.T) {
	tests := []struct {
		name string
		key  tcell.Key
		r    rune
		mod  tcell.ModMask
		want []byte
	}{
		// --- Runes ---
		{"rune-a", tcell.KeyRune, 'a', tcell.ModNone, []byte("a")},
		{"rune-z", tcell.KeyRune, 'z', tcell.ModNone, []byte("z")},
		{"rune-alt-a", tcell.KeyRune, 'a', tcell.ModAlt, []byte{0x1b, 'a'}},

		// --- Enter / Tab / Backtab ---
		{"enter", tcell.KeyEnter, 0, tcell.ModNone, []byte{'\r'}},
		{"shift-enter", tcell.KeyEnter, 0, tcell.ModShift, []byte{0x1b, '\r'}},
		{"alt-enter", tcell.KeyEnter, 0, tcell.ModAlt, []byte{0x1b, '\r'}},
		{"tab", tcell.KeyTab, 0, tcell.ModNone, []byte{'\t'}},
		{"backtab", tcell.KeyBacktab, 0, tcell.ModNone, []byte("\x1b[Z")},

		// --- Backspace / Delete ---
		{"backspace", tcell.KeyBackspace, 0, tcell.ModNone, []byte{0x7f}},
		{"backspace2", tcell.KeyBackspace2, 0, tcell.ModNone, []byte{0x7f}},
		{"alt-backspace", tcell.KeyBackspace, 0, tcell.ModAlt, []byte{0x1b, 0x7f}},
		{"alt-backspace2", tcell.KeyBackspace2, 0, tcell.ModAlt, []byte{0x1b, 0x7f}},
		{"delete", tcell.KeyDelete, 0, tcell.ModNone, []byte("\x1b[3~")},
		{"alt-delete", tcell.KeyDelete, 0, tcell.ModAlt, []byte{0x1b, 0x7f}},

		// --- Bare arrows / Home / End / Page ---
		{"up", tcell.KeyUp, 0, tcell.ModNone, []byte("\x1b[A")},
		{"down", tcell.KeyDown, 0, tcell.ModNone, []byte("\x1b[B")},
		{"right", tcell.KeyRight, 0, tcell.ModNone, []byte("\x1b[C")},
		{"left", tcell.KeyLeft, 0, tcell.ModNone, []byte("\x1b[D")},
		{"home", tcell.KeyHome, 0, tcell.ModNone, []byte("\x1b[H")},
		{"end", tcell.KeyEnd, 0, tcell.ModNone, []byte("\x1b[F")},
		{"pgup", tcell.KeyPgUp, 0, tcell.ModNone, []byte("\x1b[5~")},
		{"pgdn", tcell.KeyPgDn, 0, tcell.ModNone, []byte("\x1b[6~")},

		// --- Alt+arrow (preserve prior tcellKeyToBytes output: mod 3) ---
		{"alt-up", tcell.KeyUp, 0, tcell.ModAlt, []byte("\x1b[1;3A")},
		{"alt-down", tcell.KeyDown, 0, tcell.ModAlt, []byte("\x1b[1;3B")},
		{"alt-right", tcell.KeyRight, 0, tcell.ModAlt, []byte("\x1b[1;3C")},
		{"alt-left", tcell.KeyLeft, 0, tcell.ModAlt, []byte("\x1b[1;3D")},

		// --- NEW: modified arrows (Shift=2, Alt=3, Ctrl=5, Ctrl+Shift=6, Ctrl+Alt=7) ---
		{"shift-right", tcell.KeyRight, 0, tcell.ModShift, []byte("\x1b[1;2C")},
		{"ctrl-right", tcell.KeyRight, 0, tcell.ModCtrl, []byte("\x1b[1;5C")},
		{"ctrl-shift-right", tcell.KeyRight, 0, tcell.ModCtrl | tcell.ModShift, []byte("\x1b[1;6C")},
		{"ctrl-alt-right", tcell.KeyRight, 0, tcell.ModCtrl | tcell.ModAlt, []byte("\x1b[1;7C")},
		{"ctrl-shift-alt-right", tcell.KeyRight, 0, tcell.ModCtrl | tcell.ModShift | tcell.ModAlt, []byte("\x1b[1;8C")},
		{"shift-left", tcell.KeyLeft, 0, tcell.ModShift, []byte("\x1b[1;2D")},
		{"ctrl-left", tcell.KeyLeft, 0, tcell.ModCtrl, []byte("\x1b[1;5D")},
		{"ctrl-up", tcell.KeyUp, 0, tcell.ModCtrl, []byte("\x1b[1;5A")},
		{"ctrl-down", tcell.KeyDown, 0, tcell.ModCtrl, []byte("\x1b[1;5B")},
		{"shift-up", tcell.KeyUp, 0, tcell.ModShift, []byte("\x1b[1;2A")},
		// Home/End modified too.
		{"ctrl-home", tcell.KeyHome, 0, tcell.ModCtrl, []byte("\x1b[1;5H")},
		{"shift-end", tcell.KeyEnd, 0, tcell.ModShift, []byte("\x1b[1;2F")},

		// --- Ctrl+letter C0 controls (byte-identical to tcellKeyToBytes) ---
		{"ctrl-a", tcell.KeyCtrlA, 0, tcell.ModNone, []byte{0x01}},
		{"ctrl-b", tcell.KeyCtrlB, 0, tcell.ModNone, []byte{0x02}},
		{"ctrl-c", tcell.KeyCtrlC, 0, tcell.ModNone, []byte{0x03}},
		{"ctrl-q", tcell.KeyCtrlQ, 0, tcell.ModNone, []byte{0x11}},
		{"ctrl-d", tcell.KeyCtrlD, 0, tcell.ModNone, []byte{0x04}},
		{"ctrl-e", tcell.KeyCtrlE, 0, tcell.ModNone, []byte{0x05}},
		{"ctrl-f", tcell.KeyCtrlF, 0, tcell.ModNone, []byte{0x06}},
		{"ctrl-g", tcell.KeyCtrlG, 0, tcell.ModNone, []byte{0x07}},
		{"ctrl-h", tcell.KeyCtrlH, 0, tcell.ModNone, []byte{0x08}},
		{"ctrl-k", tcell.KeyCtrlK, 0, tcell.ModNone, []byte{0x0b}},
		{"ctrl-l", tcell.KeyCtrlL, 0, tcell.ModNone, []byte{0x0c}},
		{"ctrl-n", tcell.KeyCtrlN, 0, tcell.ModNone, []byte{0x0e}},
		{"ctrl-o", tcell.KeyCtrlO, 0, tcell.ModNone, []byte{0x0f}},
		{"ctrl-p", tcell.KeyCtrlP, 0, tcell.ModNone, []byte{0x10}},
		{"ctrl-r", tcell.KeyCtrlR, 0, tcell.ModNone, []byte{0x12}},
		{"ctrl-s", tcell.KeyCtrlS, 0, tcell.ModNone, []byte{0x13}},
		{"ctrl-t", tcell.KeyCtrlT, 0, tcell.ModNone, []byte{0x14}},
		{"ctrl-u", tcell.KeyCtrlU, 0, tcell.ModNone, []byte{0x15}},
		{"ctrl-v", tcell.KeyCtrlV, 0, tcell.ModNone, []byte{0x16}},
		{"ctrl-w", tcell.KeyCtrlW, 0, tcell.ModNone, []byte{0x17}},
		{"ctrl-x", tcell.KeyCtrlX, 0, tcell.ModNone, []byte{0x18}},
		{"ctrl-y", tcell.KeyCtrlY, 0, tcell.ModNone, []byte{0x19}},
		{"ctrl-z", tcell.KeyCtrlZ, 0, tcell.ModNone, []byte{0x1a}},

		// --- Escape ---
		{"escape", tcell.KeyEscape, 0, tcell.ModNone, []byte{0x1b}},

		// --- Unmapped ---
		{"f1", tcell.KeyF1, 0, tcell.ModNone, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ev := tcell.NewEventKey(tt.key, tt.r, tt.mod)
			testutil.DeepEqual(t, Encode(ev), tt.want)
		})
	}
}
