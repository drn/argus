package modal

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

// drawAt creates a SimulationScreen of the given size, sets the modal's
// rect, and returns the screen for assertions.
func drawAt(t *testing.T, w, h int) tcell.SimulationScreen {
	t.Helper()
	sim := tcell.NewSimulationScreen("UTF-8")
	if err := sim.Init(); err != nil {
		t.Fatal(err)
	}
	sim.SetSize(w, h)
	t.Cleanup(sim.Fini)
	return sim
}

func TestConfirmDeleteModal_Defaults(t *testing.T) {
	task := &model.Task{Name: "fix-bug", Worktree: "/tmp/wt", Branch: "argus/fix"}
	m := NewConfirmDeleteModal(task)
	testutil.False(t, m.Confirmed())
	testutil.False(t, m.Canceled())
	testutil.Equal(t, m.Task(), task)
}

func TestConfirmDeleteModal_InputHandler(t *testing.T) {
	for _, tc := range []struct {
		name     string
		key      tcell.Key
		rune     rune
		wantConf bool
		wantCanc bool
	}{
		{"enter confirms", tcell.KeyEnter, 0, true, false},
		{"esc cancels", tcell.KeyEscape, 0, false, true},
		{"ctrl+q cancels", tcell.KeyCtrlQ, 0, false, true},
		{"unrelated key no-op", tcell.KeyRune, 'x', false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := NewConfirmDeleteModal(&model.Task{Name: "x"})
			handler := m.InputHandler()
			ev := tcell.NewEventKey(tc.key, tc.rune, tcell.ModNone)
			handler(ev, nil)
			testutil.Equal(t, m.Confirmed(), tc.wantConf)
			testutil.Equal(t, m.Canceled(), tc.wantCanc)
		})
	}
}

func TestConfirmDeleteModal_Draw(t *testing.T) {
	sim := drawAt(t, 80, 24)
	task := &model.Task{Name: "fix-bug", Worktree: "/tmp/wt", Branch: "argus/fix-bug"}
	m := NewConfirmDeleteModal(task)
	m.SetRect(0, 0, 80, 24)
	m.Draw(sim)
	sim.Sync()

	body := screenString(sim)
	testutil.Contains(t, body, "Delete task?")
	testutil.Contains(t, body, "fix-bug")
	testutil.Contains(t, body, "worktree:")
	testutil.Contains(t, body, "branch:")
	testutil.Contains(t, body, "[enter] confirm")
}

func TestConfirmDeleteModal_DrawZeroSizeNoOp(t *testing.T) {
	sim := drawAt(t, 80, 24)
	m := NewConfirmDeleteModal(&model.Task{Name: "x"})
	m.SetRect(0, 0, 0, 0)
	m.Draw(sim) // must not panic
}

func TestConfirmDeleteModal_DrawWithoutWorktreeOrBranch(t *testing.T) {
	sim := drawAt(t, 80, 24)
	m := NewConfirmDeleteModal(&model.Task{Name: "no-wt"})
	m.SetRect(0, 0, 80, 24)
	m.Draw(sim)
	sim.Sync()
	body := screenString(sim)
	testutil.Contains(t, body, "no-wt")
	if contains(body, "worktree:") {
		t.Error("worktree label drawn when worktree is empty")
	}
}

func TestConfirmDeleteModal_DrawTinyArea(t *testing.T) {
	// Width < 4 means formW = -1, but the loop guards row < y+height.
	// Cover the negative-formY clamp branch with a very tall area.
	sim := drawAt(t, 80, 24)
	m := NewConfirmDeleteModal(&model.Task{Name: "x"})
	m.SetRect(0, 0, 80, 1)
	m.Draw(sim)
}

func TestConfirmDeleteProjectModal_Defaults(t *testing.T) {
	m := NewConfirmDeleteProjectModal("proj", 3)
	testutil.False(t, m.Confirmed())
	testutil.False(t, m.Canceled())
	testutil.Equal(t, m.Name(), "proj")
}

