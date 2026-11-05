package backendtier

import (
	"sync"

	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/usagebudget"
	"github.com/drn/argus/internal/uxlog"
)

// Usage is one snapshot of every reading tier resolution consumes: Claude per
// account (keyed by account name, "default" for the default account) and the
// Codex weekly reading. An absent entry means "no valid reading".
type Usage struct {
	Claude map[string]usagebudget.Reading
	Codex  *usagebudget.Reading
}

var usageSource struct {
	sync.RWMutex
	read func() (Usage, error)
}

// SetUsageSource redirects tier resolution's usage readings. The usagebudget
// and Codex probe caches are populated only in the daemon process, so a TUI
// process that resolves tiers locally sees every tier as unread; it installs a
// daemon RPC here instead. nil restores local-cache reads. Returns a restore
// func.
func SetUsageSource(read func() (Usage, error)) func() {
	usageSource.Lock()
	previous := usageSource.read
	usageSource.read = read
	usageSource.Unlock()
	return func() {
		usageSource.Lock()
		usageSource.read = previous
		usageSource.Unlock()
	}
}

// readings are the per-resolution reading functions ResolveChoice consults.
type readings struct {
	claude func(account string) (float64, bool)
	codex  func() (float64, bool)
}

// currentReadings returns the reading functions for one resolution: a single
// snapshot from the installed source (one RPC per resolution), or this
// process's own caches when none is installed or the source fails.
func currentReadings() readings {
	usageSource.RLock()
	read := usageSource.read
	usageSource.RUnlock()
	if read != nil {
		u, err := read()
		if err == nil {
			uxlog.Log("[backendtier] using daemon usage readings: claude_accounts=%d codex=%t", len(u.Claude), u.Codex != nil)
			return readings{
				claude: func(account string) (float64, bool) {
					if account == "" {
						account = config.DefaultAccountName
					}
					r, ok := u.Claude[account]
					return r.Percentage, ok
				},
				codex: func() (float64, bool) {
					if u.Codex == nil {
						return 0, false
					}
					return u.Codex.Percentage, true
				},
			}
		}
		uxlog.Log("[backendtier] daemon usage read failed, falling back to local caches: %v", err)
	}
	return readings{
		claude: func(account string) (float64, bool) { return claudeUsageForAccount([]string{account})() },
		codex:  func() (float64, bool) { return codexCachedPct() },
	}
}

// LocalUsage snapshots this process's caches for the given config's accounts;
// the daemon serves it over RPC to TUI processes.
func LocalUsage(cfg config.Config) Usage {
	u := Usage{Claude: map[string]usagebudget.Reading{}}
	for _, name := range cfg.AccountNamesFor("claude") {
		if r, ok := usagebudget.CachedReadingForAccount(name); ok {
			u.Claude[name] = r
		}
	}
	if r, ok := CachedReading(); ok {
		rr := usagebudget.Reading{Percentage: r.Percentage, ResetAt: r.ResetAt, LastProbedAt: r.LastProbedAt}
		u.Codex = &rr
	}
	return u
}
