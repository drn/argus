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
