package oscfilter

import (
	"io"
	"testing"

	xvt "github.com/charmbracelet/x/vt"

	"github.com/drn/argus/internal/testutil"
)

func TestStrip(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"plain text", "hello world", "hello world"},
		{"empty", "", ""},
		{"osc bel title", "\x1b]0;my title\x07X", "X"},
		{"osc 7-bit ST title", "\x1b]0;my title\x1b\\X", "X"},
		{"osc icon+title", "\x1b]2;set title\x07rest", "rest"},
		// The actual bug: ✳ is E2 9C B3 — the 0x9C must NOT terminate the OSC,
		// or the tail of the title leaks onto the screen.
		{"osc utf8 title with 0x9c", "\x1b]0;\xe2\x9c\xb3 Review Iris\x07after", "after"},
		{"osc utf8 mid title", "\x1b]2;ab\xe2\x9c\xb3cd\x07Z", "Z"},
		// OSC-8 hyperlinks pass through (terminator normalized to ST) so the
		// emulator can record the link; see TestFilter_Hyperlinks.
		{"osc8 hyperlink", "\x1b]8;;http://x\x07link\x1b]8;;\x07Z", "\x1b]8;;http://x\x1b\\link\x1b]8;;\x1b\\Z"},
		// Only exactly "8;" opens a hyperlink; OSC 80… is still stripped.
		{"osc 80 stripped", "\x1b]80;x\x07Z", "Z"},
		{"osc 8 without semicolon stripped", "\x1b]8\x07Z", "Z"},
		// Non-OSC escape sequences pass through untouched.
		{"csi passthrough", "\x1b[1mhi\x1b[0m", "\x1b[1mhi\x1b[0m"},
		{"esc M passthrough", "\x1bMX", "\x1bMX"},
		{"osc then csi", "\x1b]0;t\x07\x1b[1mX", "\x1b[1mX"},
		{"csi then osc then text", "\x1b[31m\x1b]0;t\x07red", "\x1b[31mred"},
		{"can cancels osc", "\x1b]0;abc\x18Z", "Z"},
		{"sub cancels osc", "\x1b]0;abc\x1aZ", "Z"},
		// A fresh ESC inside an OSC cancels it and begins a new sequence.
		{"esc-new-seq cancels osc", "\x1b]0;abc\x1b[1mX", "\x1b[1mX"},
		{"back to back osc", "\x1b]0;a\x07\x1b]2;b\x07Y", "Y"},
		{"trailing lone esc", "ab\x1b", "ab\x1b"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := string(Strip([]byte(tt.in)))
			testutil.Equal(t, got, tt.want)
		})
	}
}

func TestOSCFilter_SplitAcrossChunks(t *testing.T) {
	tests := []struct {
		name   string
		chunks []string
		want   string
	}{
		{"split mid-title", []string{"\x1b]0;ti", "tle\x07X"}, "X"},
		{"split mid-utf8-glyph", []string{"\x1b]0;\xe2", "\x9c\xb3 Rev\x07Y"}, "Y"},
		{"split at introducer", []string{"\x1b", "]0;t\x07Z"}, "Z"},
		{"split before backslash ST", []string{"\x1b]0;t\x1b", "\\W"}, "W"},
		{"esc held then real text", []string{"a\x1b", "[1mb"}, "a\x1b[1mb"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var f Filter
			var got []byte
			for _, c := range tt.chunks {
				got = append(got, f.Filter([]byte(c))...)
			}
			got = append(got, f.Flush()...)
			testutil.Equal(t, string(got), tt.want)
		})
	}
}

func TestOSCFilter_Reset(t *testing.T) {
	var f Filter
	// Leave the filter mid-OSC, then reset and feed plain text.
	f.Filter([]byte("\x1b]0;partial"))
	f.Reset()
	got := string(f.Filter([]byte("clean")))
	testutil.Equal(t, got, "clean")
}

func TestOSCFilter_RunawayGuard(t *testing.T) {
	// An OSC that never sends a recognized terminator must not drop unbounded:
	// after maxOSCDropBytes the filter resumes passing bytes through.
	var f Filter
	in := make([]byte, 0, maxOSCDropBytes+100)
	in = append(in, '\x1b', ']', '0', ';')
	for i := 0; i < maxOSCDropBytes+50; i++ {
		in = append(in, 'a')
	}
	got := f.Filter(in)
	if len(got) == 0 {
		t.Fatal("runaway guard never resumed output")
	}
}

// TestOSCFilter_FixesEmulatorLeak is the end-to-end regression: without the
// filter, x/vt's parser truncates a UTF-8 OSC title at the embedded 0x9C and
// renders the tail as printable text. With the filter, the screen stays clean.
func TestOSCFilter_FixesEmulatorLeak(t *testing.T) {
	// ✳ = E2 9C B3; the 0x9C byte triggers the upstream bug.
	leakySeq := []byte("\x1b]2;\xe2\x9c\xb3 Review Iris permissions\x07")

	row := func(emu *xvt.SafeEmulator) string {
		s := ""
		for x := 0; x < 40; x++ {
			c := emu.CellAt(x, 0)
			if c == nil || c.Content == "" {
				s += " "
			} else {
				s += c.Content
			}
		}
		return s
	}

	// Sanity: confirm the bug exists unfiltered, so this test fails loudly if a
	// future x/vt upgrade changes behavior and the workaround becomes moot.
	raw := xvt.NewSafeEmulator(40, 3)
	go io.Copy(io.Discard, raw) //nolint:errcheck
	raw.Write(leakySeq)         //nolint:errcheck
	if got := row(raw); got == "                                        " {
		t.Skip("upstream x/vt no longer leaks UTF-8 OSC titles; workaround may be removable")
	}

	// Filtered: the title is gone and the screen is blank.
	filtered := xvt.NewSafeEmulator(40, 3)
	go io.Copy(io.Discard, filtered) //nolint:errcheck
	filtered.Write(Strip(leakySeq))  //nolint:errcheck
	testutil.Equal(t, row(filtered), "                                        ")
}

