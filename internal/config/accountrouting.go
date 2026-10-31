package config

import (
	"log/slog"
	"sync"
)

// DefaultAccountThresholdPct is the per-window usage threshold "auto" account
// selection uses when [account_routing] leaves it unset or out of range.
const DefaultAccountThresholdPct = 90

// AccountRoutingConfig is the [account_routing] table: the usage thresholds
// "auto" account selection measures headroom against, one per Claude plan
// window. Zero means unset.
type AccountRoutingConfig struct {
	FiveHourThresholdPct int `toml:"five_hour_threshold_pct"`
	WeeklyThresholdPct   int `toml:"weekly_threshold_pct"`
}

// FiveHourThreshold returns the effective 5-hour window threshold.
func (r AccountRoutingConfig) FiveHourThreshold() int {
	return accountThreshold("five_hour_threshold_pct", r.FiveHourThresholdPct)
}

// WeeklyThreshold returns the effective weekly window threshold.
func (r AccountRoutingConfig) WeeklyThreshold() int {
	return accountThreshold("weekly_threshold_pct", r.WeeklyThresholdPct)
}

var warnedAccountThresholds sync.Map

func accountThreshold(key string, v int) int {
	if v == 0 {
		return DefaultAccountThresholdPct
	}
	if v < 1 || v > 100 {
		// Warn once per distinct bad value: Config() runs constantly.
		if _, seen := warnedAccountThresholds.LoadOrStore(struct {
			k string
			v int
		}{key, v}, struct{}{}); !seen {
			slog.Warn("argus config: account_routing threshold out of range, using default", "key", key, "value", v, "default", DefaultAccountThresholdPct)
		}
		return DefaultAccountThresholdPct
	}
	return v
}
