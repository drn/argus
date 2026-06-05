package terminal

import (
	xvt "github.com/charmbracelet/x/vt"
)

// PreviewVT maintains a persistent x/vt emulator for the task-list Preview
// pane, advanced incrementally.
//
// The previous preview design rebuilt a throwaway emulator on every refresh
// and replayed only a short output tail. Progress frames that the agent
// overwrites in place (e.g. a Claude Code Task-tool counter shrinking
// "20" → "9") emit their erasing escape sequences earlier in the stream than
// the bytes that follow; when only the tail is replayed those erases fall
// outside the window and the superseded digits survive as ghost cells
// floating to the right of collapsed lines. A persistent emulator processes
// every byte in stream order, so an erase is always applied before the bytes
// that follow it and the ghost cells cannot form — exactly why the live agent
// pane (which feeds a persistent emulator incrementally) never exhibits the
// artifact. It also removes the per-refresh re-parse cost: steady-state
// refreshes feed only the new suffix instead of re-emulating the whole tail.
//
// PreviewVT performs no internal locking. The owner must serialize calls to
// Feed and Reset — the preview panel does this with its own mutex because
// RefreshOutput runs on both the tick goroutine and the cursor-change
// goroutine.
type PreviewVT struct {
	emu      *xvt.SafeEmulator
	taskID   string
	cols     int
	rows     int
	fedTotal uint64
	oscStrip oscFilter
}

// Feed advances the emulator to reflect the output stream [0, totalWritten)
// for taskID, given `tail` — the most recent bytes of that stream (typically
// the 256KB ring tail from RecentOutputTailWithTotal for a live session, or a
// log tail for a finished session). It returns the emulator to render from,
// or (nil, nil) when there is nothing to show yet.
//
// The emulator is rebuilt and the aligned tail fully replayed when the task
// or emulator dimensions change, or when the stream advanced by more bytes
// than `tail` holds (a ring wrap past the last fed offset). Otherwise only the
// unseen suffix of `tail` is fed, contiguous with what the emulator already
// parsed. A recovered emulator-write panic returns (nil, err) and drops the
// emulator so the next call rebuilds from scratch.
//
// `tail` ends at byte `totalWritten`; for a finished session pass the log's
// file size as totalWritten (the tail need only cover the visible viewport —
// the preview has no scrollback, so a bounded tail reconstructs it cleanly).
func (p *PreviewVT) Feed(taskID string, cols, rows int, tail []byte, totalWritten uint64) (*xvt.SafeEmulator, error) {
	needRebuild := p.emu == nil || p.taskID != taskID || p.cols != cols || p.rows != rows
	if needRebuild {
		p.rebuild(taskID, cols, rows)
	}

	if totalWritten == 0 || len(tail) == 0 {
		if p.emu != nil && p.fedTotal > 0 {
			return p.emu, nil // nothing new — keep showing prior content
		}
		return nil, nil
	}

	newBytes := totalWritten - p.fedTotal
	// fullReplay covers: a fresh emulator (needRebuild), and a ring wrap where
	// the new high-water mark outran our tail so the unseen suffix is no longer
	// contiguous with the emulator's parser state (newBytes > len(tail)). The
	// uint64 subtraction also makes fedTotal > totalWritten (a stream reset)
	// underflow to a huge value, which trips this same guard — the safe
	// outcome.
	fullReplay := needRebuild || newBytes > uint64(len(tail))

	var feed []byte
	switch {
	case fullReplay:
		// A ring wrap can require a full replay without a dimension/task
		// change; drop the desynced emulator first.
		if !needRebuild {
			p.rebuild(taskID, cols, rows)
		}
		// AlignToEscBoundary skips any partial CSI/OSC prefix the tail may
		// begin mid-sequence at (x/vt would render those orphan bytes as a
		// smudge of digits at the top of the emulator). oscStrip was reset
		// in rebuild, so it continues from a known state into the incremental
		// feeds that follow.
		feed = p.oscStrip.filter(AlignToEscBoundary(tail))
	case newBytes > 0:
		// Incremental: the delta is contiguous with what the emulator already
		// parsed, so no ESC realignment is needed. oscStrip carries state
		// across feeds so an OSC split between two deltas is still stripped.
		// newBytes <= len(tail) here — the fullReplay guard above caught the
		// greater-than case — so the conversion is in range.
		start := len(tail) - int(newBytes) //nolint:gosec // bounded by fullReplay guard
		feed = p.oscStrip.filter(tail[start:])
	default:
		return p.emu, nil // nothing new
	}

	if _, err := SafeEmuWrite(p.emu, feed); err != nil {
		// The recovered panic may have left the emulator's internal state
		// inconsistent, so drop it — the next Feed creates a fresh one. We do
		// NOT Close it: SafeEmulator exposes no mutex-guarded Close, and
		// calling Emulator.Close races the drain goroutine's Read on e.closed
		// (the -race detector flags it). The orphaned drain goroutine is
		// bounded to one per emulator panic — rare (upstream x/vt out-of-bounds
		// bugs) — matching the live pane's leak posture. Steady-state rebuilds
		// reuse the emulator (see rebuild) so they never leak.
		p.emu = nil
		p.fedTotal = 0
		return nil, err
	}
	p.fedTotal = totalWritten
	return p.emu, nil
}

