package terminal

import (
	"fmt"
	"runtime"
	"strings"
	"testing"
	"time"

	xvt "github.com/charmbracelet/x/vt"

	"github.com/drn/argus/internal/testutil"
)

// emuScreen renders the emulator's main screen as newline-joined rows, with
// trailing blanks on each row trimmed. Used to compare emulator states.
func emuScreen(emu *xvt.SafeEmulator, cols, rows int) string {
	var b strings.Builder
	for y := range rows {
		var line strings.Builder
		for x := range cols {
			ch := ' '
			if cell := emu.CellAt(x, y); cell != nil && cell.Content != "" {
				ch = []rune(cell.Content)[0]
			}
			line.WriteRune(ch)
		}
		b.WriteString(strings.TrimRight(line.String(), " "))
		b.WriteByte('\n')
	}
	return b.String()
}

// feedWhole emulates the entire stream into a single fresh emulator — the
// reference rendering that a persistent in-order feed must match.
func feedWhole(t *testing.T, cols, rows int, stream []byte) string {
	t.Helper()
	emu := NewDrainedEmulator(cols, rows)
	if _, err := SafeEmuWrite(emu, FilterOSC(AlignToEscBoundary(stream))); err != nil {
		t.Fatalf("whole-stream feed errored: %v", err)
	}
	return emuScreen(emu, cols, rows)
}

// TestPreviewVT_IncrementalEqualsWholeStream is the core correctness proof and
// the ghost-cell regression guard. A throwaway emulator replaying only the
// final tail loses the erase sequences emitted earlier in the stream, leaving
// superseded progress digits as ghost cells. PreviewVT feeds every byte in
// order, so its final screen must equal a single emulator fed the whole
// stream — erases and all.
func TestPreviewVT_IncrementalEqualsWholeStream(t *testing.T) {
	const cols, rows = 40, 6
	// "20 widgets" is drawn, then overwritten in place by "9 widgets" (one
	// char shorter) followed by ESC[K (erase to end of line) which clears the
	// orphaned trailing "s". Replaying only the post-overwrite tail without
	// the ESC[K — the old throwaway behavior — would leave "9 widgetss" with a
	// ghost "s". The whole-stream reference renders the correct "9 widgets".
	stream := []byte("\x1b[2J\x1b[H20 widgets\x1b[H9 widgets\x1b[K")
	want := feedWhole(t, cols, rows, stream)
	testutil.Contains(t, want, "9 widgets")
	if strings.Contains(want, "widgetss") {
		t.Fatalf("reference itself ghosted: %q", want)
	}

	tests := []struct {
		name   string
		splits []int // byte offsets to chunk the stream at
	}{
		{"single feed", nil},
		{"split mid-counter", []int{len("\x1b[2J\x1b[H20 wid")}},
		{"split before erase", []int{len("\x1b[2J\x1b[H20 widgets\x1b[H9 widgets")}},
		{"byte by byte", byteOffsets(len(stream))},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var pv PreviewVT
			var emu *xvt.SafeEmulator
			feedTo := func(end int) {
				e, err := pv.Feed("task", cols, rows, stream[:end], uint64(end)) //nolint:gosec // test offset, non-negative
				testutil.NoError(t, err)
				emu = e
			}
			for _, off := range tc.splits {
				feedTo(off)
			}
			feedTo(len(stream))

			got := emuScreen(emu, cols, rows)
			testutil.Equal(t, got, want)
			if strings.Contains(got, "widgetss") {
				t.Fatalf("ghost cell survived incremental feed: %q", got)
			}
		})
	}
}

func byteOffsets(n int) []int {
	out := make([]int, 0, n-1)
	for i := 1; i < n; i++ {
		out = append(out, i)
	}
	return out
}

func TestPreviewVT_RebuildOnTaskChange(t *testing.T) {
	const cols, rows = 30, 4
	var pv PreviewVT

	a := []byte("\x1b[2J\x1b[Halpha-output")
	_, err := pv.Feed("task-a", cols, rows, a, uint64(len(a)))
	testutil.NoError(t, err)

	b := []byte("\x1b[2J\x1b[Hbeta-output")
	emu, err := pv.Feed("task-b", cols, rows, b, uint64(len(b)))
	testutil.NoError(t, err)

	screen := emuScreen(emu, cols, rows)
	testutil.Contains(t, screen, "beta-output")
	if strings.Contains(screen, "alpha") {
		t.Fatalf("task A content leaked after task change: %q", screen)
	}
}

