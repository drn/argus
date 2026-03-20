package gitutil

import (
	"testing"
)

func TestWordDiff(t *testing.T) {
	tests := []struct {
		name     string
		old      string
		new      string
		wantOld  []DiffSpan
		wantNew  []DiffSpan
	}{
		{
			name:    "identical lines",
			old:     "hello world",
			new:     "hello world",
			wantOld: nil,
			wantNew: nil,
		},
		{
			name:    "single word change",
			old:     "hello world",
			new:     "hello earth",
			wantOld: []DiffSpan{{Start: 6, End: 11}},
			wantNew: []DiffSpan{{Start: 6, End: 11}},
		},
		{
			name:    "prefix change",
			old:     "foo bar baz",
			new:     "qux bar baz",
			wantOld: []DiffSpan{{Start: 0, End: 3}},
			wantNew: []DiffSpan{{Start: 0, End: 3}},
		},
		{
			name:    "completely different",
			old:     "abc",
			new:     "xyz",
			wantOld: []DiffSpan{{Start: 0, End: 3}},
			wantNew: []DiffSpan{{Start: 0, End: 3}},
		},
		{
			name:    "empty old",
			old:     "",
			new:     "hello",
			wantOld: nil,
			wantNew: []DiffSpan{{Start: 0, End: 5}},
		},
		{
			name:    "variable rename",
			old:     "  x := getValue()",
			new:     "  result := getValue()",
			wantOld: []DiffSpan{{Start: 2, End: 3}},
			wantNew: []DiffSpan{{Start: 2, End: 8}},
		},
		{
			name:    "multiple changes",
			old:     "func foo(a int) string",
			new:     "func bar(a int) error",
			wantOld: []DiffSpan{{Start: 5, End: 8}, {Start: 16, End: 22}},
			wantNew: []DiffSpan{{Start: 5, End: 8}, {Start: 16, End: 21}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotOld, gotNew := WordDiff(tt.old, tt.new)
			if !spansEqual(gotOld, tt.wantOld) {
				t.Errorf("old spans = %v, want %v", gotOld, tt.wantOld)
			}
			if !spansEqual(gotNew, tt.wantNew) {
				t.Errorf("new spans = %v, want %v", gotNew, tt.wantNew)
			}
		})
	}
}

func TestWordDiffMultipleChanges(t *testing.T) {
	old := "func foo(a int) string"
	new := "func bar(b int) error"
	gotOld, gotNew := WordDiff(old, new)

	// foo→bar and string→error should be highlighted
	// a→b too
	if len(gotOld) == 0 {
		t.Fatal("expected old spans")
	}
	if len(gotNew) == 0 {
		t.Fatal("expected new spans")
	}
}

func spansEqual(a, b []DiffSpan) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
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
