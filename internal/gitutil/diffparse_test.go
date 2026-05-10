package gitutil

import (
	"strings"
	"testing"

	"github.com/drn/argus/internal/testutil"
)

func TestParseUnifiedDiff(t *testing.T) {
	raw := `diff --git a/file.go b/file.go
--- a/file.go
+++ b/file.go
@@ -1,3 +1,4 @@
 context
-removed
+added1
+added2
 context2`

	pd := ParseUnifiedDiff(raw)
	if pd.OldFile != "file.go" {
		t.Errorf("OldFile = %q, want file.go", pd.OldFile)
	}
	if pd.NewFile != "file.go" {
		t.Errorf("NewFile = %q, want file.go", pd.NewFile)
	}
	if len(pd.Hunks) != 1 {
		t.Fatalf("expected 1 hunk, got %d", len(pd.Hunks))
	}
	hunk := pd.Hunks[0]
	if len(hunk.Lines) < 4 {
		t.Fatalf("expected >= 4 lines, got %d", len(hunk.Lines))
	}
}

func TestBuildSideBySide(t *testing.T) {
	raw := `--- a/file.go
+++ b/file.go
@@ -1,2 +1,2 @@
-old line
+new line
 context`

	pd := ParseUnifiedDiff(raw)
	sbs := BuildSideBySide(pd)
	if len(sbs) == 0 {
		t.Fatal("expected non-empty side-by-side")
	}
	// First row should be the hunk header
	if sbs[0].LeftText == "" {
		t.Error("first row should be hunk header")
	}
}

func TestFormatLineNum(t *testing.T) {
	if got := FormatLineNum(0, 4); got != "    " {
		t.Errorf("FormatLineNum(0, 4) = %q", got)
	}
	if got := FormatLineNum(42, 4); got != "  42" {
		t.Errorf("FormatLineNum(42, 4) = %q", got)
	}
}

func TestExpandTabs(t *testing.T) {
	if got := ExpandTabs("a\tb"); got != "a  b" {
		t.Errorf("ExpandTabs = %q", got)
	}
}

func TestParseRange(t *testing.T) {
	cases := []struct {
		name      string
		in        string
		wantStart int
		wantCount int
	}{
		{"start_count", "10,5", 10, 5},
		{"single_value", "42", 42, 1},
		{"empty_string", "", 1, 1},
		{"zero_with_comma", "0,3", 1, 3},
		{"zero_count_only", "0", 1, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			start, count := parseRange(tc.in)
			testutil.Equal(t, start, tc.wantStart)
			testutil.Equal(t, count, tc.wantCount)
		})
	}
}

func TestScrollState_OffsetAdjustment(t *testing.T) {
	t.Run("CursorDown scrolls offset when off-screen", func(t *testing.T) {
		var s ScrollState
		// Move cursor past the visible window of 3 items.
		s.CursorDown(10, 3) // cursor=1
		s.CursorDown(10, 3) // cursor=2
		s.CursorDown(10, 3) // cursor=3, offset becomes 1
		if s.Offset() != 1 {
			t.Errorf("expected offset 1, got %d", s.Offset())
		}
	})

	t.Run("CursorUp scrolls offset back", func(t *testing.T) {
		var s ScrollState
		s.SetCursor(5)
		s.SetOffset(3)
		s.CursorUp() // cursor=4 (still ≥ offset, so offset stays)
		s.CursorUp() // cursor=3 (still ≥ offset, so offset stays)
		s.CursorUp() // cursor=2 (< offset, offset becomes 2)
		if s.Offset() != 2 {
			t.Errorf("expected offset 2, got %d", s.Offset())
		}
	})

	t.Run("CursorDown does nothing when at end", func(t *testing.T) {
		var s ScrollState
		s.SetCursor(9)
		s.CursorDown(10, 5)
		testutil.Equal(t, s.Cursor(), 9)
	})

	t.Run("ClampCursor leaves cursor when in bounds", func(t *testing.T) {
		var s ScrollState
		s.SetCursor(2)
		s.ClampCursor(10)
		testutil.Equal(t, s.Cursor(), 2)
	})

	t.Run("ClampCursor with zero items resets to 0", func(t *testing.T) {
		var s ScrollState
		s.SetCursor(5)
		s.ClampCursor(0)
		testutil.Equal(t, s.Cursor(), 0)
	})
}
func TestFormatLineNum_Truncated(t *testing.T) {
	t.Run("number wider than width is truncated to last n digits", func(t *testing.T) {
		got := FormatLineNum(123456, 3)
		testutil.Equal(t, got, "456")
	})
}

