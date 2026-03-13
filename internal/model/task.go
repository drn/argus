package model

import (
	"fmt"
	"time"
)

// Task represents a unit of work to be completed by an LLM agent.
type Task struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Status    Status    `json:"status"`
	Project   string    `json:"project"`
	Branch    string    `json:"branch"`
	Prompt    string    `json:"prompt"`
	Backend   string    `json:"backend,omitempty"`
	Worktree  string    `json:"worktree,omitempty"`
	AgentPID  int       `json:"agent_pid,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	StartedAt time.Time `json:"started_at,omitempty"`
	EndedAt   time.Time `json:"ended_at,omitempty"`
}

// Elapsed returns the duration since the task was started.
// Returns zero if the task hasn't started.
func (t *Task) Elapsed() time.Duration {
	if t.StartedAt.IsZero() {
		return 0
	}
	if !t.EndedAt.IsZero() {
		return t.EndedAt.Sub(t.StartedAt)
	}
	return time.Since(t.StartedAt)
}

// ElapsedString returns a human-readable elapsed time.
func (t *Task) ElapsedString() string {
	d := t.Elapsed()
	if d == 0 {
		return ""
	}
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	hours := int(d.Hours())
	if hours < 24 {
		return fmt.Sprintf("%dh", hours)
	}
	return fmt.Sprintf("%dd", hours/24)
}

// SetStatus updates the task status and manages timestamps.
func (t *Task) SetStatus(s Status) {
	t.Status = s
	now := time.Now()
	switch s {
	case StatusInProgress:
		if t.StartedAt.IsZero() {
			t.StartedAt = now
		}
	case StatusComplete:
		t.EndedAt = now
	}
}
