package model

import (
	"testing"
	"time"
)

func TestTask_Elapsed_NotStarted(t *testing.T) {
	task := &Task{}
	if d := task.Elapsed(); d != 0 {
		t.Errorf("expected 0, got %v", d)
	}
}

func TestTask_Elapsed_Running(t *testing.T) {
	task := &Task{StartedAt: time.Now().Add(-5 * time.Second)}
	d := task.Elapsed()
	if d < 4*time.Second || d > 6*time.Second {
		t.Errorf("expected ~5s, got %v", d)
	}
}

func TestTask_Elapsed_Completed(t *testing.T) {
	start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(10 * time.Minute)
	task := &Task{StartedAt: start, EndedAt: end}
	if d := task.Elapsed(); d != 10*time.Minute {
		t.Errorf("expected 10m, got %v", d)
	}
}

func TestTask_ElapsedString(t *testing.T) {
	tests := []struct {
		name  string
		task  Task
		want  string
	}{
		{"not started", Task{}, ""},
		{"seconds", Task{StartedAt: time.Now().Add(-30 * time.Second)}, "30s"},
		{"minutes", Task{
			StartedAt: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
			EndedAt:   time.Date(2025, 1, 1, 0, 5, 0, 0, time.UTC),
		}, "5m"},
		{"hours", Task{
			StartedAt: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
			EndedAt:   time.Date(2025, 1, 1, 2, 0, 0, 0, time.UTC),
		}, "2h"},
		{"days", Task{
			StartedAt: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
			EndedAt:   time.Date(2025, 1, 3, 0, 0, 0, 0, time.UTC),
		}, "2d"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.task.ElapsedString(); got != tt.want {
				t.Errorf("ElapsedString() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestTask_SetStatus_InProgress(t *testing.T) {
	task := &Task{}
	task.SetStatus(StatusInProgress)

	if task.Status != StatusInProgress {
		t.Error("status not set")
	}
	if task.StartedAt.IsZero() {
		t.Error("StartedAt should be set")
	}
	if !task.EndedAt.IsZero() {
		t.Error("EndedAt should be zero")
	}
}

func TestTask_SetStatus_InProgress_PreservesStartedAt(t *testing.T) {
	original := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	task := &Task{StartedAt: original}
	task.SetStatus(StatusInProgress)

	if !task.StartedAt.Equal(original) {
		t.Error("StartedAt should not be overwritten")
	}
}

func TestTask_SetStatus_Complete(t *testing.T) {
	task := &Task{}
	task.SetStatus(StatusComplete)

	if task.Status != StatusComplete {
		t.Error("status not set")
	}
	if task.EndedAt.IsZero() {
		t.Error("EndedAt should be set")
	}
}

func TestTask_SetStatus_Pending_NoTimestamps(t *testing.T) {
	task := &Task{}
	task.SetStatus(StatusPending)

	if !task.StartedAt.IsZero() {
		t.Error("StartedAt should remain zero for pending")
	}
	if !task.EndedAt.IsZero() {
		t.Error("EndedAt should remain zero for pending")
	}
}
