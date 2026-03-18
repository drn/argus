package agent

import (
	"io"

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
	HasSession(taskID string) bool
	WorkDir(taskID string) string
}

// SessionHandle abstracts a single agent session.
// Implemented by Session (in-process) and RemoteSession (daemon client).
type SessionHandle interface {
	PID() int
	WriteInput(p []byte) (int, error)
	Resize(rows, cols uint16) error
	RecentOutput() []byte
	RecentOutputTail(n int) []byte
	TotalWritten() uint64
	IsIdle() bool
	Alive() bool
	PTYSize() (cols, rows int)
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
