package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCountLines(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want int
	}{
		{"empty", "", 0},
		{"one line, no trailing newline", "a", 1},
		{"two lines, trailing newline", "a\nb\n", 2},
		{"blank lines count", "\n\n\n", 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := countLines(strings.NewReader(tt.in)); got != tt.want {
				t.Fatalf("countLines(%q) = %d, want %d", tt.in, got, tt.want)
			}
		})
	}
}

func writeLines(t *testing.T, dir, name string, n int) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(strings.Repeat("x\n", n)), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestCheck(t *testing.T) {
	dir := t.TempDir()
	under := writeLines(t, dir, "under.md", 10)
	over := writeLines(t, dir, "over.md", 20)

	t.Run("under cap passes", func(t *testing.T) {
		results, ok := check([]guard{{path: under, max: 15}})
		if !ok {
			t.Fatal("expected ok")
		}
		if results[0].over() {
			t.Fatal("expected not over cap")
		}
		if results[0].lines != 10 {
			t.Fatalf("lines = %d, want 10", results[0].lines)
		}
	})

	t.Run("over cap fails", func(t *testing.T) {
		results, ok := check([]guard{{path: over, max: 15}})
		if ok {
			t.Fatal("expected not ok")
		}
		if !results[0].over() {
			t.Fatal("expected over cap")
		}
	})

	t.Run("at cap passes", func(t *testing.T) {
		if _, ok := check([]guard{{path: over, max: 20}}); !ok {
			t.Fatal("expected exactly-at-cap to pass")
		}
	})

	t.Run("missing file fails", func(t *testing.T) {
		results, ok := check([]guard{{path: filepath.Join(dir, "nope.md"), max: 15}})
		if ok {
			t.Fatal("expected not ok for missing file")
		}
		if results[0].err == nil {
			t.Fatal("expected an error on the result")
		}
	})
}

// The real policy table must stay sane: every configured doc needs a positive
// cap and must actually exist relative to the repo root.
func TestRealGuards(t *testing.T) {
	if len(guards) == 0 {
		t.Fatal("no guards configured")
	}
	for _, g := range guards {
		if g.max <= 0 {
			t.Errorf("guard %q has non-positive cap %d", g.path, g.max)
		}
		if _, err := os.Stat(filepath.Join("..", "..", g.path)); err != nil {
			t.Errorf("guarded doc %q not found from repo root: %v", g.path, err)
		}
	}
}

func TestReport(t *testing.T) {
	var sb strings.Builder
	report(&sb, []result{
		{guard: guard{path: "ok.md", max: 10}, lines: 5},
		{guard: guard{path: "big.md", max: 10}, lines: 20},
		{guard: guard{path: "gone.md", max: 10}, err: os.ErrNotExist},
	})
	out := sb.String()
	for _, want := range []string{"ok", "OVER", "ERR"} {
		if !strings.Contains(out, want) {
			t.Errorf("report missing %q:\n%s", want, out)
		}
	}
}
