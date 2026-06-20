package widget

import (
	"testing"

	"github.com/drn/argus/internal/testutil"
	"github.com/drn/argus/internal/tui/theme"
)

// TestRoleStatusIcon_Precedence pins the shared classifier's precedence +
// vocabulary (BUG-007): the single source of truth the rail and the plan view
// both consume. ready_to_close → needs-input → done → active → idle → live →
// default.
func TestRoleStatusIcon_Precedence(t *testing.T) {
	const frame = 0
	cases := []struct {
		name      string
		in        RoleStatusInputs
		wantGlyph rune
	}{
		{"ready_to_close wins over all", RoleStatusInputs{ReadyToClose: true, NeedsInput: true, Done: true, Active: true}, theme.IconReview},
		{"needs-input over done/active", RoleStatusInputs{NeedsInput: true, Done: true, Active: true}, theme.IconNeedsInput},
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

// TestRoleStatusIcon_DimForcesDimStyle: archived placement forces the dimmed
// style (the glyph never lies — only the style dims).
func TestRoleStatusIcon_DimForcesDimStyle(t *testing.T) {
	_, style := RoleStatusIcon(RoleStatusInputs{Done: true}, true, 0)
	testutil.Equal(t, style, theme.StyleDimmed)
	// ready_to_close also dims.
	_, rtc := RoleStatusIcon(RoleStatusInputs{ReadyToClose: true}, true, 0)
	testutil.Equal(t, rtc, theme.StyleDimmed)
}
