package ui

import (
	"strings"
	"testing"

	"github.com/drn/argus/internal/model"
)

func TestStatusBar_TaskCounts(t *testing.T) {
	sb := NewStatusBar(DefaultTheme())
	sb.SetWidth(120)
	sb.SetTasks([]*model.Task{
		{ID: "a", Status: model.StatusInProgress},
		{ID: "b", Status: model.StatusInProgress},
		{Status: model.StatusPending},
		{Status: model.StatusComplete},
		{Status: model.StatusComplete},
		{Status: model.StatusComplete},
	})
	sb.SetRunning([]string{"a", "b"})

	v := sb.View()
	if !strings.Contains(v, "2 active") {
		t.Error("expected 2 active")
	}
	if !strings.Contains(v, "1 pending") {
		t.Error("expected 1 pending")
	}
	if !strings.Contains(v, "3 done") {
		t.Error("expected 3 done")
	}
}

func TestStatusBar_Error(t *testing.T) {
	sb := NewStatusBar(DefaultTheme())
	sb.SetWidth(120)
	sb.SetError("something broke")

	v := sb.View()
	if !strings.Contains(v, "something broke") {
		t.Error("expected error message in view")
	}
}

func TestStatusBar_ClearError(t *testing.T) {
	sb := NewStatusBar(DefaultTheme())
	sb.SetWidth(120)
	sb.SetTasks([]*model.Task{{Status: model.StatusPending}})
	sb.SetError("err")
	sb.ClearError()

	v := sb.View()
	if strings.Contains(v, "err") {
		t.Error("error should be cleared")
	}
	if !strings.Contains(v, "1 pending") {
		t.Error("should show task counts after clearing error")
	}
}

func TestStatusBar_Empty(t *testing.T) {
	sb := NewStatusBar(DefaultTheme())
	sb.SetWidth(80)
	sb.SetTasks(nil)

	v := sb.View()
	if !strings.Contains(v, "0 active") {
		t.Error("expected 0 counts")
	}
}

func TestStatusBar_SettingsTabKeys(t *testing.T) {
	sb := NewStatusBar(DefaultTheme())
	sb.SetWidth(120)
	sb.SetTasks(nil)
	sb.SetSettingsTab(true)

	v := sb.View()
	// Settings tab should show "tasks" key (to switch back) and "new project"
	if !strings.Contains(v, "tasks") {
		t.Error("settings tab should show 'tasks' key hint")
	}
	if !strings.Contains(v, "new project") {
		t.Error("settings tab should show 'new project' key hint")
	}
	// Should NOT show "attach" (tasks-only key)
	if strings.Contains(v, "attach") {
		t.Error("settings tab should not show 'attach' key hint")
	}
}

func TestStatusBar_TaskTabKeys(t *testing.T) {
	sb := NewStatusBar(DefaultTheme())
	sb.SetWidth(120)
	sb.SetTasks(nil)
	sb.SetSettingsTab(false)

	v := sb.View()
	// Task tab should show "attach" and "settings"
	if !strings.Contains(v, "attach") {
		t.Error("task tab should show 'attach' key hint")
	}
	if !strings.Contains(v, "settings") {
		t.Error("task tab should show 'settings' key hint")
	}
}
