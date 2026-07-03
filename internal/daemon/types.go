package daemon

import (
	"time"

	"github.com/drn/argus/internal/buildid"
)

// BootInfoResp describes the daemon's boot-time identity. Used by the TUI to
// detect when the daemon binary is older than the TUI binary (e.g. after a
// rebuild) and prompt the user to restart it.
//
// It also relays the connected session-supervisor's identity (D1): the TUI
// talks only to the daemon, so the daemon — which already holds the supervisor
// client — re-queries the supervisor's Hello when it serves BootInfo (see
// RPCService.BootInfo) and folds the reply into the Supervisor* fields. The
// re-query (not a New()-time cache) means an independently-restarted supervisor
// reports its CURRENT identity.
type BootInfoResp struct {
	BinaryPath  string      // resolved path of the daemon executable at boot
	BinaryMtime time.Time   // mtime of the binary at boot (zero if stat failed)
	BinaryHash  string      // SHA-256 of the binary at boot (empty if hashing failed)
	VCS         buildid.VCS // daemon's own commit SHA + dirty flag (blank outside a git tree); display-only
	BootedAt    time.Time   // wall-clock time the daemon started

	// Supervisor identity, relayed from the connected supervisor's Hello at
	// serve time. SupervisorPresent is false when the daemon runs the in-process
	// runner (no supervisor). When present but the supervisor speaks an older
	// protocol that omits the hash, SupervisorHash is empty — reported as
	// "unknown", NEVER as stale (the additive-protocol feature-detect).
	SupervisorPresent bool        // a supervisor client is connected
	SupervisorPath    string      // resolved path of the supervisor executable
	SupervisorHash    string      // SHA-256 of the supervisor binary (empty ⇒ unknown / pre-hash protocol)
	SupervisorVCS     buildid.VCS // supervisor's commit SHA + dirty flag; display-only
}

// ProtocolVersion is the version of the session-server R/S protocol that the
// session-supervisor speaks (see context/plans/session-supervisor.md §4.4).
// It is reported in the Hello handshake so a future daemon (P2) can connect to
// an already-running, possibly-older supervisor and feature-detect its
// capabilities before relying on any newer RPC or field.
//
// The protocol is ADDITIVE-ONLY: bump this constant whenever a new RPC method
// or a new (optional) request/response field is introduced. Never remove or
// repurpose an existing method/field — the daemon must always be able to talk
// to a supervisor binary it did not itself start (go install + daemon restart
// does NOT restart the supervisor; agents would die). Treat it as a frozen
// public contract and review changes like an API break.
//
// Version history:
//   - v1 (P1): Ping, StartSession, StopSession, StopAll, SessionStatus,
//     ListSessions, HasPendingRestart, WriteInput, Resize, GetExitInfo, Hello, Shutdown.
//   - v2 (P2): + KickRerender (the daemon's API-server resize path drives a
//     kick-rerender restart through the supervisor; the runner's pendingRestart
//     bookkeeping must run supervisor-side, so it can't be composed client-side
//     from Stop+Start). Additive: a v1 supervisor simply lacks the method and a
//     KickRerender RPC against it errors, which the daemon treats as a no-op kick.
//   - v3 (binary-coherence): + HelloResp.BinaryHash and HelloResp.VCS (the
//     supervisor hashes its own resolved binary at boot and reports its VCS
//     identity, so the daemon can relay supervisor skew to the TUI via BootInfo).
//     Additive: a v2 supervisor omits both fields, and the daemon feature-detects
//     the empty BinaryHash as "supervisor identity unknown" — NEVER a false stale,
//     and never a trigger to auto-restart the live supervisor.
const ProtocolVersion = 3

// SupervisorProtocolMatch reports whether a supervisor's handshake version
// equals the daemon's. A mismatch is NOT fatal and NEVER triggers an auto-
// restart of the live supervisor: restarting it would SIGHUP its agents (the
// one event P2 exists to avoid — design §4.4). The daemon logs the skew and
// proceeds within the running supervisor's capabilities. This is a pure helper
// so the connect path's skew decision is unit-testable (the connect glue itself
// forks/dials and is coverage-exempt).
func SupervisorProtocolMatch(hello HelloResp) bool {
	return hello.ProtocolVersion == ProtocolVersion
}

// HelloResp is the session-supervisor's handshake reply. ProtocolVersion lets
// the daemon decide which RPCs/fields it may use against this supervisor.
//
// It carries the supervisor's binary identity so the daemon can reason about
// (and relay) supervisor staleness. A stale supervisor is NEVER auto-restarted
// — that would interrupt agents; see the design doc §4.4 — the daemon only
// surfaces it to the TUI, which prompts for a guarded, user-initiated restart.
//
// BinaryHash + VCS were added in ProtocolVersion 3 (binary-coherence). The
// staleness DECISION is the SHA-256 content-hash comparison; VCS (commit SHA +
// dirty flag) is display-only and blank for binaries built outside a git tree.
// A v2 supervisor omits both — the daemon feature-detects the empty BinaryHash
// as "unknown", never stale.
type HelloResp struct {
	ProtocolVersion int
	BinaryPath      string      // resolved path of the supervisor executable at boot
	BinaryMtime     time.Time   // mtime of the binary at boot (zero if stat failed)
	BinaryHash      string      // SHA-256 of the binary at boot (empty ⇒ hashing failed OR pre-v3 supervisor)
	VCS             buildid.VCS // supervisor's commit SHA + dirty flag; display-only, blank outside a git tree
	BootedAt        time.Time   // wall-clock time the supervisor started
}

