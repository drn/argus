package theme

import (
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

func TestPRGlyph(t *testing.T) {
	cases := []struct {
		name      string
		state     model.PRState
		wantRune  rune
		wantStyle tcell.Style
		wantOK    bool
	}{
		{"awaiting-review", model.PRAwaitingReview, IconPRAwaiting, StylePRAwaiting, true},
		{"changes-requested", model.PRChangesRequested, IconPRChanges, StylePRChanges, true},
		{"approved", model.PRApproved, IconPRApproved, StylePRApproved, true},
		{"none-blank", model.PRNone, ' ', tcell.StyleDefault, false},
		{"draft-blank", model.PRDraft, ' ', tcell.StyleDefault, false},
		{"merged-closed-blank", model.PRMergedClosed, ' ', tcell.StyleDefault, false},
		{"unknown-blank", model.PRUnknown, ' ', tcell.StyleDefault, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotRune, gotStyle, gotOK := PRGlyph(tc.state)
			testutil.Equal(t, gotRune, tc.wantRune)
			testutil.Equal(t, gotStyle, tc.wantStyle)
			testutil.Equal(t, gotOK, tc.wantOK)
		})
	}
}

// TestPRGlyph_ActionableGlyphsDistinct guards against accidentally assigning
// the same codepoint to two actionable states (which would make them
// indistinguishable in the task row).
func TestPRGlyph_ActionableGlyphsDistinct(t *testing.T) {
	seen := map[rune]model.PRState{}
	for _, s := range []model.PRState{model.PRAwaitingReview, model.PRChangesRequested, model.PRApproved} {
		r, _, _ := PRGlyph(s)
		if r == ' ' {
			t.Fatalf("actionable state %v rendered blank", s)
		}
		if prev, ok := seen[r]; ok {
			t.Fatalf("states %v and %v share glyph %#U", prev, s, r)
		}
		seen[r] = s
	}
}
