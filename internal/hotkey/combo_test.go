package hotkey

import (
	"testing"

	"github.com/drn/argus/internal/testutil"
)

func TestParseCombo(t *testing.T) {
	for _, tc := range []struct {
		name, in, want string
		wantErr        bool
	}{
		{"right option", "right_option", "right_option", false},
		{"alias right alt", "right_alt", "right_option", false},
		{"hyphen and case", " Right-Option ", "right_option", false},
		{"generic pair", "shift+ctrl", "ctrl+shift", false},
		{"aliases", "control+alt+command", "ctrl+option+cmd", false},
		{"fn", "fn", "fn", false},
		{"left sides", "left_ctrl+left_shift+left_command+left_option", "left_ctrl+left_option+left_shift+left_cmd", false},
		{"right sides", "right_ctrl+right_shift+right_cmd", "right_ctrl+right_shift+right_cmd", false},
		{"both sides", "left_option+right_option", "left_option+right_option", false},
		{"generic plus sided", "option+right_option", "right_option", false},
		{"duplicate", "cmd+cmd", "cmd", false},
		{"empty", "", "", true},
		{"blank", "   ", "", true},
		{"empty token", "ctrl+", "", true},
		{"unknown", "right_option+a", "", true},
		{"sided fn unsupported", "right_fn", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, err := ParseCombo(tc.in)
			if tc.wantErr {
				testutil.Error(t, err)
				testutil.True(t, c.IsZero())
				return
			}
			testutil.NoError(t, err)
			testutil.Equal(t, c.String(), tc.want)
			rt, err := ParseCombo(c.String())
			testutil.NoError(t, err)
			testutil.Equal(t, rt, c)
		})
	}
}

func TestParseCombo_EmptyIsErrEmptyCombo(t *testing.T) {
	_, err := ParseCombo("")
	testutil.ErrorIs(t, err, ErrEmptyCombo)
}

func mustCombo(t *testing.T, s string) Combo {
	t.Helper()
	c, err := ParseCombo(s)
	testutil.NoError(t, err)
	return c
}

func TestComboMatches(t *testing.T) {
	const capsLock uint64 = 0x10000
	const nonMod uint64 = 0x100 // kCGEventFlagMaskNonCoalesced
	rOpt := FlagOption | FlagRightOption
	lOpt := FlagOption | FlagLeftOption
	for _, tc := range []struct {
		name    string
		combo   string
		flags   uint64
		matches bool
		held    bool
	}{
		{"right option exact", "right_option", rOpt, true, true},
		{"right option with caps lock and noise", "right_option", rOpt | capsLock | nonMod, true, true},
		{"left option is not right option", "right_option", lOpt, false, false},
		{"both options rejected for one side", "right_option", rOpt | FlagLeftOption, false, true},
		{"right option plus shift", "right_option", rOpt | FlagShift | FlagLeftShift, false, true},
		{"generic bit without device bit", "right_option", FlagOption, false, false},
		{"device bit without generic bit", "right_option", FlagRightOption, false, false},
		{"nothing down", "right_option", 0, false, false},
		{"generic option either side left", "option", lOpt, true, true},
		{"generic option either side right", "option", rOpt, true, true},
		{"generic option both sides", "option", rOpt | FlagLeftOption, true, true},
		{"ctrl+shift exact", "ctrl+shift", FlagControl | FlagLeftControl | FlagShift | FlagRightShift, true, true},
		{"ctrl+shift missing shift", "ctrl+shift", FlagControl | FlagLeftControl, false, false},
		{"ctrl+shift plus cmd", "ctrl+shift", FlagControl | FlagShift | FlagCommand, false, true},
		{"right cmd", "right_cmd", FlagCommand | FlagRightCommand, true, true},
		{"left cmd", "left_cmd", FlagCommand | FlagLeftCommand, true, true},
		{"right ctrl", "right_ctrl", FlagControl | FlagRightControl, true, true},
		{"left ctrl", "left_ctrl", FlagControl | FlagLeftControl, true, true},
		{"left shift", "left_shift", FlagShift | FlagLeftShift, true, true},
		{"right shift", "right_shift", FlagShift | FlagRightShift, true, true},
		{"fn", "fn", FlagFn, true, true},
		{"fn plus option", "fn", FlagFn | lOpt, false, true},
		{"both option sides combo", "left_option+right_option", rOpt | FlagLeftOption, true, true},
		{"both option sides only one down", "left_option+right_option", rOpt, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := mustCombo(t, tc.combo)
			testutil.Equal(t, c.Matches(tc.flags), tc.matches)
			testutil.Equal(t, c.Held(tc.flags), tc.held)
		})
	}
}

func TestComboZeroMatchesNothing(t *testing.T) {
	var c Combo
	testutil.True(t, c.IsZero())
	testutil.False(t, c.Matches(0))
	testutil.False(t, c.Held(0))
	testutil.Equal(t, c.String(), "")
}
