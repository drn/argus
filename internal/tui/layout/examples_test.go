package layout

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/drn/argus/internal/testutil"
)

// TestExamples_DocsLayoutsParseAndValidate guards docs/examples/layouts/*.json
// against drift. Every example must round-trip cleanly through Parse so the
// schema docs stay executable.
func TestExamples_DocsLayoutsParseAndValidate(t *testing.T) {
	_, thisFile, _, _ := runtime.Caller(0)
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..", "..")
	dir := filepath.Join(repoRoot, "docs", "examples", "layouts")

	entries, err := os.ReadDir(dir)
	testutil.NoError(t, err)
	if len(entries) == 0 {
		t.Fatalf("expected example layouts under %s", dir)
	}

	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		t.Run(e.Name(), func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join(dir, e.Name())) //nolint:gosec // test reads bundled examples
			testutil.NoError(t, err)
			l, err := Parse(data)
			testutil.NoError(t, err)
			if l.Name == "" {
				t.Errorf("example %s has empty name", e.Name())
			}
			if l.Title == "" {
				t.Errorf("example %s has empty title", e.Name())
			}
		})
	}
}
