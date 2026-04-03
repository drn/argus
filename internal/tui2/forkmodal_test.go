package tui2

import (
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

func testProjects() map[string]config.Project {
	return map[string]config.Project{
		"alpha": {Path: "/tmp/alpha"},
		"beta":  {Path: "/tmp/beta"},
	}
}

func TestForkTaskModal_Enter(t *testing.T) {
	task := &model.Task{Name: "test-task", Project: "alpha", Worktree: "/tmp/wt"}
	m := NewForkTaskModal(task, testProjects())

	testutil.Equal(t, m.Confirmed(), false)
	testutil.Equal(t, m.Canceled(), false)

	handler := m.InputHandler()
	handler(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone), func(p tview.Primitive) {})

	testutil.Equal(t, m.Confirmed(), true)
	testutil.Equal(t, m.Canceled(), false)
	testutil.Equal(t, m.Task().Name, "test-task")
	testutil.Equal(t, m.SelectedProject(), "alpha")
}

func TestForkTaskModal_Escape(t *testing.T) {
	task := &model.Task{Name: "test-task", Project: "alpha"}
	m := NewForkTaskModal(task, testProjects())

	handler := m.InputHandler()
	handler(tcell.NewEventKey(tcell.KeyEscape, 0, tcell.ModNone), func(p tview.Primitive) {})

	testutil.Equal(t, m.Confirmed(), false)
	testutil.Equal(t, m.Canceled(), true)
}

func TestForkTaskModal_CtrlQ(t *testing.T) {
	task := &model.Task{Name: "test-task", Project: "alpha"}
	m := NewForkTaskModal(task, testProjects())

	handler := m.InputHandler()
	handler(tcell.NewEventKey(tcell.KeyCtrlQ, 0, tcell.ModNone), func(p tview.Primitive) {})

	testutil.Equal(t, m.Confirmed(), false)
	testutil.Equal(t, m.Canceled(), true)
}

func TestForkTaskModal_Draw(t *testing.T) {
	task := &model.Task{
		Name:     "my-task",
		Project:  "alpha",
		Worktree: "/path/to/worktree",
		Branch:   "argus/my-task",
	}
	m := NewForkTaskModal(task, testProjects())
	m.SetRect(0, 0, 80, 24)

	screen := tcell.NewSimulationScreen("")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	screen.SetSize(80, 24)

	// Should not panic.
	m.Draw(screen)
}

func TestForkTaskModal_ChangeProject(t *testing.T) {
	task := &model.Task{Name: "test-task", Project: "alpha"}
	m := NewForkTaskModal(task, testProjects())

	handler := m.InputHandler()

	// Clear the pre-filled project name
	handler(tcell.NewEventKey(tcell.KeyCtrlU, 0, tcell.ModNone), func(p tview.Primitive) {})
	testutil.Equal(t, string(m.projInput), "")

	// Type "beta"
	for _, ch := range "beta" {
		handler(tcell.NewEventKey(tcell.KeyRune, ch, tcell.ModNone), func(p tview.Primitive) {})
	}

	// Accept the autocomplete match
	testutil.Equal(t, m.projACOpen, true)
	handler(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone), func(p tview.Primitive) {})

	testutil.Equal(t, m.SelectedProject(), "beta")
	testutil.Equal(t, m.Confirmed(), false) // Enter in AC doesn't confirm
}

func TestForkTaskModal_ProjectDefaultsToSource(t *testing.T) {
	task := &model.Task{Name: "test-task", Project: "alpha"}
	m := NewForkTaskModal(task, testProjects())

	testutil.Equal(t, m.SelectedProject(), "alpha")
}
