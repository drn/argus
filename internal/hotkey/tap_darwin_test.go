//go:build darwin

package hotkey

import (
	"testing"

	"github.com/drn/argus/internal/testutil"
)

func TestInputMonitoringGranted_NoPanic(t *testing.T) {
	if testing.Short() {
		t.Skip("loads CoreGraphics; skipped in -short")
	}
	_ = InputMonitoringGranted()
}

func TestStartTap_ZeroCombo(t *testing.T) {
	tap, err := StartTap(Combo{}, nil)
	testutil.ErrorIs(t, err, ErrEmptyCombo)
	testutil.True(t, tap == nil)
}

func TestTapClose_Nil(t *testing.T) {
	var tap *Tap
	tap.Close()
}
