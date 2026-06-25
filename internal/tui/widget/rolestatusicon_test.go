package widget

import (
	"testing"

	"github.com/drn/argus/internal/testutil"
	"github.com/drn/argus/internal/tui/theme"
)

// TestRoleStatusIcon_Precedence pins the shared classifier's precedence +
// vocabulary (BUG-007): the single source of truth the rail and the plan view
// both consume. ready_to_close → needs-input → failed(red ✕) → done → active →
// idle → live → default.
func TestRoleStatusIcon_Precedence(t *testing.T) {
	const frame = 0
	cases := []struct {
		name      string
		in        RoleStatusInputs
		wantGlyph rune
	}{
		{"ready_to_close wins over all", RoleStatusInputs{ReadyToClose: true, NeedsInput: true, Failed: true, Done: true, Active: true}, theme.IconReview},
		{"needs-input over failed/done/active", RoleStatusInputs{NeedsInput: true, Failed: true, Done: true, Active: true}, theme.IconNeedsInput},
		{"failed over done/active", RoleStatusInputs{Failed: true, Done: true, Active: true}, '✕'},
		{"done over active", RoleStatusInputs{Done: true, Active: true}, '✓'},
		{"active → spinner frame", RoleStatusInputs{Active: true}, SpinnerFrame(frame)},
		{"idle moon-outline", RoleStatusInputs{Idle: true, Live: true}, theme.IconMoonOutline},
		{"live-quiet moon-stars", RoleStatusInputs{Live: true}, theme.IconMoonStars},
		{"default moon-outline", RoleStatusInputs{}, theme.IconMoonOutline},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			glyph, _ := RoleStatusIcon(tc.in, false, frame)
			testutil.Equal(t, glyph, tc.wantGlyph)
		})
	}
}

// TestRoleStatusIcon_Failed pins the D2 (make-hera-plan-living) failed glyph:
// a red ✕ distinct from the Done ✓, placed below NeedsInput and above Done in
// precedence.
func TestRoleStatusIcon_Failed(t *testing.T) {
	t.Run("failed renders red ✕", func(t *testing.T) {
		glyph, style := RoleStatusIcon(RoleStatusInputs{Failed: true}, false, 0)
		testutil.Equal(t, glyph, '✕')
		testutil.Equal(t, style, theme.StyleError)
	})

	t.Run("failed is distinct from done ✓", func(t *testing.T) {
		failedGlyph, _ := RoleStatusIcon(RoleStatusInputs{Failed: true}, false, 0)
		doneGlyph, _ := RoleStatusIcon(RoleStatusInputs{Done: true}, false, 0)
		if failedGlyph == doneGlyph {
			t.Fatalf("failed glyph %q must differ from done glyph %q", failedGlyph, doneGlyph)
		}
	})

	t.Run("needs-input beats failed", func(t *testing.T) {
		glyph, _ := RoleStatusIcon(RoleStatusInputs{NeedsInput: true, Failed: true}, false, 0)
		testutil.Equal(t, glyph, theme.IconNeedsInput)
	})

	t.Run("ready_to_close beats failed", func(t *testing.T) {
		glyph, _ := RoleStatusIcon(RoleStatusInputs{ReadyToClose: true, Failed: true}, false, 0)
		testutil.Equal(t, glyph, theme.IconReview)
	})

	t.Run("failed beats done", func(t *testing.T) {
		glyph, _ := RoleStatusIcon(RoleStatusInputs{Failed: true, Done: true}, false, 0)
		testutil.Equal(t, glyph, '✕')
	})

	t.Run("dim forces dimmed style even for failed", func(t *testing.T) {
		_, style := RoleStatusIcon(RoleStatusInputs{Failed: true}, true, 0)
		testutil.Equal(t, style, theme.StyleDimmed)
	})
}

// TestRoleStatusIcon_DimForcesDimStyle: archived placement forces the dimmed
// style (the glyph never lies — only the style dims).
func TestRoleStatusIcon_DimForcesDimStyle(t *testing.T) {
	_, style := RoleStatusIcon(RoleStatusInputs{Done: true}, true, 0)
	testutil.Equal(t, style, theme.StyleDimmed)
	// ready_to_close also dims.
	_, rtc := RoleStatusIcon(RoleStatusInputs{ReadyToClose: true}, true, 0)
	testutil.Equal(t, rtc, theme.StyleDimmed)
}
