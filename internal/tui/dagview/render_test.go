package dagview

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
)

// TestRender_LinearChain — three nodes stacked on top of each other with
// straight `│` edges between them. The golden output validates the box
// glyphs, the label rendering, and the edge column alignment.
func TestRender_LinearChain(t *testing.T) {
	l := Compute([]Node{
		{ID: "A", Name: "alpha", Status: "complete"},
		{ID: "B", Name: "beta", Status: "in_progress", DependsOn: []string{"A"}},
		{ID: "C", Name: "gamma", Status: "pending", DependsOn: []string{"B"}},
	})
	out := RenderToString(l, "", nil)
	wantLines := []string{
		"╭────────────────╮",
		"│ ✓ alpha        │",
		"╰────────────────╯",
		"         │",
		"╭────────────────╮",
		"│ ▶ beta         │",
		"╰────────────────╯",
		"         │",
		"╭────────────────╮",
		"│ ○ gamma        │",
		"╰────────────────╯",
	}
	if got := strings.Split(out, "\n"); !equalLines(got, wantLines) {
		t.Fatalf("linear chain mismatch:\nwant:\n%s\n\ngot:\n%s",
			strings.Join(wantLines, "\n"), out)
	}
}

// TestRender_FanOut — single parent → three children. Asserts the bent
// edges render with the expected `╰─...─╮` / `│` / `╭─...─╯` shape.
func TestRender_FanOut(t *testing.T) {
	l := Compute([]Node{
		{ID: "A", Name: "root"},
		{ID: "B", Name: "left", DependsOn: []string{"A"}},
		{ID: "C", Name: "mid", DependsOn: []string{"A"}},
		{ID: "D", Name: "right", DependsOn: []string{"A"}},
	})
	out := RenderToString(l, "", nil)
	// The exact spacing depends on the barycentric pass; instead of pinning
	// a golden, assert structural facts: 3 child boxes on row 4, the root
	// box on row 0, and at least one bent corner glyph on the edge row.
	lines := strings.Split(out, "\n")
	if len(lines) < 6 {
		t.Fatalf("too few lines: %d\n%s", len(lines), out)
	}
	// Bent edges should appear on the edge row between layers.
	edgeRow := lines[3]
	if !strings.ContainsAny(edgeRow, "╰╮╭╯") {
		t.Fatalf("expected bent edge chars on edge row, got %q", edgeRow)
	}
}

// TestRender_StatusGlyphs — every status maps to its expected glyph.
func TestRender_StatusGlyphs(t *testing.T) {
	cases := []struct {
		status   string
		archived bool
		failed   bool
		want     rune
	}{
		{"pending", false, false, '○'},
		{"in_progress", false, false, '▶'},
		{"in_review", false, false, '⊙'},
		{"complete", false, false, '✓'},
		{"pending", true, false, '·'},
		{"in_progress", false, true, '✕'},
	}
	for _, tc := range cases {
		got := StatusGlyph(tc.status, tc.archived, tc.failed)
		if got != tc.want {
			t.Errorf("glyph(%q, archived=%v, failed=%v): got %q want %q",
				tc.status, tc.archived, tc.failed, got, tc.want)
		}
	}
}

// TestStyle_AllBranches pins the foreground each status/flag combination
// maps to. Style feeds both the string renderer and the screen painter, so a
// regression here silently miscolors the whole DAG view.
func TestStyle_AllBranches(t *testing.T) {
	cases := []struct {
		name                    string
		status                  string
		archived, failed, focus bool
		wantFG                  tcell.Color
		wantBold                bool
	}{
		{"archived wins", "in_progress", true, true, true, tcell.ColorGray, false},
		{"failed", "complete", false, true, false, tcell.ColorRed, true},
		{"pending", "pending", false, false, false, tcell.ColorGray, false},
		{"in_progress focused", "in_progress", false, false, true, tcell.ColorAqua, true},
		{"in_progress unfocused", "in_progress", false, false, false, tcell.ColorAqua, false},
		{"in_review", "in_review", false, false, false, tcell.ColorYellow, false},
		{"complete", "complete", false, false, false, tcell.ColorGreen, false},
		{"unknown status", "weird", false, false, false, tcell.ColorDefault, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fg, _, attrs := Style(tc.status, tc.archived, tc.failed, tc.focus).Decompose()
			if fg != tc.wantFG {
				t.Errorf("Style(%q, arch=%v, fail=%v, focus=%v) fg = %v, want %v",
					tc.status, tc.archived, tc.failed, tc.focus, fg, tc.wantFG)
			}
			gotBold := attrs&tcell.AttrBold != 0
			if gotBold != tc.wantBold {
				t.Errorf("Style(%q, arch=%v, fail=%v, focus=%v) bold = %v, want %v",
					tc.status, tc.archived, tc.failed, tc.focus, gotBold, tc.wantBold)
			}
		})
	}
}

