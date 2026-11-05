package terminal

import (
	"testing"

	"github.com/drn/argus/internal/testutil"
	"github.com/gdamore/tcell/v2"
)

func TestTakePaint(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	for _, tc := range []struct {
		name          string
		width, height int
		diff, want    bool
	}{
		{"live", 80, 24, false, true},
		{"empty", 0, 0, false, false},
		{"border only", 2, 2, false, false},
		{"diff", 80, 24, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sim := tcell.NewSimulationScreen("UTF-8")
			testutil.NoError(t, sim.Init())
			t.Cleanup(sim.Fini)
			sim.SetSize(80, 24)
			pane := NewTerminalPane()
			pane.SetTaskID("paint-test")
			sess := &mockAdapter{alive: true, output: []byte("echo"), totalWritten: 4}
			pane.SetSession(sess)
			pane.SetRect(0, 0, tc.width, tc.height)
			if tc.diff {
				pane.EnterDiffMode("", "file")
			}
			pane.Draw(sim)
			got, total, drawn := pane.TakePaint()
			testutil.Equal(t, got == sess, true)
			testutil.Equal(t, drawn, tc.want)
			if tc.want {
				testutil.Equal(t, total, uint64(4))
			}
			_, _, drawn = pane.TakePaint()
			testutil.Equal(t, drawn, false)
			testutil.Equal(t, pane.TaskID(), "paint-test")
		})
	}
}
