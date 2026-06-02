package terminal

import (
	"io"
	"testing"

	xvt "github.com/charmbracelet/x/vt"

	"github.com/drn/argus/internal/testutil"
)

func TestFilterOSC(t *testing.T) {
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
		// OSC-8 hyperlink: the URL wrappers are dropped, the visible text stays.
		{"osc8 hyperlink", "\x1b]8;;http://x\x07link\x1b]8;;\x07Z", "linkZ"},
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
			got := string(FilterOSC([]byte(tt.in)))
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
			var f oscFilter
			var got []byte
			for _, c := range tt.chunks {
				got = append(got, f.filter([]byte(c))...)
			}
			got = append(got, f.flush()...)
			testutil.Equal(t, string(got), tt.want)
		})
	}
}

func TestOSCFilter_Reset(t *testing.T) {
	var f oscFilter
	// Leave the filter mid-OSC, then reset and feed plain text.
	f.filter([]byte("\x1b]0;partial"))
	f.reset()
	got := string(f.filter([]byte("clean")))
	testutil.Equal(t, got, "clean")
}

func TestOSCFilter_RunawayGuard(t *testing.T) {
	// An OSC that never sends a recognized terminator must not drop unbounded:
	// after maxOSCDropBytes the filter resumes passing bytes through.
	var f oscFilter
	in := make([]byte, 0, maxOSCDropBytes+100)
	in = append(in, '\x1b', ']', '0', ';')
	for i := 0; i < maxOSCDropBytes+50; i++ {
		in = append(in, 'a')
	}
	got := f.filter(in)
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
	go io.Copy(io.Discard, filtered)    //nolint:errcheck
	filtered.Write(FilterOSC(leakySeq)) //nolint:errcheck
	testutil.Equal(t, row(filtered), "                                        ")
}
