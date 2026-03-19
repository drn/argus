package agentview

// TerminalAdapter is the narrow interface that a terminal rendering pane
// needs to display a running agent session. It is a subset of
// agent.SessionHandle focused on display and input — it omits lifecycle
// methods (Stop, Done, Err) that belong to the orchestration layer.
//
// The tcell/tview renderer satisfies its terminal rendering needs
// through this interface.
type TerminalAdapter interface {
	// WriteInput sends raw bytes to the agent process stdin.
	WriteInput(p []byte) (int, error)

	// Resize informs the PTY of a new terminal size.
	Resize(rows, cols uint16) error

	// RecentOutput returns the full contents of the ring buffer.
	RecentOutput() []byte

	// RecentOutputTail returns the last n bytes from the ring buffer.
	RecentOutputTail(n int) []byte

	// TotalWritten returns the monotonic byte count written to the ring buffer.
	// Used to detect new output without copying the buffer.
	TotalWritten() uint64

	// Alive reports whether the agent process is still running.
	Alive() bool

	// PTYSize returns the current PTY dimensions (cols, rows).
	PTYSize() (cols, rows int)
}

// SessionLookup abstracts the ability to find a running session by task ID.
// This allows the terminal pane to resolve sessions without depending on
// the full SessionProvider interface.
type SessionLookup interface {
	// Get returns the TerminalAdapter for the given task, or nil if no
	// session is active.
	Get(taskID string) TerminalAdapter
}
