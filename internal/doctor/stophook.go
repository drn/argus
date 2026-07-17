package doctor

import (
	"fmt"
	"strings"
)

// StopHookStatus classifies whether ~/.claude/settings.json registers
// `argus coord-hook` as a global Claude Code Stop hook. This check is
// independent of the binary-coherence Verdict above and never affects
// argus doctor's exit code — it exists purely to make a silent-failure
// install step (see detect-missing-coord-hook) visible.
type StopHookStatus int

const (
	// StopHookRegistered: at least one Stop-hook command references
	// `argus coord-hook`.
	StopHookRegistered StopHookStatus = iota
	// StopHookNotRegistered: settings.json was read and parsed successfully,
	// but no Stop-hook command references `argus coord-hook`.
	StopHookNotRegistered
	// StopHookUnknown: settings.json could not be read or parsed, so
	// registration cannot be determined — reported distinctly from
	// StopHookNotRegistered rather than assumed absent.
	StopHookUnknown
)

// DiagnoseStopHook classifies Stop-hook registration from the Stop-hook
// commands already read out of ~/.claude/settings.json. readErr is the
// outcome of loading/parsing that file; when non-nil the result degrades to
// StopHookUnknown regardless of commands, since registration truly cannot be
// determined (missing file, bad permissions, malformed JSON).
func DiagnoseStopHook(commands []string, readErr error) StopHookStatus {
	if readErr != nil {
		return StopHookUnknown
	}
	for _, c := range commands {
		if strings.Contains(c, "coord-hook") {
			return StopHookRegistered
		}
	}
	return StopHookNotRegistered
}

// stopHookSnippet is the exact JSON to add to ~/.claude/settings.json,
// mirroring the README's "Context-budget Stop hook" section.
const stopHookSnippet = `{
  "hooks": {
    "Stop": [
      { "hooks": [ { "type": "command", "command": "argus coord-hook" } ] }
    ]
  }
}`

// RenderStopHook builds the human-readable Stop-hook status line(s) printed
// by `argus doctor` alongside the binary-coherence table.
func RenderStopHook(status StopHookStatus) string {
	var b strings.Builder
	b.WriteString("\nStop hook (argus coord-hook): ")
	switch status {
	case StopHookRegistered:
		b.WriteString("REGISTERED\n")
	case StopHookNotRegistered:
		fmt.Fprintf(&b, "NOT REGISTERED\n\nAdd to ~/.claude/settings.json:\n%s\n", stopHookSnippet)
	default:
		b.WriteString("UNKNOWN (could not read/parse ~/.claude/settings.json)\n")
	}
	return b.String()
}