func TestFilter_Hyperlinks(t *testing.T) {
	const st = "\x1b\\"
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			"claude code link with id param, BEL terminated",
			"\x1b]8;id=ub9bei;https://app.notion.com/p/abc?pvs=204\x07\x1b[94mHi\x1b]8;;\x07",
			"\x1b]8;id=ub9bei;https://app.notion.com/p/abc?pvs=204" + st + "\x1b[94mHi\x1b]8;;" + st,
		},
		{
			"ST terminated passes through unchanged",
			"\x1b]8;;https://x.test/a\x1b\\L\x1b]8;;\x1b\\",
			"\x1b]8;;https://x.test/a" + st + "L\x1b]8;;" + st,
		},
		{
			// ✳ = E2 9C B3: the 0x9C must never reach the parser inside an OSC.
			"non-ascii bytes percent-encoded",
			"\x1b]8;;https://x.test/\xe2\x9c\xb3\x07L",
			"\x1b]8;;https://x.test/%E2%9C%B3" + st + "L",
		},
		{
			"control bytes percent-encoded",
			"\x1b]8;;https://x.test/a\x01b\x7f\x07L",
			"\x1b]8;;https://x.test/a%01b%7F" + st + "L",
		},
		{"malformed hyperlink without uri separator stripped", "\x1b]8;nouri\x07L", "L"},
		{"can cancels hyperlink", "\x1b]8;;https://x\x18L", "L"},
		{"sub cancels hyperlink", "\x1b]8;;https://x\x1aL", "L"},
		{"fresh esc cancels hyperlink", "\x1b]8;;https://x\x1b[1mL", "\x1b[1mL"},
		{"title then link", "\x1b]0;\xe2\x9c\xb3 t\x07\x1b]8;;u\x07L", "\x1b]8;;u" + st + "L"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testutil.Equal(t, string(Strip([]byte(tt.in))), tt.want)
		})
	}
}

func TestFilter_HyperlinkSplitAcrossFeeds(t *testing.T) {
	in := "\x1b]8;id=1;https://x.test/\xe2\x9c\xb3\x07Hi\x1b]8;;\x1b\\"
	want := "\x1b]8;id=1;https://x.test/%E2%9C%B3\x1b\\Hi\x1b]8;;\x1b\\"
	// Split at every possible byte boundary.
	for cut := 1; cut < len(in); cut++ {
		var f Filter
		got := string(f.Filter([]byte(in[:cut])))
		got += string(f.Filter([]byte(in[cut:])))
		got += string(f.Flush())
		if got != want {
			t.Fatalf("cut=%d: got %q, want %q", cut, got, want)
		}
	}
}

func TestFilter_OversizedHyperlinkStripped(t *testing.T) {
	url := make([]byte, maxHyperlinkBytes+10)
	for i := range url {
		url[i] = 'a'
	}
	in := "\x1b]8;;" + string(url) + "\x07link\x1b]8;;\x07"
	got := string(Strip([]byte(in)))
	testutil.Equal(t, got, "link\x1b]8;;\x1b\\")
}

func TestFilter_ResetClearsHyperlink(t *testing.T) {
	var f Filter
	f.Filter([]byte("\x1b]8;;https://partial"))
	f.Reset()
	testutil.Equal(t, string(f.Filter([]byte("clean\x07"))), "clean\x07")
}

func TestHyperlinkID(t *testing.T) {
	tests := []struct{ params, want string }{
		{"", ""},
		{"id=ub9bei", "ub9bei"},
		{"foo=bar:id=x1", "x1"},
		{"id=x1:foo=bar", "x1"},
		{"identity=nope", ""},
		{"id=", ""},
	}
	for _, tt := range tests {
		t.Run(tt.params, func(t *testing.T) {
			testutil.Equal(t, HyperlinkID(tt.params), tt.want)
		})
	}
}

// TestFilter_HyperlinkReachesEmulator drives real Claude Code OSC-8 bytes
// through the filter into x/vt and checks the cell records the link. The
// August 2026 fix tested the emulator directly and missed that the filter
// dropped every link; this test exists so that can't happen again.
func TestFilter_HyperlinkReachesEmulator(t *testing.T) {
	in := []byte("\x1b]8;id=ub9bei;https://app.notion.com/p/abc?pvs=204\x07\x1b[94mHi\x1b]8;;\x07 x")
	emu := xvt.NewSafeEmulator(20, 2)
	go io.Copy(io.Discard, emu) //nolint:errcheck
	emu.Write(Strip(in))        //nolint:errcheck
	for x := 0; x < 2; x++ {
		c := emu.CellAt(x, 0)
		testutil.Equal(t, c.Link.URL, "https://app.notion.com/p/abc?pvs=204")
		testutil.Equal(t, c.Link.Params, "id=ub9bei")
	}
	testutil.Equal(t, emu.CellAt(3, 0).Link.URL, "")
}