func TestPreviewVT_RebuildOnDimensionChange(t *testing.T) {
	var pv PreviewVT
	// Multiple ED-2 frames so scrollback would grow if the cap regressed.
	s := []byte("\x1b[2J\x1b[Hone\x1b[2J\x1b[Htwo\x1b[2J\x1b[Hthree")
	emu, err := pv.Feed("t", 30, 4, s, uint64(len(s)))
	testutil.NoError(t, err)
	testutil.Equal(t, pv.cols, 30)
	if emu.ScrollbackLen() > 1 {
		t.Fatalf("scrollback not capped before resize: %d", emu.ScrollbackLen())
	}

	// New dimensions force a rebuild; fedTotal resets then re-advances. The
	// scrollback cap set at creation must persist across Resize (the rebuild
	// also ClearScrollback()s, so it lands at 0).
	emu, err = pv.Feed("t", 50, 8, s, uint64(len(s)))
	testutil.NoError(t, err)
	testutil.Equal(t, pv.cols, 50)
	testutil.Equal(t, pv.rows, 8)
	testutil.Equal(t, pv.fedTotal, uint64(len(s)))
	if emu.ScrollbackLen() > 1 {
		t.Fatalf("scrollback cap not preserved across resize: %d", emu.ScrollbackLen())
	}
}

// TestPreviewVT_LiveToDeadTransitionFullReplays covers the app-level live→dead
// handoff at the engine boundary: a live session feeds a high totalWritten
// (ring high-water mark), then the session exits and the dead path feeds the
// log's file size as totalWritten — which is SMALLER than the prior fedTotal.
// The uint64 underflow in `newBytes = totalWritten - fedTotal` must trip the
// fullReplay guard (newBytes > len(tail)) so the emulator rebuilds and replays
// the dead tail cleanly, rather than slicing a bogus negative-length suffix.
func TestPreviewVT_LiveToDeadTransitionFullReplays(t *testing.T) {
	const cols, rows = 40, 6
	var pv PreviewVT

	// Live: high-water mark far ahead (only the tail is held).
	live := []byte("\x1b[2J\x1b[Hlive-frame")
	_, err := pv.Feed("t", cols, rows, live, 5_000_000)
	testutil.NoError(t, err)
	testutil.Equal(t, pv.fedTotal, uint64(5_000_000))

	// Dead: log file size (much smaller than 5M) as totalWritten.
	dead := []byte("\x1b[2J\x1b[Hdead-frame")
	emu, err := pv.Feed("t", cols, rows, dead, uint64(len(dead)))
	testutil.NoError(t, err)
	screen := emuScreen(emu, cols, rows)
	testutil.Contains(t, screen, "dead-frame")
	if strings.Contains(screen, "live-frame") {
		t.Fatalf("live frame survived live→dead transition: %q", screen)
	}
	testutil.Equal(t, pv.fedTotal, uint64(len(dead)))
}

// TestPreviewVT_RingWrapFullReplays simulates a steady live stream where the
// high-water mark advances by more than the bounded tail holds (a ring wrap):
// the unseen suffix is no longer contiguous, so PreviewVT must full-replay the
// tail rather than feed a bogus slice.
func TestPreviewVT_RingWrapFullReplays(t *testing.T) {
	const cols, rows = 40, 6
	var pv PreviewVT

	first := []byte("\x1b[2J\x1b[Hframe-one")
	_, err := pv.Feed("t", cols, rows, first, uint64(len(first)))
	testutil.NoError(t, err)

	// Tail now holds only "frame-two" but totalWritten jumped far past
	// fedTotal+len(tail): bytes were dropped from the ring. fullReplay path.
	tail := []byte("\x1b[2J\x1b[Hframe-two")
	emu, err := pv.Feed("t", cols, rows, tail, uint64(len(first))+1_000_000)
	testutil.NoError(t, err)

	screen := emuScreen(emu, cols, rows)
	testutil.Contains(t, screen, "frame-two")
	if strings.Contains(screen, "frame-one") {
		t.Fatalf("stale frame survived ring wrap: %q", screen)
	}
	testutil.Equal(t, pv.fedTotal, uint64(len(first))+1_000_000)
}

