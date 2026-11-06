package hera

import (
	"testing"
	"time"

	"github.com/drn/argus/internal/testutil"
	"github.com/drn/argus/internal/tui/terminal"
	"github.com/gdamore/tcell/v2"
)

func TestForwardKeyTiming(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		key  tcell.Key
		r    rune
		want string
	}{
		{"text", tcell.KeyRune, 'x', "x"},
		{"escape", tcell.KeyEscape, 0, "\x1b"},
		{"interrupt", tcell.KeyCtrlC, 0, "\x03"},
		{"scroll", tcell.KeyPgDn, 0, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := NewHeraPage(nil)
			pane := p.AgentPane()
			sess := &fakeSession{alive: true}
			pane.SetSession(sess)
			starts, finishes := 0, 0
			event := tcell.NewEventKey(tc.key, tc.r, 0)
			p.OnForwardKey = func(tp *terminal.TerminalPane, ev *tcell.EventKey, entered time.Time) func() {
				starts++
				testutil.Equal(t, tp, pane)
				testutil.Equal(t, ev, event)
				testutil.Equal(t, entered.Before(event.When()), false)
				testutil.Equal(t, len(sess.wrote), 0)
				return func() { finishes++; testutil.Equal(t, string(sess.wrote), tc.want) }
			}
			p.forwardKey(pane, event)
			want := 0
			if tc.want != "" {
				want = 1
			}
			testutil.Equal(t, starts, want)
			testutil.Equal(t, finishes, want)
		})
	}
}
