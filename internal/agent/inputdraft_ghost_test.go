package agent

import (
	"os"
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"

	"github.com/drn/argus/internal/testutil"
)

// composerScreen paints a Claude Code style composer on rows 6-8 of an 80x24
// screen: separator, prompt row (body is written verbatim after the prompt),
// separator, then parks the terminal cursor with the given CUP. Claude Code
// shows its caret with the terminal's own cursor (?25h + CUP), not a painted
// cell, which is why the real captured bytes carry no reverse-video cell.
func composerScreen(body, cursorCUP string) []byte {
	sep := strings.Repeat("─", 78)
	return []byte("\x1b[?1049h\x1b[2J\x1b[6;1H" + sep + "\x1b[7;1H❯ " + body + "\x1b[8;1H" + sep + cursorCUP)
}

func TestInputDraft_ComposerShapes(t *testing.T) {
	const (
		ghost    = "\x1b[2mpush it\x1b[22m"
		caretCol = "\x1b[7;3H" // terminal cursor on the first composer cell
	)
	tests := []struct {
		name      string
		body      string
		cursorCUP string
		want      string
	}{
		{"empty composer", "", caretCol, ""},
		{"ghost suggestion, caret on its first char", ghost, caretCol, ""},
		{"all-faint placeholder", "\x1b[2mTry \"fix this bug\"\x1b[22m", caretCol, ""},
		{"faint plus blink attrs on ghost (as x/vt reports it)", "\x1b[2;5mpush it\x1b[22;25m", caretCol, ""},
		{"real typed draft, caret at end", "fix the bug", "\x1b[7;14H", "fix the bug"},
		{"real draft, caret on its first char", "fix the bug", caretCol, "fix the bug"},
		{"real draft, caret mid-text", "fix the bug", "\x1b[7;8H", "fix the bug"},
		{"mixed faint and normal text is a draft", "run \x1b[2mthe tests\x1b[22m", "\x1b[7;7H", "run the tests"},
		{"normal text then faint tail is a draft", "run the \x1b[2mtests\x1b[22m", "\x1b[7;11H", "run the tests"},
		{"multi-line draft", "line one\r\n  line two", "\x1b[8;11H", "line one\n  line two"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var r ScreenRenderer
			frame := composerScreen(tt.body, tt.cursorCUP)
			if tt.name == "multi-line draft" {
				// The composer grows downward: no bottom separator in the way.
				frame = []byte("\x1b[?1049h\x1b[2J\x1b[7;1H❯ line one\x1b[8;3Hline two\x1b[8;11H")
			}
			got, found := r.InputDraft(frame, 80, 24)
			testutil.Equal(t, found, true)
			testutil.Equal(t, got, tt.want)
		})
	}
}

// TestInputDraft_GhostSuggestionWithOSCTitleLeak is the regression for the
// false "unsubmitted input" annotation. The fixture is reduced from a real
// Claude Code session capture (transcript body trimmed to one line; every
// escape sequence on the composer path is verbatim): the composer shows the
// ghost suggestion "push it" as ESC[2m…ESC[22m with the terminal cursor parked
// on its first cell, and a window-title update `OSC 0 ; ✳ Argus … BEL` arrives
// between two repaints. ✳ is E2 9C B3 and x/ansi ends an OSC at the 0x9C byte,
// so without OSC stripping the title tail printed at the cursor, over the
// composer row, as NON-faint text that survived the faint-only repaint.
func TestInputDraft_GhostSuggestionWithOSCTitleLeak(t *testing.T) {
	raw, err := os.ReadFile("testdata/ghost_osc_title.bin")
	testutil.NoError(t, err)

	var r ScreenRenderer
	got, found := r.InputDraft(raw, 123, 65)
	testutil.Equal(t, found, true)
	testutil.Equal(t, got, "")

	// Ground truth for the first composer cell: faint, not reverse-video.
	cell := r.emu.CellAt(2, r.emu.CursorPosition().Y)
	testutil.Equal(t, cell.Content, "p")
	if cell.Style.Attrs&uv.AttrFaint == 0 {
		t.Fatalf("first ghost cell is not faint: attrs=%b", cell.Style.Attrs)
	}
}

// TestInputDraft_OSCTitleDoesNotHideRealDraft guards the other direction: the
// OSC stripping must not swallow a genuinely typed draft that follows a title.
func TestInputDraft_OSCTitleDoesNotHideRealDraft(t *testing.T) {
	frame := append([]byte("\x1b]0;✳ Argus some title\a"), composerScreen("fix the bug", "\x1b[7;14H")...)
	var r ScreenRenderer
	got, found := r.InputDraft(frame, 80, 24)
	testutil.Equal(t, found, true)
	testutil.Equal(t, got, "fix the bug")
}
