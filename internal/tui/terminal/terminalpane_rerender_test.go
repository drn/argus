package terminal

import (
	"strings"
	"testing"

	"github.com/drn/argus/internal/testutil"
	"github.com/gdamore/tcell/v2"
)

func TestTerminalPane_RerenderStatus(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	tp := NewTerminalPane()
	tp.SetTaskID("repair")
	screen := tcell.NewSimulationScreen("UTF-8")
	testutil.NoError(t, screen.Init())
	defer screen.Fini()
	screen.SetSize(80, 24)
	tp.SetRect(0, 0, 80, 24)
	prompt := strings.Repeat("?", 78)
	output := []byte("\x1b[1;1H" + prompt + "\r\nYes / No")
	tp.SetSession(&mockAdapter{alive: true, output: output, totalWritten: uint64(len(output)), ptyCols: 78, ptyRows: 22})
	tp.SetRerenderingForTask("other", true)
	testutil.False(t, tp.Rerendering())
	tp.SetRerenderingForTask("repair", true)
	tp.Draw(screen)
	var content strings.Builder
	for row := range 24 {
		content.WriteString(screenRowText(screen, row, 80))
	}
	testutil.Contains(t, content.String(), "Re-rendering…")
	testutil.Contains(t, screenRowText(screen, 0, 80), "Re-rendering…")
	testutil.Equal(t, screenRowText(screen, 1, 80), "│"+prompt+"│")
	testutil.False(t, strings.Contains(screenRowText(screen, 1, 80), "Re-rendering"))
	tp.SetRerenderPendingForTask("repair")
	tp.Draw(screen)
	testutil.Contains(t, screenRowText(screen, 0, 80), "Re-render pending")
	tp.SetRerenderingForTask("repair", false)
	tp.Draw(screen)
	content.Reset()
	for row := range 24 {
		content.WriteString(screenRowText(screen, row, 80))
	}
	testutil.False(t, strings.Contains(content.String(), "Re-rendering…"))
	tp.SetRerenderingForTask("repair", true)
	tp.ResetVT()
	testutil.False(t, tp.Rerendering())
	tp.SetRerenderingForTask("repair", true)
	tp.SetTaskID("different")
	testutil.False(t, tp.Rerendering())
}
