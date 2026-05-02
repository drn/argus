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
	RemoveWriter(w io.Writer)
}

// Compile-time assertions.
var _ SessionProvider = (*Runner)(nil)
var _ SessionHandle = (*Session)(nil)
var _ agentview.TerminalAdapter = (*Session)(nil)
