package tui2

import (
	"testing"
	"time"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/gdamore/tcell/v2"
)

func testDB(t *testing.T) *db.DB {
	t.Helper()
	d, err := db.OpenInMemory()
	if err != nil {
		t.Fatalf("OpenInMemory: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func TestNew(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)

	if app.tapp == nil {
		t.Error("tview.Application should not be nil")
	}
	if app.header == nil {
		t.Error("header should not be nil")
	}
	if app.statusbar == nil {
		t.Error("statusbar should not be nil")
	}
	if app.tasklist == nil {
		t.Error("tasklist should not be nil")
	}
	if app.mode != modeTaskList {
		t.Errorf("initial mode = %v, want modeTaskList", app.mode)
	}
	if app.daemonConnected {
		t.Error("daemonConnected should be false")
	}
}

func TestSwitchTab(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)

	app.switchTab(TabReviews)
	if app.header.ActiveTab() != TabReviews {
		t.Errorf("tab = %v, want TabReviews", app.header.ActiveTab())
	}

	app.switchTab(TabSettings)
	if app.header.ActiveTab() != TabSettings {
		t.Errorf("tab = %v, want TabSettings", app.header.ActiveTab())
	}

	app.switchTab(TabTasks)
	if app.header.ActiveTab() != TabTasks {
		t.Errorf("tab = %v, want TabTasks", app.header.ActiveTab())
	}
}

func TestOnTaskSelect(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)

	task := &model.Task{
		ID:   "test-1",
		Name: "test task",
	}

	app.onTaskSelect(task)

	if app.mode != modeAgent {
		t.Errorf("mode = %v, want modeAgent", app.mode)
	}
	if app.agentState.TaskID != "test-1" {
		t.Errorf("agentState.TaskID = %q, want %q", app.agentState.TaskID, "test-1")
	}
}

func TestExitAgentView(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)

	app.mode = modeAgent
	app.exitAgentView()

	if app.mode != modeTaskList {
		t.Errorf("mode = %v, want modeTaskList", app.mode)
	}
}

func TestTcellKeyToBytes(t *testing.T) {
	tests := []struct {
		name string
		key  tcell.Key
		rune rune
		mod  tcell.ModMask
		want []byte
	}{
		{"enter", tcell.KeyEnter, 0, 0, []byte{'\r'}},
		{"tab", tcell.KeyTab, 0, 0, []byte{'\t'}},
		{"backspace", tcell.KeyBackspace2, 0, 0, []byte{0x7f}},
		{"up", tcell.KeyUp, 0, 0, []byte("\x1b[A")},
		{"down", tcell.KeyDown, 0, 0, []byte("\x1b[B")},
		{"right", tcell.KeyRight, 0, 0, []byte("\x1b[C")},
		{"left", tcell.KeyLeft, 0, 0, []byte("\x1b[D")},
		{"ctrl-c", tcell.KeyCtrlC, 0, 0, []byte{0x03}},
		{"ctrl-d", tcell.KeyCtrlD, 0, 0, []byte{0x04}},
		{"escape", tcell.KeyEscape, 0, 0, []byte{0x1b}},
		{"rune-a", tcell.KeyRune, 'a', 0, []byte("a")},
		{"rune-alt-a", tcell.KeyRune, 'a', tcell.ModAlt, []byte{0x1b, 'a'}},
		{"delete", tcell.KeyDelete, 0, 0, []byte("\x1b[3~")},
		// Alt+arrow keys for word navigation
		{"alt-left", tcell.KeyLeft, 0, tcell.ModAlt, []byte("\x1b[1;3D")},
		{"alt-right", tcell.KeyRight, 0, tcell.ModAlt, []byte("\x1b[1;3C")},
		{"alt-up", tcell.KeyUp, 0, tcell.ModAlt, []byte("\x1b[1;3A")},
		{"alt-down", tcell.KeyDown, 0, tcell.ModAlt, []byte("\x1b[1;3B")},
		{"alt-backspace", tcell.KeyBackspace2, 0, tcell.ModAlt, []byte{0x1b, 0x7f}},
		{"alt-delete", tcell.KeyDelete, 0, tcell.ModAlt, []byte{0x1b, 0x7f}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ev := tcell.NewEventKey(tt.key, tt.rune, tt.mod)
			got := tcellKeyToBytes(ev)
			if string(got) != string(tt.want) {
				t.Errorf("tcellKeyToBytes(%v) = %v, want %v", tt.name, got, tt.want)
			}
		})
	}
}

// ptySizeForPanel lives in internal/ui/terminal_adapter.go (shared) — tested there.

func TestRefreshTasks(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)

	// Add a task
	task := &model.Task{
		ID:        "t1",
		Name:      "task one",
		Status:    model.StatusPending,
		Project:   "proj",
		CreatedAt: time.Now(),
	}
	d.Add(task)

	app.refreshTasks()

	if len(app.tasks) != 1 {
		t.Errorf("len(tasks) = %d, want 1", len(app.tasks))
	}
	if !app.tasklist.HasTasks() {
		t.Error("tasklist should have tasks")
	}
}
