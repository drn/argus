package agent

import (
	"io"
	"time"

	"github.com/drn/argus/internal/app/agentview"
	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/model"
)

// SessionProvider abstracts the management of agent sessions.
// Implemented by Runner (in-process) and daemon client (remote).
type SessionProvider interface {
	Start(task *model.Task, cfg config.Config, rows, cols uint16, resume bool) (SessionHandle, error)
	Stop(taskID string) error
	StopAll()
	Get(taskID string) SessionHandle // returns nil if not found
	Running() []string
	Idle() []string
	RunningAndIdle() (running, idle []string)
	HasSession(taskID string) bool
	WorkDir(taskID string) string

	// HasPendingRestart reports whether a kick-restart is queued for the task
	// — i.e., the session was stopped by KickRerender and the runner is about
	// to spawn a replacement at new dimensions. Callers consulting session
	// liveness during the brief gap between exit and restart should treat
	// pending tasks as alive to avoid tearing down UI state mid-rerender.
	HasPendingRestart(taskID string) bool
}

// SessionRunner is the full in-daemon session-management surface: SessionProvider
// plus the kick-rerender / reattach / needs-input extras that the daemon's API
// server and resize/resume paths drive. Both *Runner (in-process) and the
// daemon's session-supervisor client (internal/daemon/client.Client) satisfy it,
// so the daemon's `d.runner` can be either one — an in-process runner (supervisor
// OFF, byte-identical to pre-P2) or a supervisor-client (supervisor ON) — without
// any consumer in the daemon process knowing which.
//
// It is intentionally NOT used by the remote-TUI apiclient path (cmd/argus
// --remote): that stays on the narrower SessionProvider, so widening this set
// never forces the HTTP apiclient to grow methods it cannot serve.
type SessionRunner interface {
	SessionProvider

	// StartOrReattach returns the live session for task.ID if one already
	// exists (reattached=true), otherwise starts a new one (reattached=false).
	StartOrReattach(task *model.Task, cfg config.Config, rows, cols uint16, resume bool) (SessionHandle, bool, error)

	// KickRerender stops a live session and queues a same-task restart at the
	// supplied dimensions so the agent re-flows its scrollback. The runner owns
	// the pendingRestart bookkeeping that keeps the intervening exit from
	// flipping task status — callers MUST NOT emulate it with Stop+Start.
	KickRerender(task *model.Task, cfg config.Config, rows, cols uint16) error

	// NeedsInputIDs / SetNeedsInputIDs hold the daemon-computed "this session is
	// waiting on the user" set. It is derived state owned by whatever runner the
	// daemon process holds (in-process runner OFF; supervisor-client ON, where it
	// is a purely local set — needs-input is a daemon-side notion, not something
	// the supervisor tracks).
	NeedsInputIDs() []string
	SetNeedsInputIDs(ids []string)
}

// SessionHandle abstracts a single agent session.
// Implemented by Session (in-process) and RemoteSession (daemon client).
//
// IMPORTANT: most read methods on RemoteSession block on a SessionStatus
// JSON-RPC round-trip — never call them from the tview main goroutine.
// Specifically: PID, IsIdle, PTYSize, InitialPTYSize, WorkDir, TotalWritten
// (when refreshed). And every write method (WriteInput, Resize, Stop)
// hits the daemon over the Unix socket.
//
// The lock-free / local-only methods (safe on the main goroutine):
//   - Alive() — non-blocking channel select.
//   - Done() — returns the channel itself.
//   - Err() — local field.
//   - RecentOutput, RecentOutputTail — local ring buffer.
//   - AddWriter, RemoveWriter — local writer registration.
//
// Use a goroutine + QueueUpdateDraw for everything else, the same pattern
// refreshTasksAsync uses. See context/knowledge/gotchas/daemon-rpc.md.
type SessionHandle interface {
	PID() int
	WriteInput(p []byte) (int, error)
	// Resize is the internal apply-to-PTY primitive (still driven by the
	// daemon RPC and rerender-kick restart). Viewers SHALL NOT call it
	// directly; they influence size only through the registry below.
	Resize(rows, cols uint16) error
	// SetViewerSize registers (or updates) an active viewer's requested PTY
	// dimensions under a stable ID. The live PTY is sized to the per-dimension
	// min over all active viewers; an unchanged min is a no-op (no resize, no
	// SIGWINCH).
	SetViewerSize(id string, cols, rows int)
	// RemoveViewer drops a viewer's size claim and recomputes the min. With no
	// active viewers left the session keeps its last applied size.
	RemoveViewer(id string)
	RecentOutput() []byte
	RecentOutputTail(n int) []byte
	// RecentOutputTailWithTotal returns the last n bytes AND the high-water
	// mark in a single locked snapshot. Required for the /output ring-fallback
	// path so the advertised X-Output-Total cursor matches the bytes returned;
	// reading tail and total separately lets readLoop advance total past the
	// data and silently skips bytes on /stream resume.
	RecentOutputTailWithTotal(n int) (tail []byte, total uint64)
	TotalWritten() uint64
	IsIdle() bool
	// LastInput is the wall-clock time of the most recent WriteInput call,
	// or zero if no input has ever been written. Used by the idle-push watcher
	// to gate "task done" notifications: a busy→idle transition only fires a
	// push if input has arrived since the last push, so stale long-idle
	// sessions emitting incidental output do not re-notify.
	//
	// Process-boundary note: the watcher runs inside the daemon process and
	// reads this off the in-process *agent.Session. RemoteSession (the
	// daemon-client implementation in TUI processes) tracks its own local
	// timestamp so the interface contract holds, but no watcher ever reads
	// that value — it exists only to satisfy SessionHandle.
	LastInput() time.Time
	Alive() bool
	PTYSize() (cols, rows int)
	// InitialPTYSize returns the PTY dimensions the session was started with,
	// before any subsequent Resize calls. Used to detect "started narrow"
	// sessions whose conversation history won't re-flow on SIGWINCH.
	InitialPTYSize() (cols, rows int)
	Done() <-chan struct{}
	Err() error
	WorkDir() string
	Stop() error
	AddWriter(w io.Writer)
	// AddWriterFrom registers w to receive output starting at byte `offset`.
	// Bytes [offset..currentTotal] are replayed from the ring buffer in a
	// single critical section that also appends w to the writer set, so
	// readLoop cannot interleave — the writer sees the byte stream exactly
	// once from `offset` onward, no gap and no duplicate. Used by the SSE
	// /stream endpoint with an offset taken from the on-disk log size so
	// the client gets full history (disk log + bounded ring delta) without
	// overlap. w.Write MUST be non-blocking (e.g., buffered channel send
	// with select-default) — see Session.AddWriterFrom for the rationale.
	AddWriterFrom(w io.Writer, offset uint64)
	// AddWriterFromTolerant is the offset-aware analog of AddWriter — replays
	// [offset..currentTotal) without holding the session mutex, accepting a
	// small gap (rather than a duplicate) if readLoop writes concurrently
	// during the replay window. w.Write MAY block — used by the daemon's
	// stream socket where conn.Write blocks on kernel flow control.
	AddWriterFromTolerant(w io.Writer, offset uint64)
	RemoveWriter(w io.Writer)
}

// Compile-time assertions.
var _ SessionProvider = (*Runner)(nil)
var _ SessionRunner = (*Runner)(nil)
var _ SessionHandle = (*Session)(nil)
var _ agentview.TerminalAdapter = (*Session)(nil)
