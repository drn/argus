package gitutil

import "testing"

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
