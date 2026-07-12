// Package claudeagents queries and stops Claude Code's own background-agent
// sessions via the `claude agents` / `claude stop` CLI surface.
//
// Claude Code can "background" a session — via /bg, /background, an
// empty-prompt arrow-key detach, or a literal Ctrl+Z reaching its PTY — at
// which point it becomes a detached process hosted by Claude Code's own
// per-user supervisor, entirely outside the process tree argus spawns and
// tracks. No signal argus sends to the PID it originally spawned can ever
// reach a session once it has detached this way; the CLI's own job-control
// surface is the only way to find or stop one. See
// context/knowledge/gotchas/daemon-rpc.md ("Claude Code's own
// background-session supervisor") for the full investigation.
package claudeagents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// ErrUnavailable indicates the `claude` CLI is missing from PATH. Callers
// should treat this as a clean skip, not a failure worth surfacing.
var ErrUnavailable = errors.New("claude CLI unavailable")

// DefaultTimeout bounds a single List or Stop call. Both are local,
// non-LLM CLI operations against Claude Code's own session registry — no
// model call is involved — so this is far shorter than an LLM-backed
// invocation's budget.
const DefaultTimeout = 10 * time.Second

// Session is one row of `claude agents --json` output — either the caller's
// own interactive (foreground) session or one Claude Code has backgrounded.
// Fields mirror the documented schema (code.claude.com/docs/en/agent-view);
// unrecognized fields are ignored by encoding/json.
type Session struct {
	PID        int    `json:"pid,omitempty"`
	CWD        string `json:"cwd"`
	Kind       string `json:"kind"`
	StartedAt  int64  `json:"startedAt"`
	ID         string `json:"id,omitempty"`
	State      string `json:"state,omitempty"`
	Status     string `json:"status,omitempty"`
	WaitingFor string `json:"waitingFor,omitempty"`
	SessionID  string `json:"sessionId,omitempty"`
	Name       string `json:"name,omitempty"`
}

// Alive reports whether the OS process behind this session entry is
// currently running. `pid` is only present in the CLI's JSON while the
// process is alive — a background entry can still be listed, with no pid,
// after it has exited.
func (s Session) Alive() bool {
	return s.PID != 0
}

// Backgrounded reports whether this session has been detached to Claude
// Code's own per-user supervisor, as opposed to an ordinary
// foreground/interactive session argus itself is tracking.
func (s Session) Backgrounded() bool {
	return s.Kind == "background"
}

// cmdFactory is the exec seam; tests swap it to avoid shelling out to a real
// claude binary. "claude" is a fixed literal at every call site (never a
// variable), so this never passes untrusted input as the executable name.
var cmdFactory = func(ctx context.Context, args ...string) *exec.Cmd {
	return exec.CommandContext(ctx, "claude", args...)
}

// withTimeout returns ctx unchanged if it already carries a deadline,
// otherwise wraps it with DefaultTimeout.
func withTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	if _, ok := ctx.Deadline(); ok {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, DefaultTimeout)
}

// List returns every session `claude agents --json` reports, optionally
// scoped to cwd (an empty cwd lists every session across all projects). The
// result includes the caller's own interactive session alongside any
// background ones — callers looking for orphan candidates should filter on
// Backgrounded() && Alive().
func List(ctx context.Context, cwd string) ([]Session, error) {
	if _, err := exec.LookPath("claude"); err != nil {
		return nil, ErrUnavailable
	}
	ctx, cancel := withTimeout(ctx)
	defer cancel()

	args := []string{"agents", "--json"}
	if cwd != "" {
		args = append(args, "--cwd", cwd)
	}
	out, err := cmdFactory(ctx, args...).Output()
	if err != nil {
		return nil, fmt.Errorf("claude agents --json: %w", err)
	}
	var sessions []Session
	if err := json.Unmarshal(out, &sessions); err != nil {
		return nil, fmt.Errorf("parse claude agents --json output: %w", err)
	}
	return sessions, nil
}

// Stop stops the background session identified by its short id (the `id`
// field from List) — NOT a session UUID. `claude stop <uuid>` fails with
// "No job matching '<uuid>'"; only the short id is accepted.
func Stop(ctx context.Context, id string) error {
	if strings.TrimSpace(id) == "" {
		return errors.New("claudeagents: empty session id")
	}
	if _, err := exec.LookPath("claude"); err != nil {
		return ErrUnavailable
	}
	ctx, cancel := withTimeout(ctx)
	defer cancel()

	out, err := cmdFactory(ctx, "stop", id).CombinedOutput()
	if err != nil {
		return fmt.Errorf("claude stop %s: %w: %s", id, err, strings.TrimSpace(string(out)))
	}
	return nil
}
