package tui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/testutil"
)

// wideSessionBytes is a minimal reproduction of Claude's repaint stream on a
// wide PTY: absolute cursor positioning (CSI row;col H / CSI col G) that
// places content at columns far beyond the preview pane width. Re-emulating
// these bytes in a pane-width emulator clamps the positioning at the right
// edge and autowraps the tail onto the next row — the "scrambled preview"
// defect (holes in words, orphan chars down the right edge, shredded lines).
// Row 2 is written FIRST so the narrow-emulator wrap of row 1's overflow
// lands on top of it (overwriting "second l" with "IGHTEDGE") — pinning the
// scramble in a way later writes can't mask.
var wideSessionBytes = []byte("\x1b[2J\x1b[2;1Hsecond line\x1b[1;1HSummary of changes\x1b[100GRIGHTEDGE")

func TestTaskPreviewPanel_WideSessionEmulatedAtPTYWidth_NoScramble(t *testing.T) {
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { screen.Fini() })
	screen.SetSize(40, 10)

	tp := NewTaskPreviewPanel()
	tp.SetRect(1, 1, 38, 8)
	tp.SetTaskID("wide-task")

	// Emulate at the session's real PTY width (200), clip to the 36-col view.
	tp.RefreshOutput("wide-task", wideSessionBytes, uint64(len(wideSessionBytes)), 200, 24, 36, 6)
	tp.Draw(screen)

	if !previewScreenContains(screen, "Summary of changes") {
		t.Fatal("expected left-aligned content to render intact")
	}
	if !previewScreenContains(screen, "second line") {
		t.Fatal("expected following line to render intact")
	}
	// Col-100 content lies beyond the viewport: it must be clipped away,
	// not clamped to the right edge and wrapped onto the next row.
	if previewScreenContains(screen, "RIGHTEDGE") || previewScreenContains(screen, "IGHTEDGE") {
		t.Fatal("expected beyond-viewport content to be clipped, not wrapped/scrambled")
	}
}

func TestPreviewEmuSize(t *testing.T) {
	tests := []struct {
		name             string
		ptyCols, ptyRows int
		sizeFile         bool
		wantCols         int
		wantRows         int
		wantSrc          string
	}{
		{"live_pty_size_wins", 316, 82, true, 316, 82, "pty"},
		{"live_pty_zero_rows_uses_pane_rows", 316, 0, false, 316, 20, "pty"},
		{"no_pty_uses_size_file", 0, 0, true, 200, 50, "sizefile"},
		{"no_pty_no_size_file_falls_back_to_pane", 0, 0, false, 60, 20, "pane"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			if tt.sizeFile {
				agent.SaveSessionSize("emusize-task", 200, 50)
			}
			cols, rows, src := previewEmuSize("emusize-task", tt.ptyCols, tt.ptyRows, 60, 20)
			testutil.Equal(t, cols, tt.wantCols)
			testutil.Equal(t, rows, tt.wantRows)
			testutil.Equal(t, src, tt.wantSrc)
		})
	}
}

// writeSessionLog writes raw bytes as the on-disk session log for taskID.
// HOME must already point at a temp dir.
func writeSessionLog(t *testing.T, taskID string, raw []byte) {
	t.Helper()
	logPath := agent.SessionLogPath(taskID)
	testutil.NoError(t, os.MkdirAll(filepath.Dir(logPath), 0o700))
	testutil.NoError(t, os.WriteFile(logPath, raw, 0o600))
}

func TestApp_RefreshPreview_DeadSessionUsesSizeFile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	writeSessionLog(t, "dead-wide", wideSessionBytes)
	// The session ran on a 200x24 PTY; the sidecar survives its exit.
	agent.SaveSessionSize("dead-wide", 200, 24)

	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
	screen := drawSim(t)
	app.taskPreview.SetRect(0, 0, 40, 10)
	app.taskPreview.SetTaskID("dead-wide")
	app.taskPreview.Draw(screen) // caches pane inner dims for refreshPreview

	app.refreshPreview("dead-wide")
	app.taskPreview.Draw(screen)

	if !previewScreenContains(screen, "Summary of changes") {
		t.Fatal("expected dead-session preview emulated at persisted PTY width to render intact")
	}
	if !previewScreenContains(screen, "second line") {
		t.Fatal("expected second row to survive — at pane width the wrapped overflow overwrites it")
	}
	// At pane width the col-100 write clamps to the right edge and wraps —
	// the scramble this fix removes. It must be clipped instead.
	if previewScreenContains(screen, "RIGHTEDGE") || previewScreenContains(screen, "IGHTEDGE") {
		t.Fatal("expected beyond-pane content to be clipped, not wrapped/scrambled")
	}
}

func TestApp_RefreshPreview_DeadSessionNoSizeFile_FallsBackToPane(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	writeSessionLog(t, "dead-plain", []byte("plain output content\r\n"))

	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
	screen := drawSim(t)
	app.taskPreview.SetRect(0, 0, 40, 10)
	app.taskPreview.SetTaskID("dead-plain")
	app.taskPreview.Draw(screen)

	app.refreshPreview("dead-plain")
	app.taskPreview.Draw(screen)

	if !previewScreenContains(screen, "plain output content") {
		t.Fatal("expected pane-width fallback to still render plain content")
	}
}
