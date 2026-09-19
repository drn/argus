package model

import (
	"crypto/rand"
	"fmt"
	"time"
)

// Task represents a unit of work to be completed by an LLM agent.
//
// BaseBranch and Result are orchestration fields. BaseBranch records the start
// point passed to git worktree add (empty means the project default) — used by
// stacked-PR workflows where each task branches off the previous task's branch.
// Result is an opaque JSON string the agent writes via the task_set_result MCP
// tool; the daemon never inspects it. By convention, agents that fail should
// write {"failed": true, "reason": "..."} so a Hera coordinator can decide how
// to proceed.
type Task struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Status  Status `json:"status"`
	Project string `json:"project"`
	Branch  string `json:"branch"`
	Prompt  string `json:"prompt"`
	Backend string `json:"backend,omitempty"`
	Model   string `json:"model,omitempty"`
	// Archetype is the optional diligence-profile resolution key
	// (add-diligence-profiles): an empty string means no archetype, so no
	// profile is consulted and resolution falls through to the project/backend
	// default. Set at the agent.CreateAndStart spawn layer; read by
	// agent.ResolveModel to pick the per-archetype model from the bound profile.
	Archetype string `json:"archetype,omitempty"`
	// Profile is the per-spawn diligence-profile override (add-diligence-profiles).
	// When non-empty it takes precedence over the project's bound profile during
	// model resolution. Empty means "use the project's binding".
	Profile   string `json:"profile,omitempty"`
	Worktree  string `json:"worktree,omitempty"`
	AgentPID  int    `json:"agent_pid,omitempty"`
	SessionID string `json:"session_id,omitempty"`
	// SandboxOverride is an optional per-task tri-state override of the
	// resolved sandbox setting (add-task-sandbox-override): "" (inherit the
	// project/global setting, unchanged behavior), "enabled" (force sandboxed
	// regardless of project/global config), "disabled" (force unsandboxed).
	// Set once at agent.CreateAndStart spawn time; consulted by
	// agent.ResolveSandboxConfig, which persists the resolved result on
	// Sandboxed below — the override itself is never re-derived.
	SandboxOverride string    `json:"sandbox_override,omitempty"`
	Sandboxed       bool      `json:"sandboxed,omitempty"`
	Archived        bool      `json:"archived,omitempty"`
	Pinned          bool      `json:"pinned,omitempty"`
	BaseBranch      string    `json:"base_branch,omitempty"`
	Result          string    `json:"result,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
	StartedAt       time.Time `json:"started_at,omitempty"`
	EndedAt         time.Time `json:"ended_at,omitempty"`
}

// Elapsed returns the duration since the task was started.
// Returns zero if the task hasn't started.
//
// StartedAt/EndedAt are wall-clock timestamps (time.Now), so a backward
// clock correction — NTP resync after a laptop sleep/wake, for example —
// can leave StartedAt sitting in the future or after EndedAt. That would
// otherwise surface as a negative elapsed time (e.g. "-2503s" / "-41h").
// Floor the result at zero so a clock skew never renders as negative duration.
func (t *Task) Elapsed() time.Duration {
	if t.StartedAt.IsZero() {
		return 0
	}
	var d time.Duration
	if !t.EndedAt.IsZero() {
		d = t.EndedAt.Sub(t.StartedAt)
	} else {
		d = time.Since(t.StartedAt)
	}
	if d < 0 {
		return 0
	}
	return d
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

// SetPinned and SetArchived enforce the mutual-exclusivity invariant for the
// two section flags: at most one is true at a time. All callers (TUI key
// handlers, MCP tools, HTTP API endpoints) must go through these setters —
// direct assignment leaks illegal states (e.g. a pinned-and-archived task)
// into the DB.

func (t *Task) SetPinned(v bool) {
	t.Pinned = v
	if v {
		t.Archived = false
	}
}

func (t *Task) SetArchived(v bool) {
	t.Archived = v
	if v {
		t.Pinned = false
	}
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
