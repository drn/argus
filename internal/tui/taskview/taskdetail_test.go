package taskview

import (
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

func TestTaskDetailPanel_DrawNilTask(t *testing.T) {
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	screen.SetSize(40, 20)

	td := NewTaskDetailPanel()
	td.SetRect(1, 1, 38, 18)
	td.Draw(screen)
	// Should not panic with nil task
}

func TestTaskDetailPanel_DrawWithTask(t *testing.T) {
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	screen.SetSize(40, 20)

	td := NewTaskDetailPanel()
	td.SetRect(1, 1, 38, 18)

	task := &model.Task{
		ID:        "test-1",
		Name:      "fix-the-bug",
		Status:    model.StatusInProgress,
		Project:   "argus",
		Branch:    "argus/fix-the-bug",
		Backend:   "claude",
		Worktree:  "/Users/test/.argus/worktrees/argus/fix-the-bug",
		Prompt:    "Fix the critical bug in the login flow",
		CreatedAt: time.Now(),
	}
	task.SetStatus(model.StatusInProgress)

	td.SetTask(task, true)
	td.Draw(screen)
	// Should render without panic
}

func TestTaskDetailPanel_ZeroDimensions(t *testing.T) {
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	screen.SetSize(1, 1)

	td := NewTaskDetailPanel()
	td.SetRect(0, 0, 0, 0)
	td.Draw(screen) // must not panic
}

func TestTaskDetailPanel_SandboxIndicator(t *testing.T) {
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	screen.SetSize(40, 20)

	td := NewTaskDetailPanel()
	td.SetRect(1, 1, 38, 18)

	task := &model.Task{
		ID:      "test-sb",
		Name:    "sandbox-test",
		Status:  model.StatusPending,
		Project: "argus",
		Backend: "claude",
	}

	readScreen := func() string {
		var buf strings.Builder
		w, h := screen.Size()
		for row := 0; row < h; row++ {
			for col := 0; col < w; col++ {
				ch, _, _, _ := screen.GetContent(col, row)
				buf.WriteRune(ch)
			}
			buf.WriteRune('\n')
		}
		return buf.String()
	}

	t.Run("sandboxed", func(t *testing.T) {
		task.Sandboxed = true
		td.SetTask(task, false)
		td.Draw(screen)
		content := readScreen()
		testutil.Contains(t, content, "Sandbox")
		testutil.Contains(t, content, "Yes")
	})

	t.Run("not sandboxed", func(t *testing.T) {
		task.Sandboxed = false
		td.SetTask(task, false)
		td.Draw(screen)
		content := readScreen()
		testutil.Contains(t, content, "Sandbox")
		testutil.Contains(t, content, "No")
	})
}

func TestTaskDetailPanel_PIDIndicator(t *testing.T) {
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	screen.SetSize(40, 20)

	td := NewTaskDetailPanel()
	td.SetRect(1, 1, 38, 18)

	task := &model.Task{
		ID:      "test-pid",
		Name:    "pid-test",
		Status:  model.StatusPending,
		Project: "argus",
		Backend: "claude",
	}

	readScreen := func() string {
		var buf strings.Builder
		w, h := screen.Size()
		for row := 0; row < h; row++ {
			for col := 0; col < w; col++ {
				str, _, _ := screen.Get(col, row)
				buf.WriteString(str)
			}
			buf.WriteRune('\n')
		}
		return buf.String()
	}

	t.Run("no PID recorded", func(t *testing.T) {
		task.AgentPID = 0
		td.SetTask(task, false)
		td.Draw(screen)
		content := readScreen()
		if strings.Contains(content, "PID") {
			t.Error("expected no PID row when AgentPID is 0")
		}
	})

	t.Run("PID recorded", func(t *testing.T) {
		task.AgentPID = 17557
		td.SetTask(task, false)
		td.Draw(screen)
		content := readScreen()
		testutil.Contains(t, content, "PID")
		testutil.Contains(t, content, "17557")
	})

	t.Run("stale PID kept for a non-running task", func(t *testing.T) {
		task.Status = model.StatusInReview
		task.AgentPID = 17557
		td.SetTask(task, false)
		td.Draw(screen)
		content := readScreen()
		testutil.Contains(t, content, "PID")
		testutil.Contains(t, content, "17557")
	})
}

func TestTaskDetailPanel_TaskShapeFiresOnPIDChange(t *testing.T) {
	td := NewTaskDetailPanel()

	task := &model.Task{ID: "t1", Name: "n", Status: model.StatusInProgress, AgentPID: 100}

	fired := 0
	td.OnBranchChange = func() { fired++ }

	td.SetTask(task, true) // first call always fires (sentinel lastShape)
	if fired != 1 {
		t.Fatalf("expected first SetTask to fire OnBranchChange, fired=%d", fired)
	}

	// Same PID, same everything else — no re-fire.
	td.SetTask(task, true)
	if fired != 1 {
		t.Fatalf("expected no re-fire when nothing changed, fired=%d", fired)
	}

	// PID changes (e.g. task restarted under a new PID) — must fire.
	task.AgentPID = 200
	td.SetTask(task, true)
	if fired != 2 {
		t.Fatalf("expected OnBranchChange to fire when AgentPID changes, fired=%d", fired)
	}
}

func TestTaskDetailPanel_WrapText(t *testing.T) {
	td := NewTaskDetailPanel()
	lines := td.wrapText("the quick brown fox jumps over the lazy dog", 15)
	if len(lines) < 2 {
		t.Errorf("expected multiple lines, got %d", len(lines))
	}

	// Empty text
	lines = td.wrapText("", 20)
	if lines != nil {
		t.Errorf("expected nil for empty text, got %v", lines)
	}

	// Zero width
	lines = td.wrapText("hello", 0)
	if lines != nil {
		t.Errorf("expected nil for zero width, got %v", lines)
	}
}
