package agent

import (
	"fmt"
	"strings"
	"sync"

	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/usagebudget"
)

var accountUsageSource struct {
	sync.RWMutex
	read func(cfg config.Config) map[string]usagebudget.Reading
}

// SetAccountUsageSource replaces where per-account Claude usage is read from.
// The daemon keeps the default (its own usagebudget caches); the local TUI
// installs a daemon RPC, since the probe caches live in the daemon. nil
// restores the default. Returns a restore func.
func SetAccountUsageSource(read func(cfg config.Config) map[string]usagebudget.Reading) func() {
	accountUsageSource.Lock()
	previous := accountUsageSource.read
	accountUsageSource.read = read
	accountUsageSource.Unlock()
	return func() {
		accountUsageSource.Lock()
		accountUsageSource.read = previous
		accountUsageSource.Unlock()
	}
}

// AccountUsage returns the current reading of every Claude-capable account,
// keyed by account name ("default" for the default account). An account with
// no valid reading is absent.
func AccountUsage(cfg config.Config) map[string]usagebudget.Reading {
	accountUsageSource.RLock()
	read := accountUsageSource.read
	accountUsageSource.RUnlock()
	if read != nil {
		if out := read(cfg); out != nil {
			return out
		}
		return map[string]usagebudget.Reading{}
	}
	return LocalAccountUsage(cfg)
}

// LocalAccountUsage reads this process's usagebudget caches directly.
func LocalAccountUsage(cfg config.Config) map[string]usagebudget.Reading {
	out := map[string]usagebudget.Reading{}
	for _, name := range cfg.AccountNamesFor("claude") {
		if r, ok := usagebudget.CachedReadingForAccount(name); ok {
			out[name] = r
		}
	}
	return out
}

// PickMoveTarget resolves the "auto" move target: among accounts that support
// backendCommand other than exclude, the one with the lowest current usage
// (the fuller valid window). Unknown usage counts as 0%; ties keep candidate
// order ("default", then names sorted). ok is false when no candidate exists.
func PickMoveTarget(cfg config.Config, usage map[string]usagebudget.Reading, backendCommand, exclude string) (string, string, bool) {
	best, bestPct := "", 0.0
	var scores []string
	for _, name := range cfg.AccountNamesFor(backendCommand) {
		if name == exclude {
			continue
		}
		pct := usage[name].Percentage
		scores = append(scores, fmt.Sprintf("%s=%.0f", name, pct))
		if best == "" || pct < bestPct {
			best, bestPct = name, pct
		}
	}
	return best, strings.Join(scores, " "), best != ""
}

// NormalizeAccount maps a stored account ("" for default) to its name.
func NormalizeAccount(stored string) string {
	if s := strings.TrimSpace(stored); s != "" {
		return s
	}
	return config.DefaultAccountName
}

// storedAccount maps an account name to its stored form ("" for default).
func storedAccount(name string) string {
	if name == config.DefaultAccountName {
		return ""
	}
	return name
}