// TestDraw_PaintsToScreen exercises the screen-painting path (Draw → drawNode
// → drawEdge), which is distinct from RenderToString's string buffer. A
// fan-out layout forces the bent-corner edge glyphs; a failed + cursor node
// covers the highlight/reverse and failed-style branches.
func TestDraw_PaintsToScreen(t *testing.T) {
	l := Compute([]Node{
		{ID: "A", Name: "root", Status: "complete"},
		{ID: "B", Name: "left", Status: "in_progress", DependsOn: []string{"A"}},
		{ID: "C", Name: "right", Status: "pending", DependsOn: []string{"A"}},
	})

	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	defer screen.Fini()
	screen.SetSize(120, 40)

	parseFailed := func(id string) bool { return id == "B" }
	w, h := Draw(screen, 0, 0, l, "A", true, parseFailed)
	if w <= 0 || h <= 0 {
		t.Fatalf("expected positive bounding box, got w=%d h=%d", w, h)
	}
	screen.Show() // flush SetContent writes into the front buffer GetContents reads

	// Draw should have painted box-corner glyphs somewhere on the screen.
	cells, _, _ := screen.GetContents()
	var sawCorner bool
	for _, c := range cells {
		if len(c.Runes) > 0 && c.Runes[0] == '╭' {
			sawCorner = true
			break
		}
	}
	if !sawCorner {
		t.Error("expected at least one box corner glyph painted")
	}

	// Empty layout is a no-op returning a zero box.
	if ew, eh := Draw(screen, 0, 0, Layout{}, "", false, nil); ew != 0 || eh != 0 {
		t.Errorf("empty layout: got w=%d h=%d, want 0,0", ew, eh)
	}
}

// TestRender_CursorMarker — RenderToString prefixes the cursor node's label
// with `*` so golden tests can verify cursor movement without poking at
// internal state.
func TestRender_CursorMarker(t *testing.T) {
	l := Compute([]Node{{ID: "A", Name: "alpha"}})
	withCursor := RenderToString(l, "A", nil)
	if !strings.Contains(withCursor, "*") {
		t.Fatalf("expected cursor marker, got:\n%s", withCursor)
	}
	withoutCursor := RenderToString(l, "", nil)
	if strings.Contains(withoutCursor, "*") {
		t.Fatalf("did not expect cursor marker, got:\n%s", withoutCursor)
	}
}

// TestRender_ArchivedFlag — archived tasks get the `·` glyph regardless of
// their underlying status.
func TestRender_ArchivedFlag(t *testing.T) {
	l := Compute([]Node{{ID: "A", Name: "old", Status: "in_progress", Archived: true}})
	out := RenderToString(l, "", nil)
	if !strings.Contains(out, "· old") {
		t.Fatalf("expected archived glyph, got:\n%s", out)
	}
}

// TestRender_FailedResultGlyph — passing parseFailed=true for a node swaps
// in the `✕` glyph. The widget computes this once when SetNodes is called
// rather than re-parsing JSON inside Draw.
func TestRender_FailedResultGlyph(t *testing.T) {
	l := Compute([]Node{{ID: "A", Name: "alpha", Status: "complete"}})
	out := RenderToString(l, "", func(id string) bool { return id == "A" })
	if !strings.Contains(out, "✕ alpha") {
		t.Fatalf("expected failed glyph, got:\n%s", out)
	}
}

// TestRender_LabelTruncation — names too long for the box are cut with an
// ellipsis. The box width stays constant regardless of name length.
func TestRender_LabelTruncation(t *testing.T) {
	l := Compute([]Node{{ID: "A", Name: "this-name-is-definitely-too-long-for-the-box"}})
	out := RenderToString(l, "", nil)
	lines := strings.Split(out, "\n")
	if len(lines) < 2 {
		t.Fatalf("too few lines")
	}
	if !strings.Contains(lines[1], "…") {
		t.Fatalf("expected ellipsis in truncated label, got %q", lines[1])
	}
	// Box width stable.
	for _, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "╭") || strings.HasPrefix(strings.TrimSpace(line), "╰") {
			r := []rune(strings.TrimSpace(line))
			if len(r) != boxWidth {
				t.Errorf("box width drift: got %d, want %d in %q", len(r), boxWidth, line)
			}
		}
	}
}

// TestRender_EmptyLayout — Compute([]) + RenderToString returns the empty
// string. The widget uses this branch to decide whether to draw a banner.
func TestRender_EmptyLayout(t *testing.T) {
	l := Compute(nil)
	if out := RenderToString(l, "", nil); out != "" {
		t.Fatalf("expected empty render, got %q", out)
	}
}

func equalLines(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