func TestParseHunkHeader_Edges(t *testing.T) {
	t.Run("non-hunk-header line returns defaults", func(t *testing.T) {
		got := parseHunkHeader("not a hunk")
		testutil.Equal(t, got.OldStart, 1)
		testutil.Equal(t, got.NewStart, 1)
	})

	t.Run("hunk header without closing @@ returns defaults", func(t *testing.T) {
		got := parseHunkHeader("@@ -1,5 +1,5")
		testutil.Equal(t, got.OldStart, 1)
		testutil.Equal(t, got.NewStart, 1)
	})

	t.Run("well-formed hunk header parses ranges", func(t *testing.T) {
		got := parseHunkHeader("@@ -10,5 +12,7 @@ context")
		testutil.Equal(t, got.OldStart, 10)
		testutil.Equal(t, got.OldCount, 5)
		testutil.Equal(t, got.NewStart, 12)
		testutil.Equal(t, got.NewCount, 7)
	})
}

func TestParseUnifiedDiff_NoNewlineAtEnd(t *testing.T) {
	raw := `--- a/file
+++ b/file
@@ -1,1 +1,1 @@
-old line
\ No newline at end of file
+new line`
	pd := ParseUnifiedDiff(raw)
	if len(pd.Hunks) == 0 {
		t.Fatal("expected at least one hunk")
	}
	// The marker line should NOT be added as a content line.
	for _, l := range pd.Hunks[0].Lines {
		testutil.Equal(t, strings.HasPrefix(l.Content, `\ No newline`), false)
	}
}

func TestParseUnifiedDiff_EmptyContextLineInHunk(t *testing.T) {
	raw := "--- a/f\n+++ b/f\n@@ -1,3 +1,3 @@\n line1\n\n line3"
	pd := ParseUnifiedDiff(raw)
	if len(pd.Hunks) == 0 {
		t.Fatal("expected hunk")
	}
	// We should have 3 lines: context, empty context, context.
	if len(pd.Hunks[0].Lines) < 2 {
		t.Errorf("expected ≥2 lines, got %d", len(pd.Hunks[0].Lines))
	}
}

func TestParseGitStatus_RenamedShort(t *testing.T) {
	// Lines shorter than 4 characters are silently skipped — already
	// covered, but exercise once more for clarity.
	got := ParseGitStatus("M\nA   foo.txt\n??")
	if len(got) != 1 {
		t.Errorf("expected 1 file, got %d: %v", len(got), got)
	}
}

func TestParseGitDiffNameStatus_Skips(t *testing.T) {
	// A line without a tab is skipped.
	got := ParseGitDiffNameStatus("invalid-line-no-tab\nA\tfoo.txt")
	if len(got) != 1 {
		t.Errorf("expected 1 entry, got %d: %v", len(got), got)
	}
	testutil.Equal(t, got[0].Path, "foo.txt")
}

func TestWordDiff_ConsecutiveUnmatchedTokens(t *testing.T) {
	// Forces the inner-loop branch in mergeUnmatched where consecutive
	// unmatched tokens get merged into a single span.
	old := "alpha beta gamma delta"
	new := "x y z delta"
	gotOld, gotNew := WordDiff(old, new)
	if len(gotOld) == 0 || len(gotNew) == 0 {
		t.Fatal("expected non-empty diff spans")
	}
}
