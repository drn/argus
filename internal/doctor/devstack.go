package doctor

import (
	"fmt"
	"strings"
)

// DevStackOrphan is one running dev-stack process (mysqld/redis-server/
// postgres/caddy/process-compose) whose command line embeds a worktree path
// that no longer exists on disk — evidence a worktree was removed without
// its dev stack being torn down first (see fix-devstack-orphaning). Gathering
// (the process scan, the filesystem existence check per candidate) happens
// in cmd/argus; this package only classifies and renders.
type DevStackOrphan struct {
	PID          int
	Name         string
	WorktreePath string
}

// DevStackOrphanStatus classifies the outcome of scanning for dev-stack
// orphans. This check is purely informational — advisory-only — and never
// affects argus doctor's exit code, matching StopHookStatus/ProfileLibraryStatus.
type DevStackOrphanStatus int

const (
	// DevStackOrphanNone: the scan ran successfully and found no orphan.
	DevStackOrphanNone DevStackOrphanStatus = iota
	// DevStackOrphanFound: one or more dev-stack processes reference a
	// worktree path that no longer exists.
	DevStackOrphanFound
	// DevStackOrphanUnknown: the process scan itself could not run (e.g. the
	// scanning mechanism is unavailable on the current platform) — reported
	// distinctly from DevStackOrphanNone rather than assumed clean.
	DevStackOrphanUnknown
)

// DiagnoseDevStackOrphans classifies an already-gathered list of orphan
// candidates (dev-stack processes whose embedded worktree path the caller
// has already confirmed no longer exists) together with the outcome of the
// scan that produced them. scanErr degrades the result to
// DevStackOrphanUnknown regardless of candidates, since orphan status truly
// cannot be determined when the scan itself failed.
func DiagnoseDevStackOrphans(orphans []DevStackOrphan, scanErr error) (DevStackOrphanStatus, []DevStackOrphan) {
	if scanErr != nil {
		return DevStackOrphanUnknown, nil
	}
	if len(orphans) == 0 {
		return DevStackOrphanNone, nil
	}
	return DevStackOrphanFound, orphans
}

// RenderDevStackOrphans builds the human-readable dev-stack-orphan status
// line(s) printed by `argus doctor` alongside the binary-coherence table,
// the Stop-hook section, and the diligence-profile-library section. This
// check never terminates, signals, or otherwise mutates a reported process
// — argus doctor is strictly read-only.
func RenderDevStackOrphans(status DevStackOrphanStatus, orphans []DevStackOrphan) string {
	var b strings.Builder
	b.WriteString("\nOrphaned dev-stack processes: ")
	switch status {
	case DevStackOrphanFound:
		fmt.Fprintf(&b, "FOUND (%d)\n", len(orphans))
		for _, o := range orphans {
			fmt.Fprintf(&b, "  pid %d  %s  %s (worktree no longer exists)\n", o.PID, o.Name, o.WorktreePath)
		}
		b.WriteString("\nThese are not stopped automatically. Stop them manually, e.g.: kill <pid>\n")
	case DevStackOrphanNone:
		b.WriteString("NONE FOUND\n")
	default:
		b.WriteString("UNKNOWN (pgrep unavailable on this platform)\n")
	}
	return b.String()
}

// FinishedStack is one running dev-stack process whose worktree belongs to
// task rows that are all complete or archived (stop-finished-task-devstacks).
type FinishedStack struct {
	PID          int
	Name         string
	WorktreePath string
	TaskName     string
}

// FinishedStackStatus classifies the finished-task dev-stack check. Advisory
// only: it never affects argus doctor's exit code.
type FinishedStackStatus int

const (
	// FinishedStackNone: the scan and task lookup ran and found nothing.
	FinishedStackNone FinishedStackStatus = iota
	// FinishedStackFound: one or more stacks run in finished tasks' worktrees.
	FinishedStackFound
	// FinishedStackUnknown: the process scan or the task lookup failed.
	FinishedStackUnknown
)

// DiagnoseFinishedStacks classifies already-gathered candidates together with
// the outcome of the scan and task lookup that produced them. Either error
// degrades the result to Unknown: it cannot be judged, not assumed clean.
func DiagnoseFinishedStacks(stacks []FinishedStack, err error) (FinishedStackStatus, []FinishedStack) {
	if err != nil {
		return FinishedStackUnknown, nil
	}
	if len(stacks) == 0 {
		return FinishedStackNone, nil
	}
	return FinishedStackFound, stacks
}

// RenderFinishedStacks builds the doctor section for dev stacks running in
// finished tasks' worktrees. Read-only: nothing it reports is signaled.
func RenderFinishedStacks(status FinishedStackStatus, stacks []FinishedStack) string {
	var b strings.Builder
	b.WriteString("\nDev stacks of finished tasks: ")
	switch status {
	case FinishedStackFound:
		fmt.Fprintf(&b, "FOUND (%d)\n", len(stacks))
		for _, s := range stacks {
			fmt.Fprintf(&b, "  pid %d  %s  %s (task %q is complete/archived)\n", s.PID, s.Name, s.WorktreePath, s.TaskName)
		}
		b.WriteString("\nThe daemon stops these automatically unless the task was deliberately resumed or\n[devstack] stop_finished = false in config.toml.\n")
	case FinishedStackNone:
		b.WriteString("NONE FOUND\n")
	default:
		b.WriteString("UNKNOWN (process scan or task lookup unavailable)\n")
	}
	return b.String()
}