func TestPreviewVT_EmptyReturnsNil(t *testing.T) {
	var pv PreviewVT
	emu, err := pv.Feed("t", 30, 4, nil, 0)
	testutil.NoError(t, err)
	testutil.Nil(t, emu)

	emu, err = pv.Feed("t", 30, 4, []byte{}, 0)
	testutil.NoError(t, err)
	testutil.Nil(t, emu)
}

// TestPreviewVT_NoNewBytesKeepsContent ensures a repeat Feed with an unchanged
// high-water mark is a no-op that returns the same emulator (no double-feed,
// no duplicated content).
func TestPreviewVT_NoNewBytesKeepsContent(t *testing.T) {
	const cols, rows = 40, 6
	var pv PreviewVT
	s := []byte("\x1b[2J\x1b[Hcount: 5")
	emu1, err := pv.Feed("t", cols, rows, s, uint64(len(s)))
	testutil.NoError(t, err)
	before := emuScreen(emu1, cols, rows)

	emu2, err := pv.Feed("t", cols, rows, s, uint64(len(s)))
	testutil.NoError(t, err)
	testutil.Equal(t, emu1, emu2)
	testutil.Equal(t, emuScreen(emu2, cols, rows), before)
}

// TestPreviewVT_EmptyTailKeepsPriorContent: a transient empty tail after
// content has been shown must not blank the preview.
func TestPreviewVT_EmptyTailKeepsPriorContent(t *testing.T) {
	const cols, rows = 40, 6
	var pv PreviewVT
	s := []byte("\x1b[2J\x1b[Hpersisted")
	_, err := pv.Feed("t", cols, rows, s, uint64(len(s)))
	testutil.NoError(t, err)

	emu, err := pv.Feed("t", cols, rows, nil, uint64(len(s)))
	testutil.NoError(t, err)
	if emu == nil {
		t.Fatal("empty tail dropped prior content")
	}
	testutil.Contains(t, emuScreen(emu, cols, rows), "persisted")
}

// TestPreviewVT_ScrollbackBoundedAndClearedOnRebuild is the sentinel for the
// scrollback regression. Agents emit ED-2 (`ESC[2J`, erase-with-scrollback)
// before every full repaint, pushing the prior screen into scrollback. The
// preview emulator must (a) bound that growth (a no-op SetScrollbackSize(0)
// would leave the 10K default, accumulating a frame per repaint) and (b) drop
// it entirely on rebuild — otherwise the prior task's rows surface via the
// grid's sbLen>0 branch as cross-task ghosts (RIS/fullReset alone does NOT
// clear scrollback content).
func TestPreviewVT_ScrollbackBoundedAndClearedOnRebuild(t *testing.T) {
	const cols, rows = 30, 4
	var pv PreviewVT

	// Task A: five frames, each ED-2 pushing the prior one into scrollback.
	var a []byte
	for i := range 5 {
		a = fmt.Appendf(a, "\x1b[2J\x1b[Halpha-%d", i)
	}
	emuA, err := pv.Feed("task-a", cols, rows, a, uint64(len(a)))
	testutil.NoError(t, err)
	if got := emuA.ScrollbackLen(); got > 1 {
		t.Fatalf("scrollback unbounded (SetScrollbackSize(0) no-op regression?): ScrollbackLen=%d", got)
	}

	// Switch to task B: rebuild must clear scrollback so task-A rows cannot leak.
	b := []byte("\x1b[2J\x1b[Hbeta")
	emuB, err := pv.Feed("task-b", cols, rows, b, uint64(len(b)))
	testutil.NoError(t, err)
	testutil.Equal(t, emuB.ScrollbackLen(), 0)
	if strings.Contains(emuScreen(emuB, cols, rows), "alpha") {
		t.Fatalf("prior task content survived rebuild: %q", emuScreen(emuB, cols, rows))
	}
}