// rebuild resets the emulator to a pristine state and clears all
// stream-tracking state; the caller re-feeds from a clean escape boundary
// afterward. It REUSES the existing emulator (resetting it via RIS) rather
// than allocating a new one, because NewDrainedEmulator spawns a drain
// goroutine that can only be stopped by Close — and Close is racy here (see
// the Feed error path). rebuild fires on every task-list cursor move, so
// allocating per rebuild would leak one goroutine per scroll; reuse keeps it
// at one goroutine for the PreviewVT's whole lifetime.
func (p *PreviewVT) rebuild(taskID string, cols, rows int) {
	switch {
	case p.emu == nil:
		p.emu = NewDrainedEmulator(cols, rows)
		// The preview shows a snapshot of the agent's *current* screen bottom,
		// never its scrollback history, so bound scrollback to the minimum:
		// it caps the memory of this long-lived emulator and limits how many
		// ED-2 (`ESC[2J`, erase-with-scrollback) pushes can surface as stale
		// rows when the main screen is transiently empty. NOTE: 0 would be a
		// no-op — x/vt's Scrollback.SetMaxLines early-returns for <=0 — so 1
		// is the smallest effective cap.
		p.emu.Emulator.SetScrollbackSize(1)
	case p.cols != cols || p.rows != rows:
		// Resize only re-dimensions the screen buffer; it does NOT reset the
		// scrollback cap, so the SetScrollbackSize(1) set at creation persists.
		p.emu.Resize(cols, rows)
	}
	// RIS (`ESC c`) drives x/vt's fullReset: both screen buffers, cursor, saved
	// cursor, scroll region, tab stops, modes, charsets, and parser state — so
	// replaying a different task's output cannot inherit the prior task's cells
	// or pen. Fed before oscStrip is reset and before the caller's replay.
	_, _ = SafeEmuWrite(p.emu, []byte("\x1bc"))
	// fullReset does NOT clear scrollback line content, so a reused emulator
	// would carry the prior task's ED-2-pushed rows into the new task and
	// render them via the grid's sbLen>0 branch (cross-task ghosting). Drop
	// them explicitly on every rebuild.
	p.emu.ClearScrollback()
	p.oscStrip.reset()
	p.fedTotal = 0
	p.taskID = taskID
	p.cols = cols
	p.rows = rows
}

// Reset clears stream-tracking state so the next Feed rebuilds (RIS-resets the
// reused emulator). The emulator and its single drain goroutine are kept alive
// deliberately — see rebuild for why we never Close.
func (p *PreviewVT) Reset() {
	p.taskID = ""
	p.cols = 0
	p.rows = 0
	p.fedTotal = 0
	p.oscStrip.reset()
}
