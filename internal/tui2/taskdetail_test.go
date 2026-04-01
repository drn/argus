package tui2

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

	td.SetTask(task, true, false)
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
		td.SetTask(task, false, true)
		td.Draw(screen)
		content := readScreen()
		testutil.Contains(t, content, "Sandbox")
		testutil.Contains(t, content, "Yes")
	})

	t.Run("not sandboxed", func(t *testing.T) {
		td.SetTask(task, false, false)
		td.Draw(screen)
		content := readScreen()
		testutil.Contains(t, content, "Sandbox")
		testutil.Contains(t, content, "No")
	})
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
