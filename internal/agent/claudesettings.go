package agent

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
)

// retentionFailureSignature is the exact stderr text Claude Code prints (exit
// 1) when asked to resume a session whose transcript no longer exists —
// whether it never existed or was swept by its own cleanupPeriodDays
// retention window (default 30 days). Confirmed empirically: `claude --resume
// <uuid>` for an unknown ID prints this line verbatim, so a literal substring
// match is the whole detection mechanism; no extra plumbing tracking "was
// this a resume attempt" is needed since the message only occurs on resume.
const retentionFailureSignature = "No conversation found with session ID:"

// IsRetentionSweptResumeFailure reports whether a session's last output
// matches Claude Code's resume-target-not-found signature, distinguishing a
// swept transcript from a generic crash.
func IsRetentionSweptResumeFailure(lastOutput []byte) bool {
	return bytes.Contains(lastOutput, []byte(retentionFailureSignature))
}

// claudeSettingsCleanupPeriod is the shape of the one field this file cares
// about in ~/.claude/settings.json; every other key is ignored.
type claudeSettingsCleanupPeriod struct {
	CleanupPeriodDays *int `json:"cleanupPeriodDays"`
}

// ReadClaudeCleanupPeriodDaysAt reads path (a Claude Code settings.json) and
// returns the effective cleanupPeriodDays value. A nil result with no error
// means the key is absent, so Claude Code's own 30-day default applies. Takes
// an explicit path (rather than resolving $HOME itself) so it's testable
// against a temp file without touching the real ~/.claude/settings.json —
// mirrors cmd/argus/doctor.go's readStopHookCommands pattern, and is exported
// so cmd/argus/doctor.go can build its own injectable-path test wrapper
// around it, the same way it already does for QueryClaudeCleanupPeriodDays.
func ReadClaudeCleanupPeriodDaysAt(path string) (*int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var parsed claudeSettingsCleanupPeriod
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, err
	}
	return parsed.CleanupPeriodDays, nil
}

// QueryClaudeCleanupPeriodDays resolves ~/.claude/settings.json and returns
// its effective cleanupPeriodDays, so argus doctor and the Settings TUI
// classify the same underlying state via one implementation. Empty home
// resolution degrades to an error, matching ReadClaudeCleanupPeriodDaysAt's
// missing-file case (never a false "unset" reading).
func QueryClaudeCleanupPeriodDays() (*int, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	return ReadClaudeCleanupPeriodDaysAt(filepath.Join(home, ".claude", "settings.json"))
}
