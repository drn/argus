package tui2

import (
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

func TestForkTaskModal_Enter(t *testing.T) {
	task := &model.Task{Name: "test-task", Worktree: "/tmp/wt"}
	m := NewForkTaskModal(task)

	testutil.Equal(t, m.Confirmed(), false)
	testutil.Equal(t, m.Canceled(), false)

	handler := m.InputHandler()
	handler(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone), func(p tview.Primitive) {})

	testutil.Equal(t, m.Confirmed(), true)
	testutil.Equal(t, m.Canceled(), false)
	testutil.Equal(t, m.Task().Name, "test-task")
}

func TestForkTaskModal_Escape(t *testing.T) {
	task := &model.Task{Name: "test-task"}
	m := NewForkTaskModal(task)

	handler := m.InputHandler()
	handler(tcell.NewEventKey(tcell.KeyEscape, 0, tcell.ModNone), func(p tview.Primitive) {})

	testutil.Equal(t, m.Confirmed(), false)
	testutil.Equal(t, m.Canceled(), true)
}

func TestForkTaskModal_CtrlQ(t *testing.T) {
	task := &model.Task{Name: "test-task"}
	m := NewForkTaskModal(task)

	handler := m.InputHandler()
	handler(tcell.NewEventKey(tcell.KeyCtrlQ, 0, tcell.ModNone), func(p tview.Primitive) {})

	testutil.Equal(t, m.Confirmed(), false)
	testutil.Equal(t, m.Canceled(), true)
}

func TestForkTaskModal_Draw(t *testing.T) {
	task := &model.Task{
		Name:     "my-task",
		Worktree: "/path/to/worktree",
		Branch:   "argus/my-task",
	}
	m := NewForkTaskModal(task)
	m.SetRect(0, 0, 80, 24)

	screen := tcell.NewSimulationScreen("")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	screen.SetSize(80, 24)

	// Should not panic.
	m.Draw(screen)
}
