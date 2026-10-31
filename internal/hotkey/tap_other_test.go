//go:build !darwin

package hotkey

import (
	"testing"

	"github.com/drn/argus/internal/testutil"
)

func TestTapOther_Unsupported(t *testing.T) {
	c, err := ParseCombo("right_option")
	testutil.NoError(t, err)
	tap, err := StartTap(c, nil)
	testutil.ErrorIs(t, err, ErrUnsupported)
	testutil.True(t, tap == nil)
	tap.Close()
	testutil.False(t, InputMonitoringGranted())
	testutil.False(t, RequestInputMonitoring())
}
