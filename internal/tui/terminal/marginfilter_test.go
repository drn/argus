package terminal

import (
	"strings"
	"testing"

	xvt "github.com/charmbracelet/x/vt"
	"github.com/gdamore/tcell/v2"

	"github.com/drn/argus/internal/testutil"
)

// TestClampScrollRegion_StaleDECSTBMPanicsWithoutClamping is the direct,
// library-level reproduction: a DECSTBM sequence authored for a 50-row
// terminal, replayed unmodified into a 10-row x/vt emulator, panics inside
// ultraviolet's DeleteLineArea. This documents the exact upstream mechanism
// ClampScrollRegion defends against — it does not exercise argus code.
func TestClampScrollRegion_StaleDECSTBMPanicsWithoutClamping(t *testing.T) {
	emu := xvt.NewEmulator(48, 10)
	seq := []byte("\x1b[1;50r\x1b[3M")

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected the unclamped sequence to panic (upstream x/vt/ultraviolet bug) — " +
				"if this no longer panics, upstream may have fixed it and ClampScrollRegion's " +
				"rationale in its doc comment needs re-verifying, not just this test")
		}
	}()
	_, _ = emu.Write(seq)
}

func TestClampScrollRegion(t *testing.T) {
	tests := []struct {
		name       string
		in         string
		cols, rows int
		want       string
	}{
		{
			name: "oversized DECSTBM bottom margin clamped to rows",
			in:   "\x1b[1;50r",
			cols: 48, rows: 10,
			want: "\x1b[1;10r",
		},
		{
			name: "oversized DECSLRM right margin clamped to cols",
			in:   "\x1b[1;200s",
			cols: 48, rows: 10,
			want: "\x1b[1;48s",
		},
		{
			name: "in-bounds margin left untouched",
			in:   "\x1b[1;8r",
			cols: 48, rows: 10,
			want: "\x1b[1;8r",
		},
		{
			name: "margin exactly at bound left untouched",
			in:   "\x1b[1;10r",
			cols: 48, rows: 10,
			want: "\x1b[1;10r",
		},
		{
			name: "omitted second param left untouched (defaults inside x/vt itself)",
			in:   "\x1b[5r",
			cols: 48, rows: 10,
			want: "\x1b[5r",
		},
		{
			name: "no params at all left untouched",
			in:   "\x1b[r",
			cols: 48, rows: 10,
			want: "\x1b[r",
		},
		{
			name: "empty second field left untouched (defaults to current height)",
			in:   "\x1b[1;r",
			cols: 48, rows: 10,
			want: "\x1b[1;r",
		},
		{
			name: "private-marker CSI (e.g. DECSET) is not touched even ending in r or s",
			in:   "\x1b[?25l",
			cols: 48, rows: 10,
			want: "\x1b[?25l",
		},
		{
			name: "unrelated final byte (SGR) is passed through unchanged",
			in:   "\x1b[1;50m",
			cols: 48, rows: 10,
			want: "\x1b[1;50m",
		},
		{
			name: "plain text with no escape sequences is untouched",
			in:   "hello world\r\n",
			cols: 48, rows: 10,
			want: "hello world\r\n",
		},
		{
			name: "surrounding content is preserved around a clamped sequence",
			in:   "before\x1b[1;50rafter",
			cols: 48, rows: 10,
			want: "before\x1b[1;10rafter",
		},
		{
			name: "lone ESC with no bracket is passed through",
			in:   "\x1bXsomething",
			cols: 48, rows: 10,
			want: "\x1bXsomething",
		},
		{
			name: "lone trailing ESC at end of buffer is passed through",
			in:   "text\x1b",
			cols: 48, rows: 10,
			want: "text\x1b",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ClampScrollRegion([]byte(tt.in), tt.cols, tt.rows)
			testutil.Equal(t, string(got), tt.want)
		})
	}
}

func TestClampScrollRegion_ZeroDimsIsNoop(t *testing.T) {
	in := "\x1b[1;50r"
	testutil.Equal(t, string(ClampScrollRegion([]byte(in), 0, 10)), in)
	testutil.Equal(t, string(ClampScrollRegion([]byte(in), 48, 0)), in)
}

// TestClampScrollRegion_FixesTheActualPanic re-runs the library-level repro
// above but through the clamp, confirming it no longer panics and preserves
// content that would otherwise be silently dropped by SafeEmuWrite's
// recover-and-drop path.
func TestClampScrollRegion_FixesTheActualPanic(t *testing.T) {
	emu := xvt.NewEmulator(48, 10)
	raw := []byte("\x1b[1;50r\x1b[3Mafter-dl-marker")
	clamped := ClampScrollRegion(raw, 48, 10)

	n, err := emu.Write(clamped)
	testutil.NoError(t, err)
	testutil.Equal(t, n, len(clamped))
}

// TestTerminalPane_ReplayStaleScrollRegionDoesNotDropContent is the full
// end-to-end regression: a session log containing a DECSTBM authored for a
// terminal taller than the pane replaying it, bound the same way a Hera
// coordinator pane binds for replay (no live session). Before
// ClampScrollRegion was wired into asyncReplayRebuild's feed, SafeEmuWrite
// recovered from the DeleteLineArea panic but dropped the entire write —
// including "after-dl-marker" below — leaving the pane blank/stale. See
// gotchas/pty-terminal.md for the BUG-0NN writeup.
func TestTerminalPane_ReplayStaleScrollRegionDoesNotDropContent(t *testing.T) {
	// DECSTBM for a 200-row terminal (this pane's replay emulator gets built
	// at the pane's own small rect — no live session, no size sidecar — so
	// 200 exceeds it by a wide margin regardless of exact pane geometry).
	log := "\x1b[1;200rline-before\r\n\x1b[3Mafter-dl-marker\r\n"
	tp, screen := replayPane(t, "margin-panic-replay", []byte(log))

	tp.mu.Lock()
	emu := tp.replayEmu
	tp.mu.Unlock()
	if emu == nil {
		t.Fatal("replay emulator was never built")
	}

	sim, ok := screen.(tcell.SimulationScreen)
	if !ok {
		t.Fatal("replayPane screen is not a SimulationScreen")
	}
	text := screenText(t, sim, 82, 26)
	if !strings.Contains(text, "after-dl-marker") {
		t.Errorf("replayed pane is missing content written after the oversized DECSTBM+DL sequence "+
			"— it was silently dropped by SafeEmuWrite's recover-and-drop path; got:\n%s", text)
	}
}