// TestPreviewVT_PanicRecoveryResets feeds a sequence that drives x/vt out of
// bounds; the recovered panic must surface as an error and drop the emulator.
func TestPreviewVT_PanicRecoveryResets(t *testing.T) {
	var pv PreviewVT
	// CSI 82;1H positions the cursor far below a 5-row emulator, then reverse
	// index (ESC M) triggers the upstream InsertLineArea panic.
	bad := []byte("\x1b[82;1H\x1bMboom")
	emu, err := pv.Feed("t", 10, 5, bad, uint64(len(bad)))
	if err == nil {
		// Upstream may have fixed the panic; then content is fine and the
		// engine stays consistent. Nothing to assert beyond no crash.
		if emu == nil {
			t.Fatal("no error but nil emulator")
		}
		return
	}
	testutil.Nil(t, emu)
	// Dropped: next Feed rebuilds from scratch.
	testutil.Equal(t, pv.fedTotal, uint64(0))
	if pv.emu != nil {
		t.Fatal("emulator not dropped after panic")
	}
}

// TestPreviewVT_RebuildDoesNotLeakGoroutines proves the drain-goroutine fix.
// NewDrainedEmulator spawns one io.Copy reader per emulator that blocks until
// the response pipe closes; rebuild fires on every task-list cursor move. The
// fix reuses a single emulator across rebuilds (RIS reset) instead of
// allocating one each time, so N rebuilds create exactly ONE drain goroutine,
// not N. After the loop the count must sit at ~baseline+1 (the one persistent
// goroutine), never baseline+N — which is what an alloc-per-rebuild regression
// would produce.
func TestPreviewVT_RebuildDoesNotLeakGoroutines(t *testing.T) {
	if testing.Short() {
		t.Skip("spins emulators; skipped under -short")
	}
	s := []byte("\x1b[2J\x1b[Hx")
	runtime.GC()
	base := runtime.NumGoroutine()

	var pv PreviewVT
	const N = 40
	for i := range N {
		// Distinct taskID forces a rebuild (RIS reset of the reused emulator)
		// on every call — no new emulator, no new goroutine.
		_, err := pv.Feed(fmt.Sprintf("task-%d", i), 40, 6, s, uint64(len(s)))
		testutil.NoError(t, err)
	}
	pv.Reset() // keeps the one emulator + its drain goroutine alive (no Close — racy)

	// The single persistent drain goroutine settles the count at ~baseline+1.
	// An alloc-per-rebuild regression would instead leave ~N extra goroutines
	// blocked indefinitely. Poll briefly to absorb scheduler/GC jitter.
	settled := false
	for range 100 {
		runtime.GC()
		if runtime.NumGoroutine() <= base+3 {
			settled = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !settled {
		t.Fatalf("goroutines did not settle after %d rebuilds: base=%d now=%d", N, base, runtime.NumGoroutine())
	}
}

func TestPreviewVT_Reset(t *testing.T) {
	var pv PreviewVT
	s := []byte("\x1b[2J\x1b[Hstale-content")
	emuBefore, err := pv.Feed("t", 30, 4, s, uint64(len(s)))
	testutil.NoError(t, err)

	pv.Reset()
	testutil.Equal(t, pv.fedTotal, uint64(0))
	testutil.Equal(t, pv.taskID, "")
	// Reset clears tracking state but KEEPS the emulator (and its single drain
	// goroutine) alive — we never Close (racy). The next Feed reuses it.
	if pv.emu == nil {
		t.Fatal("Reset must keep the emulator for reuse, not drop it")
	}

	// Feed after Reset rebuilds cleanly via RIS — same emulator instance, no
	// stale content carried over.
	fresh := []byte("\x1b[2J\x1b[Hnew-content")
	emuAfter, err := pv.Feed("t", 30, 4, fresh, uint64(len(fresh)))
	testutil.NoError(t, err)
	testutil.Equal(t, emuBefore, emuAfter) // reused, not reallocated
	screen := emuScreen(emuAfter, 30, 4)
	testutil.Contains(t, screen, "new-content")
	if strings.Contains(screen, "stale-content") {
		t.Fatalf("RIS reset did not clear prior content: %q", screen)
	}
}
