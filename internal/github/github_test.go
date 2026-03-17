package github

import (
	"strings"
	"testing"
)

func TestExtractFileDiff(t *testing.T) {
	fullDiff := `diff --git a/foo/bar.go b/foo/bar.go
index abc..def 100644
--- a/foo/bar.go
+++ b/foo/bar.go
@@ -1,3 +1,4 @@
 package foo
+// added
 func Foo() {}
diff --git a/other.go b/other.go
index 111..222 100644
--- a/other.go
+++ b/other.go
@@ -1 +1 @@
-old
+new`

	got := ExtractFileDiff(fullDiff, "foo/bar.go")
	if !strings.Contains(got, "+++ b/foo/bar.go") {
		t.Errorf("expected diff header for foo/bar.go, got: %q", got)
	}
	if strings.Contains(got, "other.go") {
		t.Errorf("expected no other.go content, got: %q", got)
	}
}

func TestExtractFileDiff_NotFound(t *testing.T) {
	fullDiff := `diff --git a/foo.go b/foo.go
--- a/foo.go
+++ b/foo.go
@@ -1 +1 @@
-old
+new`

	got := ExtractFileDiff(fullDiff, "nonexistent.go")
	if got != "" {
		t.Errorf("expected empty string for missing file, got: %q", got)
	}
}

func TestExtractFileDiff_Empty(t *testing.T) {
	got := ExtractFileDiff("", "foo.go")
	if got != "" {
		t.Errorf("expected empty string for empty diff, got: %q", got)
	}
}

func TestRunGhError(t *testing.T) {
	// runGh with a nonexistent subcommand should return an error
	_, err := runGh("__nonexistent_subcommand__")
	if err == nil {
		t.Error("expected error from nonexistent gh subcommand, got nil")
	}
}
