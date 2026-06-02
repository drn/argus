package daemon

import (
	"time"

	"github.com/drn/argus/internal/orch"
)

// BootInfoResp describes the daemon's boot-time identity. Used by the TUI to
// detect when the daemon binary is older than the TUI binary (e.g. after a
// rebuild) and prompt the user to restart it.
type BootInfoResp struct {
	BinaryPath  string    // resolved path of the daemon executable at boot
	BinaryMtime time.Time // mtime of the binary at boot (zero if stat failed)
	BootedAt    time.Time // wall-clock time the daemon started
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

// LinkTasksReq adds ParentID to ChildID's depends_on list. The daemon
// runs the DFS cycle check on the hypothetical edge before persisting.
type LinkTasksReq struct {
	ChildID  string
	ParentID string
}

// LinkTasksResp signals the result of a link / unlink operation. Cycle is
// non-empty when a cycle would be created; the offending path is included so
// the UI can show "A → B → A" instead of an opaque error.
type LinkTasksResp struct {
	OK    bool
	Cycle []string // task IDs in dependency order; empty when no cycle
	Error string
}

// UnlinkTasksReq removes ParentID from ChildID's depends_on list. A no-op if
// the edge does not exist. Never produces a cycle.
type UnlinkTasksReq struct {
	ChildID  string
	ParentID string
}

// DepsReq fetches the upstream and downstream tasks for a single task.
type DepsReq struct {
	TaskID string
}

// DepsResp reports a task's neighbours in the DAG. Upstream are the parents
// the task depends on (one hop). Downstream are the children that list the
// task in their depends_on (one hop). Both fields are task IDs only — not
// full Task objects — so the caller does a follow-up task_get if it needs
// names or status. The daemon walks the full task list once per call; for
// the Argus scale (≤ low thousands of rows) that's cheaper than
// maintaining a reverse index.
type DepsResp struct {
	Upstream   []string // task IDs this task depends on
	Downstream []string // task IDs that depend on this task
	Error      string
}

// DAGReq fetches a snapshot of the DAG for rendering. Project filters by
// project name (empty = all projects). PlanSlug filters by orchestrator
// stack (empty = all stacks within the project). IncludeArchived determines
// whether archived rows participate in the layout — the DAG view passes
// true so retried stacks remain visible in greyed-out form.
type DAGReq struct {
	Project         string
	PlanSlug        string
	IncludeArchived bool
}

// DAGNode is a minimal task projection for DAG rendering. Status, archived,
// and result are everything the renderer needs; full Task is overkill and
// noisy on the wire.
//
// Field order MUST match orch.DAGNode exactly — RPCService.ListDAG converts
// orch results via the Go struct cast `DAGNode(n)`, which only compiles
// when both definitions have identical fields in the same order. The
// compile-time assertion below catches drift the moment either side adds
// or removes a field.
type DAGNode struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Status    string   `json:"status"`
	Archived  bool     `json:"archived"`
	PlanSlug  string   `json:"plan_slug,omitempty"`
	Result    string   `json:"result,omitempty"`
	DependsOn []string `json:"depends_on,omitempty"`
}

// Compile-time guard: if orch.DAGNode and daemon.DAGNode drift in field
// order, type, or count, this conversion fails the build.
var _ = func(n orch.DAGNode) DAGNode { return DAGNode(n) }

// DAGResp returns the nodes for a DAG view. Edges are implicit in the
// DependsOn arrays — the client materializes them. Returning nodes (not a
// pre-computed layout) keeps the daemon agnostic to render strategy.
type DAGResp struct {
	Nodes []DAGNode
	Error string
}

// HaltDownstreamReq cascades a stop/archive through every task that
// transitively depends on TaskID. Used after a stack milestone fails so the
// orchestrator (or a human via the DAG tab) can abort the rest of the chain
// without manually walking depends_on.
type HaltDownstreamReq struct {
	TaskID string
}

// HaltDownstreamResp lists the IDs that were stopped vs archived so the UI
// can render a "halted N tasks" summary. Stopped tasks were running when
// halt fired; archived tasks were still pending. NotFound is empty in
// normal operation; populated only when an entry in depends_on points to a
// row the daemon could not locate (e.g. a deleted task that was once a
// parent), so UIs can warn the user to clean up dangling references.
type HaltDownstreamResp struct {
	Stopped  []string
	Archived []string
	NotFound []string
	Error    string
}

// SetPlanSlugReq writes the orchestrator grouping label for a task. Like
// task_set_result, the daemon does not interpret the value; the DAG view
// uses it as a filter key.
type SetPlanSlugReq struct {
	TaskID   string
	PlanSlug string
}