// PortsResp returns the live HTTP ports the daemon is bound to. Both servers
// pick their port via bindWithRetry on startup, so neither value is stable
// across daemon restarts. Plugins that need to call the REST API or MCP
// server use Daemon.Ports to discover the current ports instead of hardcoding
// or scanning. A zero value means that server is not running (e.g. KB
// disabled → MCPPort=0; API disabled → APIPort=0).
type PortsResp struct {
	MCPPort int
	APIPort int
}

// StartReq is the RPC request to start a new agent session.
type StartReq struct {
	TaskID    string
	SessionID string
	Prompt    string
	Project   string
	Backend   string
	Model     string
	Worktree  string
	Branch    string
	Rows      uint16
	Cols      uint16
	Resume    bool
}

// StartResp is the RPC response from starting a session.
type StartResp struct {
	PID   int
	Error string
}

// TaskIDReq is an RPC request that identifies a single task.
type TaskIDReq struct {
	TaskID string
}

// StatusResp is a generic success/error RPC response.
type StatusResp struct {
	OK    bool
	Error string
}

// SessionInfo describes the state of a running session.
type SessionInfo struct {
	TaskID       string
	Alive        bool
	Idle         bool
	PID          int
	Cols         int
	Rows         int
	InitialCols  int // PTY width at session start; immutable
	InitialRows  int // PTY height at session start; immutable
	WorkDir      string
	TotalWritten uint64
}

// WriteReq is the RPC request to send input to a session's PTY.
type WriteReq struct {
	TaskID string
	Data   []byte
}

// ResizeReq is the RPC request to resize a session's PTY.
type ResizeReq struct {
	TaskID string
	Rows   uint16
	Cols   uint16
}

// KickReq is the RPC request to kick-rerender a session (P2, protocol v2). It
// carries the full task projection (like StartReq, minus Resume — a kick always
// resumes) because the supervisor's runner stores the task to rebuild the
// command for the in-place restart. The supervisor resolves cfg via its own
// cfgFn, so the daemon does not ship config on the wire.
type KickReq struct {
	TaskID    string
	SessionID string
	Prompt    string
	Project   string
	Backend   string
	Model     string
	Worktree  string
	Branch    string
	Rows      uint16
	Cols      uint16
}

// StreamHeader is sent by the client on a stream connection to subscribe
// to a session's output. Since is the monotonic byte offset the client has
// already received; the daemon replays only [Since, currentTotal) from the
// session ring buffer before attaching live. Zero replays the full ring
// (legacy AddWriter behaviour) and matches the first attach. Set on every
// reconnect to TotalWritten() at attach time so retries don't duplicate
// bytes already in the client's local ring.
type StreamHeader struct {
	TaskID string `json:"task_id"`
	Since  uint64 `json:"since,omitempty"`
}

// ListResp is the RPC response for listing all sessions.
type ListResp struct {
	Sessions []SessionInfo
}

// PongResp is the RPC response for a Ping request.
type PongResp struct {
	OK bool
}

// PendingRestartResp reports whether the runner has a kick-restart queued
// for a task. Set during the brief gap between a stopped session's exit and
// the runner's resume Start completing.
type PendingRestartResp struct {
	Pending bool
}

// Empty is a placeholder for RPC methods that take no arguments.
type Empty struct{}

// KBSearchReq is the RPC request to search the knowledge base.
type KBSearchReq struct {
	Query string
	Limit int
}

// KBSearchResp is the RPC response from a KB search.
type KBSearchResp struct {
	Results []KBSearchResult
	Error   string
}

// KBSearchResult is a KB search result returned over RPC.
// (Mirrors kb.SearchResult but avoids importing the kb package in types.go.)
type KBSearchResult struct {
	Path    string
	Title   string
	Tier    string
	Snippet string
	Rank    float64
}

// KBIngestReq is the RPC request to ingest a document into the knowledge base.
type KBIngestReq struct {
	Path    string
	Content string
}

// KBIngestResp is the RPC response from a KB ingest.
type KBIngestResp struct {
	Error string
}

// KBListReq is the RPC request to list documents in the knowledge base.
type KBListReq struct {
	Prefix string
	Limit  int
}

// KBListResp is the RPC response from a KB list.
type KBListResp struct {
	Documents []KBDocumentInfo
	Error     string
}

// KBDocumentInfo summarises a KB document (no body).
type KBDocumentInfo struct {
	Path      string
	Title     string
	Tier      string
	WordCount int
}

// KBStatusResp is the RPC response for a KB status query.
type KBStatusResp struct {
	DocumentCount int
	VaultPath     string
	Port          int
}

// UpdateSelfResp is the RPC response from running `go install ./...` against
// the configured Argus source path. Output is the combined stdout+stderr of
// the run regardless of success — clients display it to the user.
type UpdateSelfResp struct {
	Output string
	Error  string
}

// ClipboardSetReq stages text for a task in the agent-staged clipboard.
type ClipboardSetReq struct {
	TaskID string
	Text   string
}

// ClipboardGetReq fetches any staged text for a task.
type ClipboardGetReq struct {
	TaskID string
}

// ClipboardGetResp returns the staged text and a presence flag.
type ClipboardGetResp struct {
	Text  string
	OK    bool
	Error string
}

// ClipboardClearReq clears any staged text for a task.
type ClipboardClearReq struct {
	TaskID string
}

// SetFocusedReq notifies the daemon that the TUI has entered or left agent
// view for a task. The daemon forwards this to the FocusTracker so the
// reliable pane-delivery reconciler knows whether a human is present.
type SetFocusedReq struct {
	TaskID  string
	Focused bool
}
