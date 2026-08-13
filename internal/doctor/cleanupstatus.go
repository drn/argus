package doctor

import (
	"fmt"
	"strings"
)

// CleanupPeriodStatus classifies Claude Code's effective session-retention
// window (cleanupPeriodDays in ~/.claude/settings.json). This check is
// independent of the binary-coherence Verdict and the other advisory
// sections above and never affects argus doctor's exit code — it exists
// purely to make a silent resume-failure mode visible (add-claude-retention-
// diagnostics): Claude Code deletes session transcripts older than this
// window at every claude startup, so an Argus task left untouched past it
// fails to resume with Claude Code's own "session not found" error.
type CleanupPeriodStatus int

const (
	// CleanupPeriodLow: cleanupPeriodDays is unset (Claude Code's own 30-day
	// default applies) or explicitly set to 30 or below.
	CleanupPeriodLow CleanupPeriodStatus = iota
	// CleanupPeriodOK: cleanupPeriodDays is explicitly raised above 30.
	CleanupPeriodOK
	// CleanupPeriodUnknown: ~/.claude/settings.json could not be read or
	// parsed, so the effective value cannot be determined — reported
	// distinctly from CleanupPeriodLow rather than assumed low.
	CleanupPeriodUnknown
)

// cleanupPeriodSnippet is the exact JSON to add to ~/.claude/settings.json to
// raise the retention window, mirroring the README's "Claude Code session
// retention" section.
const cleanupPeriodSnippet = `{
  "cleanupPeriodDays": 3650
}`

// DiagnoseCleanupPeriod classifies the cleanup-period tri-state from the
// already-read effective value (nil meaning unset) and any error from
// reading/parsing ~/.claude/settings.json. A non-nil readErr degrades to
// CleanupPeriodUnknown regardless of days, since the effective value truly
// cannot be determined.
func DiagnoseCleanupPeriod(days *int, readErr error) CleanupPeriodStatus {
	if readErr != nil {
		return CleanupPeriodUnknown
	}
	if days == nil || *days <= 30 {
		return CleanupPeriodLow
	}
	return CleanupPeriodOK
}

// RenderCleanupPeriod builds the human-readable Claude-session-retention
// status line(s) printed by `argus doctor` alongside its other advisory
// sections.
func RenderCleanupPeriod(status CleanupPeriodStatus, days *int) string {
	var b strings.Builder
	b.WriteString("\nClaude session retention (cleanupPeriodDays): ")
	switch status {
	case CleanupPeriodOK:
		fmt.Fprintf(&b, "OK (%d days)\n", *days)
	case CleanupPeriodLow:
		current := "unset (defaults to 30)"
		if days != nil {
			current = fmt.Sprintf("%d days", *days)
		}
		fmt.Fprintf(&b, "LOW (%s)\n\nClaude Code deletes session transcripts older than this window at every\nstartup, so an Argus task left untouched past it will fail to resume. Add to\n~/.claude/settings.json (raises retention for ALL Claude Code usage, not just\nArgus — there is no way to scope this to Argus-created sessions only):\n%s\n", current, cleanupPeriodSnippet)
	default:
		b.WriteString("UNKNOWN (could not read/parse ~/.claude/settings.json)\n")
	}
	return b.String()
}
