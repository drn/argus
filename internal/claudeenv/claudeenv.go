// Package claudeenv names the Claude Code environment variables Argus must
// control at every process spawn. It is a leaf package (no argus imports) so
// both internal/agent (agent spawns) and internal/usagebudget (the headless
// /usage probe) share one list.
package claudeenv

import "slices"

// AuthOverrides take precedence over an account's stored login.
var AuthOverrides = []string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "CLAUDE_CODE_OAUTH_TOKEN"}

func IsAuthOverride(name string) bool { return slices.Contains(AuthOverrides, name) }

// ParentSessionMarkers are the per-session runtime markers Claude Code
// exports into every child process. They describe the PARENT session — its
// identity, PID, entrypoint, messaging channel (including a token that must
// never reach another agent) and effort level. When the daemon or supervisor
// is (re)started from inside a Claude Code session they leak through
// os.Environ() into every spawn, and Claude Code then treats each agent as a
// child session and disables transcript saving, which Argus needs for resume
// and session discovery.
//
// This is an explicit denylist, not a CLAUDE_* prefix strip: user-configured
// settings variables (CLAUDE_CODE_ENABLE_TELEMETRY, ...) are deliberate
// preferences and must survive. Extend it when Claude Code adds a marker.
var ParentSessionMarkers = []string{
	"CLAUDECODE",
	"CLAUDE_CODE_CHILD_SESSION",
	"CLAUDE_CODE_ENTRYPOINT",
	"CLAUDE_CODE_EXECPATH",
	"CLAUDE_CODE_SESSION_ID",
	"CLAUDE_CODE_SESSION_ATTENDED",
	"CLAUDE_CODE_MESSAGING_SOCKET",
	"CLAUDE_CODE_MESSAGING_TOKEN",
	"CLAUDE_PID",
	"CLAUDE_EFFORT",
}

// ForcePersistenceVar makes Claude Code keep saving the session transcript
// even if a child-session marker reaches it by a path the denylist misses.
const ForcePersistenceVar = "CLAUDE_CODE_FORCE_SESSION_PERSISTENCE"

// ForcePersistenceEnv is the KEY=VALUE entry Argus exports to every
// Claude-family process it spawns.
const ForcePersistenceEnv = ForcePersistenceVar + "=1"

// IsParentSessionMarker reports whether name is one of ParentSessionMarkers.
func IsParentSessionMarker(name string) bool {
	return slices.Contains(ParentSessionMarkers, name)
}
