package agent

import (
	"testing"

	"github.com/drn/argus/internal/testutil"
)

func TestShouldKickRerender(t *testing.T) {
	cases := []struct {
		name           string
		hasSessionID   bool
		initCols       int
		panelCols      int
		idle           bool
		alreadyPending bool
		want           RerenderDecision
	}{
		{
			// The original repro: session started narrow (e.g. tview default
			// 15-col layout caught before agent-pane laid out), now viewing
			// in a 190-col TUI pane.
			name:         "kick when narrow start, wide panel, idle",
			hasSessionID: true, initCols: 20, panelCols: 190, idle: true,
			want: RerenderKick,
		},
		{
			// Symmetric case: TUI committed at 200, user switched to a
			// phone view at 80. The phone's resize handler should kick so
			// scrollback re-renders narrow for the phone.
			name:         "kick when wide start, narrow panel, idle",
			hasSessionID: true, initCols: 200, panelCols: 80, idle: true,
			want: RerenderKick,
		},
		{
			// Don't kill mid-tool-call. Wait for the agent to go idle.
			name:         "defer when busy (narrow → wide)",
			hasSessionID: true, initCols: 20, panelCols: 190, idle: false,
			want: RerenderDeferBusy,
		},
		{
			// Same gate applies to the wide → narrow direction.
			name:         "defer when busy (wide → narrow)",
			hasSessionID: true, initCols: 200, panelCols: 80, idle: false,
			want: RerenderDeferBusy,
		},
		{
			// Codex and other backends without --session-id resume can't
			// re-flow on restart, so don't bother killing them.
			name:         "skip when no SessionID",
			hasSessionID: false, initCols: 20, panelCols: 190, idle: true,
			want: RerenderSkip,
		},
		{
			// Once we've stopped a session, don't queue another stop on top.
			name:         "skip when already pending",
			hasSessionID: true, initCols: 20, panelCols: 190, idle: true, alreadyPending: true,
			want: RerenderSkip,
		},
		{
			// Old daemon without InitialPTYSize support reports 0 — treat
			// as unknown rather than triggering surprise restarts.
			name:         "skip when initCols unknown (0)",
			hasSessionID: true, initCols: 0, panelCols: 190, idle: true,
			want: RerenderSkip,
		},
		{
			// Margin guard: panel only barely wider than init. Disrupting
			// the agent isn't worth a tiny gain.
			name:         "skip when delta too thin (widening)",
			hasSessionID: true, initCols: 50, panelCols: 70, idle: true,
			want: RerenderSkip,
		},
		{
			// Same margin guard in the narrowing direction.
			name:         "skip when delta too thin (narrowing)",
			hasSessionID: true, initCols: 100, panelCols: 80, idle: true,
			want: RerenderSkip,
		},
		{
			name:         "kick at exact margin floor (widening)",
			hasSessionID: true, initCols: 50, panelCols: 80, idle: true,
			want: RerenderKick,
		},
		{
			name:         "kick at exact margin floor (narrowing)",
			hasSessionID: true, initCols: 110, panelCols: 80, idle: true,
			want: RerenderKick,
		},
		{
			// Identical width — nothing to do.
			name:         "skip when widths match",
			hasSessionID: true, initCols: 120, panelCols: 120, idle: true,
			want: RerenderSkip,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ShouldKickRerender(tc.hasSessionID, tc.initCols, tc.panelCols, tc.idle, tc.alreadyPending)
			testutil.Equal(t, got, tc.want)
		})
	}
}

func TestMarginExceedsRerenderThreshold(t *testing.T) {
	// The cheap-gate helper used by the API resize handler before the SQLite
	// task lookup. Pure margin check: no idle/sessionID/pending state.
	cases := []struct {
		name      string
		initCols  int
		panelCols int
		want      bool
	}{
		{"unknown init treated as sane", 0, 200, false},
		{"negative init treated as sane", -1, 200, false},
		{"identical widths", 120, 120, false},
		{"sub-margin widening", 100, 120, false},
		{"sub-margin narrowing", 120, 100, false},
		{"exact margin widening", 50, 80, true},
		{"exact margin narrowing", 110, 80, true},
		{"large widening", 20, 200, true},
		{"large narrowing", 200, 60, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := MarginExceedsRerenderThreshold(tc.initCols, tc.panelCols)
			testutil.Equal(t, got, tc.want)
		})
	}
}