func TestConfirmDeleteProjectModal_InputHandler(t *testing.T) {
	for _, tc := range []struct {
		name     string
		key      tcell.Key
		rune     rune
		wantConf bool
		wantCanc bool
	}{
		{"enter confirms", tcell.KeyEnter, 0, true, false},
		{"esc cancels", tcell.KeyEscape, 0, false, true},
		{"ctrl+q cancels", tcell.KeyCtrlQ, 0, false, true},
		{"unrelated key no-op", tcell.KeyRune, 'q', false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := NewConfirmDeleteProjectModal("p", 0)
			handler := m.InputHandler()
			handler(tcell.NewEventKey(tc.key, tc.rune, tcell.ModNone), nil)
			testutil.Equal(t, m.Confirmed(), tc.wantConf)
			testutil.Equal(t, m.Canceled(), tc.wantCanc)
		})
	}
}

func TestConfirmDeleteProjectModal_DrawNoOrphans(t *testing.T) {
	sim := drawAt(t, 80, 24)
	m := NewConfirmDeleteProjectModal("alpha", 0)
	m.SetRect(0, 0, 80, 24)
	m.Draw(sim)
	sim.Sync()
	body := screenString(sim)
	testutil.Contains(t, body, "Delete project?")
	testutil.Contains(t, body, "alpha")
	if contains(body, "orphaned") {
		t.Error("orphan warning drawn when taskCount=0")
	}
}

func TestConfirmDeleteProjectModal_DrawWithOrphans(t *testing.T) {
	sim := drawAt(t, 80, 24)
	m := NewConfirmDeleteProjectModal("beta", 3)
	m.SetRect(0, 0, 80, 24)
	m.Draw(sim)
	sim.Sync()
	body := screenString(sim)
	testutil.Contains(t, body, "beta")
	testutil.Contains(t, body, "orphaned")
}

func TestConfirmDeleteProjectModal_DrawZeroSize(t *testing.T) {
	sim := drawAt(t, 80, 24)
	m := NewConfirmDeleteProjectModal("x", 0)
	m.SetRect(0, 0, 0, 0)
	m.Draw(sim)
}

func TestRestartDaemonModal_Draw(t *testing.T) {
	sim := drawAt(t, 80, 24)
	m := NewRestartDaemonModal()
	m.SetRect(0, 0, 80, 24)
	m.Draw(sim)
	sim.Sync()
	body := screenString(sim)
	testutil.Contains(t, body, "Daemon out of date")
	testutil.Contains(t, body, "Restart")
	testutil.Contains(t, body, "Skip")
}

func TestRestartDaemonModal_DrawSkipSelected(t *testing.T) {
	sim := drawAt(t, 80, 24)
	m := NewRestartDaemonModal()
	// Move selection to Skip via Tab.
	m.InputHandler()(tcell.NewEventKey(tcell.KeyTab, 0, tcell.ModNone), nil)
	m.SetRect(0, 0, 80, 24)
	m.Draw(sim)
}

func TestRestartDaemonModal_DrawZeroSize(t *testing.T) {
	sim := drawAt(t, 80, 24)
	m := NewRestartDaemonModal()
	m.SetRect(0, 0, 0, 0)
	m.Draw(sim)
}

func TestRestartDaemonModal_HLNavigation(t *testing.T) {
	m := NewRestartDaemonModal()
	h := m.InputHandler()
	h(tcell.NewEventKey(tcell.KeyRune, 'l', tcell.ModNone), nil)
	testutil.Equal(t, m.Selected(), 1)
	h(tcell.NewEventKey(tcell.KeyRune, 'h', tcell.ModNone), nil)
	testutil.Equal(t, m.Selected(), 0)
}

func TestRestartDaemonModal_UppercaseShortcuts(t *testing.T) {
	m := NewRestartDaemonModal()
	m.InputHandler()(tcell.NewEventKey(tcell.KeyRune, 'R', tcell.ModNone), nil)
	testutil.True(t, m.ChoseRestart())

	m2 := NewRestartDaemonModal()
	m2.InputHandler()(tcell.NewEventKey(tcell.KeyRune, 'S', tcell.ModNone), nil)
	testutil.True(t, m2.ChoseSkip())
}

// ---- helpers ----

// screenString returns the contents of the simulation screen as a single string.
func screenString(sim tcell.SimulationScreen) string {
	cells, w, h := sim.GetContents()
	var buf []rune
	for row := range h {
		for col := range w {
			r := cells[row*w+col].Runes
			if len(r) == 0 {
				buf = append(buf, ' ')
			} else {
				buf = append(buf, r[0])
			}
		}
		buf = append(buf, '\n')
	}
	return string(buf)
}

func contains(s, sub string) bool {
	return strings.Contains(s, sub)
}
