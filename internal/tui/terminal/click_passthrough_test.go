package terminal

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// TestTerminalPane_MouseLeftClick_FocusVsForward covers MouseHandler's
// three-way click branch (see openspec/changes/add-click-passthrough):
//   - unfocused pane: a click focuses the pane, never forwards
//   - focused pane + live session: the click forwards to the agent's PTY as
//     an SGR press/release pair, and OnClick is NOT called
//   - focused pane + no live session (nil, or present but dead): OnClick is
//     called as before, and nothing is forwarded
func TestTerminalPane_MouseLeftClick_FocusVsForward(t *testing.T) {
	cases := []struct {
		name          string
		focused       bool
		session       *recAdapter // nil means no session attached at all
		wantSetFocus  bool
		wantOnClick   bool
		wantForwarded bool
	}{
		{
			name:         "unfocused pane focuses, does not forward",
			focused:      false,
			session:      &recAdapter{alive: true},
			wantSetFocus: true,
			wantOnClick:  true,
		},
		{
			name:          "focused pane with live session forwards, no OnClick",
			focused:       true,
			session:       &recAdapter{alive: true},
			wantForwarded: true,
		},
		{
			// Preserves today's (pre-change) behavior exactly: setFocus + OnClick
			// unconditionally, since there's no live session to forward to.
			name:         "focused pane with no session falls back to OnClick",
			focused:      true,
			session:      nil,
			wantSetFocus: true,
			wantOnClick:  true,
		},
		{
			name:         "focused pane with dead session falls back to OnClick",
			focused:      true,
			session:      &recAdapter{alive: false},
			wantSetFocus: true,
			wantOnClick:  true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tp := NewTerminalPane()
			tp.SetTaskID("")
			if tc.session != nil {
				tp.SetSession(tc.session)
			}
			tp.SetFocused(tc.focused)
			tp.SetRect(0, 0, 80, 24)

			var setFocusCalled, onClickCalled bool
			tp.OnClick = func() { onClickCalled = true }

			h := tp.MouseHandler()
			down := tcell.NewEventMouse(10, 5, tcell.Button1, tcell.ModNone)
			h(tview.MouseLeftDown, down, func(tview.Primitive) { setFocusCalled = true })
			up := tcell.NewEventMouse(10, 5, tcell.ButtonNone, tcell.ModNone)
			h(tview.MouseLeftUp, up, func(tview.Primitive) {})

			if setFocusCalled != tc.wantSetFocus {
				t.Errorf("setFocus called = %v, want %v", setFocusCalled, tc.wantSetFocus)
			}
			if onClickCalled != tc.wantOnClick {
				t.Errorf("OnClick called = %v, want %v", onClickCalled, tc.wantOnClick)
			}

			var wrote string
			if tc.session != nil {
				wrote = string(tc.session.wrote)
			}
			forwarded := strings.Contains(wrote, "\x1b[<0;")
			if forwarded != tc.wantForwarded {
				t.Errorf("forwarded to session = %v (wrote %q), want %v", forwarded, wrote, tc.wantForwarded)
			}
			if tc.wantForwarded {
				if !strings.Contains(wrote, "\x1b[<0;10;5M") {
					t.Errorf("expected SGR press ESC[<0;10;5M, got %q", wrote)
				}
				if !strings.Contains(wrote, "\x1b[<0;10;5m") {
					t.Errorf("expected SGR release ESC[<0;10;5m, got %q", wrote)
				}
			}
		})
	}
}

// TestTerminalPane_MouseLeftUp_WithoutPriorDown_NoOp guards the footgun called
// out in the design: tview fires MouseLeftDown -> MouseLeftUp -> MouseLeftClick
// for a single click, but a MouseLeftUp arriving with no matching Down (e.g. a
// drag that started outside the pane) must not fire a stray release.
func TestTerminalPane_MouseLeftUp_WithoutPriorDown_NoOp(t *testing.T) {
	sess := &recAdapter{alive: true}
	tp := NewTerminalPane()
	tp.SetTaskID("")
	tp.SetSession(sess)
	tp.SetFocused(true)
	tp.SetRect(0, 0, 80, 24)

	up := tcell.NewEventMouse(10, 5, tcell.ButtonNone, tcell.ModNone)
	consumed, _ := tp.MouseHandler()(tview.MouseLeftUp, up, func(tview.Primitive) {})
	if consumed {
		t.Error("MouseLeftUp with no preceding forwarded Down should not be consumed")
	}
	if len(sess.wrote) != 0 {
		t.Errorf("expected no bytes written, got %q", sess.wrote)
	}
}

// TestTerminalPane_MouseLeftClick_DoesNotDuplicateForward guards the other
// footgun: tview always fires a synthetic MouseLeftClick after Up, and it must
// not re-run the press/release forwarding (or the focus-switch/OnClick path).
func TestTerminalPane_MouseLeftClick_DoesNotDuplicateForward(t *testing.T) {
	sess := &recAdapter{alive: true}
	tp := NewTerminalPane()
	tp.SetTaskID("")
	tp.SetSession(sess)
	tp.SetFocused(true)
	tp.SetRect(0, 0, 80, 24)

	h := tp.MouseHandler()
	down := tcell.NewEventMouse(10, 5, tcell.Button1, tcell.ModNone)
	h(tview.MouseLeftDown, down, func(tview.Primitive) {})
	up := tcell.NewEventMouse(10, 5, tcell.ButtonNone, tcell.ModNone)
	h(tview.MouseLeftUp, up, func(tview.Primitive) {})
	wroteAfterUp := string(sess.wrote)

	click := tcell.NewEventMouse(10, 5, tcell.ButtonNone, tcell.ModNone)
	h(tview.MouseLeftClick, click, func(tview.Primitive) {})
	if string(sess.wrote) != wroteAfterUp {
		t.Errorf("MouseLeftClick must not write additional bytes: before %q, after %q", wroteAfterUp, sess.wrote)
	}
}
