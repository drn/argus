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
	Resize(rows, cols uint16) error
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
	RemoveWriter(w io.Writer)
}

// Compile-time assertions.
var _ SessionProvider = (*Runner)(nil)
var _ SessionHandle = (*Session)(nil)
var _ agentview.TerminalAdapter = (*Session)(nil)
