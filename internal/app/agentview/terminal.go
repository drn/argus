package agentview

// InputOrigin identifies who is responsible for a WriteInput call — a
// genuine human keystroke, or a write argus itself injected (reliable-notify
// delivery, a hera bounce instruction, a live emulator's auto-answered
// terminal capability query). idle-detection's clear-on-input logic depends
// on telling these apart: only OriginUser input can ever clear a pending
// needs-input "(?)" flag (BUG-034).
//
// There is no default value: WriteInput's origin parameter is mandatory, so
// every call site must state one explicitly. The zero value is OriginUser
// only for wire-compatibility reasons (an older peer's request that omits
// the field decodes as OriginUser, matching that peer's only prior
// behavior) — Go call sites never rely on the zero value implicitly.
type InputOrigin int

const (
	// OriginUser marks input as a genuine human keystroke. Advances both the
	// work-cycle timestamp (LastInput) and the user-input timestamp
	// (LastUserInput).
	OriginUser InputOrigin = iota
	// OriginSystem marks input as argus-injected. Advances only the
	// work-cycle timestamp — it must never be mistaken for the user
	// answering a prompt, so it never clears a pending needs-input flag.
	OriginSystem
)

// String renders the origin for logging.
func (o InputOrigin) String() string {
	if o == OriginSystem {
		return "system"
	}
	return "user"
}

// TerminalAdapter is the narrow interface that a terminal rendering pane
// needs to display a running agent session. It is a subset of
// agent.SessionHandle focused on display and input — it omits lifecycle
// methods (Stop, Done, Err) that belong to the orchestration layer.
//
// The tcell/tview renderer satisfies its terminal rendering needs
// through this interface.
type TerminalAdapter interface {
	// WriteInput sends raw bytes to the agent process stdin. origin states
	// whether this is a genuine human keystroke (OriginUser) or input argus
	// itself injected (OriginSystem) — see InputOrigin.
	WriteInput(p []byte, origin InputOrigin) (int, error)

	// Resize informs the PTY of a new terminal size.
	Resize(rows, cols uint16) error

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
