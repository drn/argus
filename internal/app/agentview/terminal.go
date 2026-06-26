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

	// Resize informs the PTY of a new terminal size. It is the internal
	// apply-to-PTY primitive; display panes SHALL NOT call it directly — they
	// influence size through the viewer registry below.
	Resize(rows, cols uint16) error

	// SetViewerSize registers (or updates) this viewer's requested PTY size
	// under a stable ID. The session sizes its PTY to the per-dimension min over
	// all active viewers, so a smaller concurrent viewer constrains the size
	// rather than the pane forcing an absolute resize. An unchanged min is a
	// no-op (no resize, no SIGWINCH).
	SetViewerSize(id string, cols, rows int)

	// RemoveViewer drops this viewer's size claim and recomputes the min. With
	// no active viewers left the session keeps its last applied size.
	RemoveViewer(id string)

	// RecentOutput returns the full contents of the ring buffer.
	RecentOutput() []byte

	// RecentOutputTail returns the last n bytes from the ring buffer.
	RecentOutputTail(n int) []byte

	// RecentOutputTailWithTotal returns the last n bytes AND the high-water
	// mark TotalWritten() in a single locked snapshot. Used by the live
	// emulator rebuild path to merge in-memory ring tail bytes with on-disk
	// session log content without race-induced duplication or gaps —
	// reading tail and total separately lets readLoop advance total past
	// the bytes in tail, leaving an inconsistent pair.
	RecentOutputTailWithTotal(n int) (tail []byte, total uint64)

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
