package model

import (
	"crypto/rand"
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
	SessionID string    `json:"session_id,omitempty"`
	PRURL     string    `json:"pr_url,omitempty"`
	Archived  bool      `json:"archived,omitempty"`
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

// GenerateSessionID creates a new UUID v4 session ID for Claude Code.
func GenerateSessionID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 2
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
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
