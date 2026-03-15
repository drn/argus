package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/drn/argus/internal/model"
)

func TestTaskDetailView(t *testing.T) {
	d := NewTaskDetail(DefaultTheme())
	d.SetSize(40, 30)

	task := &model.Task{
		ID:        "t1",
		Name:      "fix-login-bug",
		Status:    model.StatusInProgress,
		Project:   "myapp",
		Branch:    "argus/fix-login-bug",
		Prompt:    "Fix the login bug in auth.go",
		CreatedAt: time.Date(2025, 1, 15, 0, 0, 0, 0, time.UTC),
		StartedAt: time.Now().Add(-5 * time.Minute),
	}

	view := d.View(task, true)

	checks := []string{
		"fix-login-bug",
		"In Progress",
		"running",
		"myapp",
		"argus/fix-login-bug",
		"PROMPT",
		"Fix the login bug",
	}
	for _, want := range checks {
		if !strings.Contains(view, want) {
			t.Errorf("view missing %q", want)
		}
	}
}

func TestTaskDetailView_Idle(t *testing.T) {
	d := NewTaskDetail(DefaultTheme())
	d.SetSize(40, 20)

	task := &model.Task{
		ID:     "t2",
		Name:   "idle-task",
		Status: model.StatusInProgress,
	}

	view := d.View(task, false)
	if !strings.Contains(view, "idle") {
		t.Error("expected 'idle' for non-running in_progress task")
	}
}

func TestTaskDetailView_CompletedNoIndicator(t *testing.T) {
	d := NewTaskDetail(DefaultTheme())
	d.SetSize(40, 20)

	task := &model.Task{
		ID:     "t3",
		Name:   "done-task",
		Status: model.StatusComplete,
	}

	view := d.View(task, false)
	if strings.Contains(view, "running") || strings.Contains(view, "idle") {
		t.Error("completed task should not show running/idle indicator")
	}
}

func TestTaskDetailViewNilTask(t *testing.T) {
	d := NewTaskDetail(DefaultTheme())
	d.SetSize(40, 20)

	view := d.View(nil, false)
	if view == "" {
		t.Error("nil task should still render a panel")
	}
	if !strings.Contains(view, "No task selected") {
		t.Error("nil task should show empty state message")
	}
}

func TestTaskDetailSetSize(t *testing.T) {
	d := NewTaskDetail(DefaultTheme())
	d.SetSize(50, 25)

	if d.width != 50 {
		t.Errorf("width = %d, want 50", d.width)
	}
	if d.height != 25 {
		t.Errorf("height = %d, want 25", d.height)
	}
}

func TestTaskDetailView_Backend(t *testing.T) {
	d := NewTaskDetail(DefaultTheme())
	d.SetSize(50, 25)

	task := &model.Task{
		ID:      "t4",
		Name:    "custom-backend",
		Status:  model.StatusPending,
		Backend: "codex",
	}

	view := d.View(task, false)
	if !strings.Contains(view, "codex") {
		t.Error("expected backend name in view")
	}
}

func TestTaskDetailView_Worktree(t *testing.T) {
	d := NewTaskDetail(DefaultTheme())
	d.SetSize(50, 25)

	task := &model.Task{
		ID:       "t5",
		Name:     "wt-task",
		Status:   model.StatusInProgress,
		Worktree: "/Users/test/.argus/worktrees/proj/wt-task",
	}

	view := d.View(task, false)
	if !strings.Contains(view, "Worktree") {
		t.Error("expected worktree label in view")
	}
}

func TestTaskDetailWrapText(t *testing.T) {
	d := NewTaskDetail(DefaultTheme())
	text := "this is a long prompt that should wrap across multiple lines when rendered"
	wrapped := d.wrapText(text, 20)
	lines := strings.Split(wrapped, "\n")
	if len(lines) < 2 {
		t.Errorf("expected multiple lines, got %d", len(lines))
	}
	for _, line := range lines {
		if len(line) > 20 {
			t.Errorf("line exceeds maxWidth: %q (len=%d)", line, len(line))
		}
	}
}

func TestSplitThreeWidths(t *testing.T) {
	tests := []struct {
		name     string
		width    int
		wantLeft func(int) bool
		wantMin  func(int, int, int) bool
	}{
		{
			name:  "wide terminal",
			width: 200,
			wantLeft: func(left int) bool {
				return left >= 20
			},
			wantMin: func(l, c, r int) bool {
				return l+c+r == 200
			},
		},
		{
			name:  "medium terminal",
			width: 120,
			wantLeft: func(left int) bool {
				return left >= 20
			},
			wantMin: func(l, c, r int) bool {
				return l+c+r == 120
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := testModel(t)
			m.taskLayout.SetSize(tt.width, 40)
			widths := m.taskLayout.SplitWidths()
			left, center, right := widths[0], widths[1], widths[2]
			if !tt.wantLeft(left) {
				t.Errorf("left = %d, failed check", left)
			}
			if center < 20 {
				t.Errorf("center = %d, want >= 20", center)
			}
			if right < 10 {
				t.Errorf("right = %d, want >= 10", right)
			}
			if !tt.wantMin(left, center, right) {
				t.Errorf("total = %d, want %d (left=%d, center=%d, right=%d)",
					left+center+right, tt.width, left, center, right)
			}
		})
	}
}
