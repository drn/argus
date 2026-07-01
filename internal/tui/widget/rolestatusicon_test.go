package widget

import (
	"testing"

	"github.com/drn/argus/internal/testutil"
	"github.com/drn/argus/internal/tui/theme"
)

// TestRoleStatusIcon_Precedence pins the shared classifier's precedence +
// vocabulary (BUG-007): the single source of truth the rail and the plan view
// both consume. needs-input → active(spinner) → ready_to_close → failed(red ✕) →
// done → idle → live → default.
//
// needs-input outranks everything (BUG-A): a worker GENUINELY blocked on a user
// prompt RIGHT NOW is the one actionable thing — it must never be masked.
//
// active outranks the stale-able resting states ready_to_close / failed / done
// (BUG-F, the icon-precedence completion of BUG-C). Active is the HONEST,
// content-derived "producing output right now" signal (Live && SessionRunning &&
// !SessionIdle) — NOT a stale hera role-status/meta. A worker genuinely producing
// output is working again, so the spinner is the truer current state and must not
// be masked by the done-roll's ready_to_close stamp (or a stale done/failed
// role-status). When the worker goes idle again, IsActive drops false and the
// resting glyph (ready_to_close review / failed ✕ / done ✓) correctly returns.
// needs-input is content-aware upstream, so a ready_to_close worker merely idling
// at its done summary (no interactive affordance, not active) still renders the
// review glyph.
func TestRoleStatusIcon_Precedence(t *testing.T) {
	const frame = 0
	cases := []struct {
		name      string
		in        RoleStatusInputs
		wantGlyph rune
	}{
		{"needs-input wins over all", RoleStatusInputs{NeedsInput: true, Active: true, ReadyToClose: true, Failed: true, Done: true}, theme.IconNeedsInput},
		{"needs-input over active", RoleStatusInputs{NeedsInput: true, Active: true}, theme.IconNeedsInput},
		// BUG-F: active outranks the stale-able resting states. The KEY case a
		// reactivated ready_to_close worker hits — must show the spinner, not the
		// static review glyph.
		{"active over ready_to_close (BUG-F)", RoleStatusInputs{Active: true, ReadyToClose: true}, SpinnerFrame(frame)},
		{"active over failed", RoleStatusInputs{Active: true, Failed: true}, SpinnerFrame(frame)},
		{"active over done", RoleStatusInputs{Active: true, Done: true}, SpinnerFrame(frame)},
		{"active → spinner frame", RoleStatusInputs{Active: true}, SpinnerFrame(frame)},
		// Resting states (NOT active) rank among themselves exactly as before.
		{"ready_to_close over failed/done (resting)", RoleStatusInputs{ReadyToClose: true, Failed: true, Done: true}, theme.IconReview},
		{"failed over done (resting)", RoleStatusInputs{Failed: true, Done: true}, '✕'},
		{"done glyph (resting)", RoleStatusInputs{Done: true}, '✓'},
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
// a red ✕ distinct from the Done ✓, placed below NeedsInput + active(spinner)
// (BUG-F) and above Done in precedence.
func TestRoleStatusIcon_Failed(t *testing.T) {
	t.Run("failed renders red ✕", func(t *testing.T) {
		glyph, style := RoleStatusIcon(RoleStatusInputs{Failed: true}, false, 0)
		testutil.Equal(t, glyph, '✕')
		testutil.Equal(t, style, theme.StyleError)
	})

	t.Run("active beats failed (BUG-F)", func(t *testing.T) {
		// A live, running, producing worker that also carries a stale failed
		// role-status shows the spinner — active is the honest current state.
		glyph, _ := RoleStatusIcon(RoleStatusInputs{Failed: true, Active: true}, false, 0)
		testutil.Equal(t, glyph, SpinnerFrame(0))
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
