package tui

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

func TestForkTaskModal_PasteHandler(t *testing.T) {
	task := &model.Task{Name: "t", Project: "alpha"}
	m := NewForkTaskModal(task, map[string]config.Project{"alpha": {}, "beta": {}})

	m.projInput = nil
	m.projCursorPos = 0
	paste := m.PasteHandler()
	paste("be", func(p tview.Primitive) {})
	testutil.Equal(t, string(m.projInput), "be")
}

func TestForkTaskModal_ACMoveDownAndUp(t *testing.T) {
	task := &model.Task{Name: "t", Project: "alpha"}
	m := NewForkTaskModal(task, map[string]config.Project{
		"alpha": {}, "beta": {}, "gamma": {},
	})

	m.projInput = nil
	m.projCursorPos = 0
	m.updateProjectAC()
	testutil.Equal(t, m.projACOpen, true)
	testutil.Equal(t, m.projACIdx, 0)

	m.projACMoveDown()
	testutil.Equal(t, m.projACIdx, 1)

	m.projACMoveUp()
	testutil.Equal(t, m.projACIdx, 0)

	m.projACMoveUp()
	testutil.Equal(t, m.projACIdx, 2)
}

func TestForkTaskModal_ACMove_EmptyMatches(t *testing.T) {
	task := &model.Task{Name: "t", Project: ""}
	m := NewForkTaskModal(task, map[string]config.Project{})
	m.projACMoveDown()
	m.projACMoveUp()
}

func TestForkTaskModal_AC_Down_Tab_Up_Backtab_Escape(t *testing.T) {
	task := &model.Task{Name: "t", Project: "alpha"}
	m := NewForkTaskModal(task, map[string]config.Project{
		"alpha": {}, "beta": {},
	})
	m.projInput = nil
	m.projCursorPos = 0
	m.updateProjectAC()
	handler := m.InputHandler()

	handler(tcell.NewEventKey(tcell.KeyTab, 0, 0), func(p tview.Primitive) {})
	testutil.Equal(t, m.projACIdx, 1)

	handler(tcell.NewEventKey(tcell.KeyBacktab, 0, 0), func(p tview.Primitive) {})
	testutil.Equal(t, m.projACIdx, 0)

	handler(tcell.NewEventKey(tcell.KeyEscape, 0, 0), func(p tview.Primitive) {})
	testutil.Equal(t, m.projACOpen, false)
	testutil.Equal(t, m.Canceled(), false)
}

func TestForkTaskModal_LeftRight_CtrlA_CtrlE_CtrlU(t *testing.T) {
	task := &model.Task{Name: "t", Project: "alpha"}
	m := NewForkTaskModal(task, map[string]config.Project{"alpha": {}})
	handler := m.InputHandler()

	testutil.Equal(t, m.projCursorPos, 5)
	handler(tcell.NewEventKey(tcell.KeyLeft, 0, 0), func(p tview.Primitive) {})
	testutil.Equal(t, m.projCursorPos, 4)
	handler(tcell.NewEventKey(tcell.KeyRight, 0, 0), func(p tview.Primitive) {})
	testutil.Equal(t, m.projCursorPos, 5)

	handler(tcell.NewEventKey(tcell.KeyCtrlA, 0, 0), func(p tview.Primitive) {})
	testutil.Equal(t, m.projCursorPos, 0)
	handler(tcell.NewEventKey(tcell.KeyCtrlE, 0, 0), func(p tview.Primitive) {})
	testutil.Equal(t, m.projCursorPos, 5)

	handler(tcell.NewEventKey(tcell.KeyCtrlU, 0, 0), func(p tview.Primitive) {})
	testutil.Equal(t, len(m.projInput), 0)
}

func TestForkTaskModal_Backspace(t *testing.T) {
	task := &model.Task{Name: "t", Project: "alpha"}
	m := NewForkTaskModal(task, map[string]config.Project{"alpha": {}})
	handler := m.InputHandler()
	handler(tcell.NewEventKey(tcell.KeyBackspace2, 0, 0), func(p tview.Primitive) {})
	testutil.Equal(t, string(m.projInput), "alph")
}

func TestForkTaskModal_Draw_ProjectChanged(t *testing.T) {
	task := &model.Task{Name: "t", Project: "alpha"}
	m := NewForkTaskModal(task, map[string]config.Project{"alpha": {}, "beta": {}})

	m.projInput = []rune("beta")
	m.projCursorPos = 4
	m.updateProjectAC()
	m.SetRect(0, 0, 80, 24)
	m.Draw(drawSim(t))
}

func TestForkTaskModal_Draw_TinyRect(t *testing.T) {
	task := &model.Task{Name: "t", Project: "alpha"}
	m := NewForkTaskModal(task, map[string]config.Project{"alpha": {}})
	m.SetRect(0, 0, 0, 0)
	m.Draw(drawSim(t))
}
