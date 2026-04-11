package tui

import (
	"testing"

	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
	"github.com/gdamore/tcell/v2"
)

func TestStatusBar_InfoMessage(t *testing.T) {
	sb := NewStatusBar()
	sb.SetRect(0, 0, 80, 1)

	screen := tcell.NewSimulationScreen("UTF-8")
	screen.Init()
	screen.SetSize(80, 1)
	defer screen.Fini()

	// SetInfo shows the message on screen.
	sb.SetInfo("Creating worktree…")
	sb.Draw(screen)
	screen.Show()
	testutil.Contains(t, readScreenRow(screen, 0, 80), "Creating worktree…")

	// ClearInfo restores task counts.
	sb.ClearInfo()
	sb.Draw(screen)
	screen.Show()
	row := readScreenRow(screen, 0, 80)
	testutil.Contains(t, row, "0 active")
}

func TestStatusBar_ErrorTakesPrecedenceOverInfo(t *testing.T) {
	sb := NewStatusBar()
	sb.SetTasks([]*model.Task{
		{Status: model.StatusPending},
	})

	screen := tcell.NewSimulationScreen("UTF-8")
	screen.Init()
	screen.SetSize(80, 1)

	// Info message shows when no error.
	sb.SetInfo("working…")
	sb.SetRect(0, 0, 80, 1)
	sb.Draw(screen)
	screen.Show()
	content := readScreenRow(screen, 0, 80)
	testutil.Contains(t, content, "working…")

	// Error takes precedence over info.
	sb.SetError("something broke")
	sb.Draw(screen)
	screen.Show()
	content = readScreenRow(screen, 0, 80)
	testutil.Contains(t, content, "something broke")

	// Clearing error reveals info again.
	sb.ClearError()
	sb.Draw(screen)
	screen.Show()
	content = readScreenRow(screen, 0, 80)
	testutil.Contains(t, content, "working…")

	screen.Fini()
}

// readScreenRow reads a row of cells from a SimulationScreen into a string.
func readScreenRow(screen tcell.SimulationScreen, row, width int) string {
	var runes []rune
	for col := 0; col < width; col++ {
		r, _, _, _ := screen.GetContent(col, row)
		runes = append(runes, r)
	}
	return string(runes)
}
