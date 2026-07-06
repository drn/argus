package profiles

import (
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"

	"github.com/drn/argus/internal/testutil"
)

// TestNoReviewImport asserts internal/profiles never imports internal/review:
// the [panel] composition grammar is owned by the sibling cross-vendor-review
// capability and reaches this package only via an injected validator func
// (mirroring the knownModels injection), never a direct dependency
// (D-PANEL-SEAM). This is a static, parse-only check — it does not require
// internal/review to exist or compile.
func TestNoReviewImport(t *testing.T) {
	entries, err := os.ReadDir(".")
	testutil.NoError(t, err)

	fset := token.NewFileSet()
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, parser.ImportsOnly)
		testutil.NoError(t, err)
		for _, imp := range f.Imports {
			path := strings.Trim(imp.Path.Value, `"`)
			if strings.Contains(path, "/internal/review") {
				t.Fatalf("%s imports %q: internal/profiles must never import internal/review (panel grammar is injected, not depended on)", name, path)
			}
		}
	}
}
