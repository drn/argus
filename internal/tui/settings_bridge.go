package tui

import (
	"strconv"

	"github.com/drn/argus/internal/uxlog"
)

// Keyed rows let later bridge controls extend this section without new keys.
func (sv *SettingsView) bridgeRows() []settingsRow {
	cfg := sv.database.Config().Bridge
	var rows []settingsRow
	for _, item := range []struct {
		key, label string
		enabled    bool
	}{
		{"bridge.enabled", "Enabled", cfg.Enabled},
		{"bridge.delivery", "Message delivery", cfg.Delivery},
	} {
		state := "OFF"
		if item.enabled {
			state = "ON"
		}
		rows = append(rows, settingsRow{kind: srBridge, key: item.key, label: item.label + ": " + state})
	}
	return rows
}

func (sv *SettingsView) toggleBridge(key string) bool {
	cfg := sv.database.Config().Bridge
	value := cfg.Enabled
	if key == "bridge.delivery" {
		value = cfg.Delivery
	}
	if err := sv.database.SetConfigValue(key, strconv.FormatBool(!value)); err != nil {
		uxlog.Log("[bridge] settings persist failed key=%s error=%v", key, err)
		return true
	}
	uxlog.Log("[bridge] settings toggled key=%s value=%t", key, !value)
	sv.rebuildRows()
	return true
}
