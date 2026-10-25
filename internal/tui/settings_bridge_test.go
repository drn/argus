package tui

import (
	"testing"

	"github.com/drn/argus/internal/testutil"
)

func TestSettingsBridgeToggle(t *testing.T) {
	for _, key := range []string{"bridge.enabled", "bridge.delivery", "bridge.band", "bridge.toasts"} {
		t.Run(key, func(t *testing.T) {
			sv := testSettingsView(t)
			selectRowInCategory(t, sv, catBridge, srBridge, key)
			testutil.True(t, sv.handleEnter())
			sv.Refresh()
			cfg := sv.database.Config()
			switch key {
			case "bridge.enabled":
				testutil.False(t, cfg.Bridge.Enabled)
				testutil.True(t, cfg.Bridge.Delivery)
			case "bridge.delivery":
				testutil.False(t, cfg.Bridge.Delivery)
				testutil.True(t, cfg.Bridge.Enabled)
			case "bridge.band":
				testutil.False(t, cfg.Bridge.Band)
				testutil.True(t, cfg.Bridge.Toasts)
			default:
				testutil.False(t, cfg.Bridge.Toasts)
				testutil.True(t, cfg.Bridge.Band)
			}
			selectRowInCategory(t, sv, catBridge, srBridge, key)
			testutil.Contains(t, sv.SelectedRow().label, "OFF")
			testutil.True(t, sv.handleEnter())
			sv.Refresh()
			testutil.True(t, sv.database.Config().Bridge.Enabled)
			testutil.True(t, sv.database.Config().Bridge.Delivery)
			testutil.True(t, sv.database.Config().Bridge.Band)
			testutil.True(t, sv.database.Config().Bridge.Toasts)
		})
	}
}
