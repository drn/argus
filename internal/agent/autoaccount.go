package agent

import (
	"log/slog"
	"sync"

	"github.com/drn/argus/internal/accountusage"
	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/uxlog"
)

var accountUsageSource struct {
	sync.RWMutex
	read func() map[string]accountusage.Usage
}

// SetAccountUsageSource installs the per-account usage reader "auto" account
// selection consults: the daemon's ledger in the daemon, a daemon RPC in the
// local TUI. nil means every account is unknown. Returns a restore func.
func SetAccountUsageSource(read func() map[string]accountusage.Usage) func() {
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

// AccountUsage returns the current per-account usage snapshot (empty when no
// source is installed).
func AccountUsage() map[string]accountusage.Usage {
	accountUsageSource.RLock()
	read := accountUsageSource.read
	accountUsageSource.RUnlock()
	if read == nil {
		return map[string]accountusage.Usage{}
	}
	if snap := read(); snap != nil {
		return snap
	}
	return map[string]accountusage.Usage{}
}

// AccountThresholds returns the configured "auto" selection thresholds.
func AccountThresholds(cfg config.Config) accountusage.Thresholds {
	return accountusage.Thresholds{
		FiveHour: cfg.AccountRouting.FiveHourThreshold(),
		Weekly:   cfg.AccountRouting.WeeklyThreshold(),
	}
}

// PickAccount applies "auto" selection over the accounts that support the
// backend command, skipping exclude. ok is false when no candidate remains.
func PickAccount(cfg config.Config, backendCommand string, exclude ...string) (accountusage.Choice, bool) {
	return accountusage.Pick(AccountThresholds(cfg), cfg.AccountNamesFor(backendCommand), AccountUsage(), exclude...)
}

// peekAutoAccount is pickAutoAccount without the log line, for lookups that
// only need the likely account (backend-tier resolution runs on every
// ResolveBackend of a task with no stored backend).
func peekAutoAccount(cfg config.Config, backendCommand string) string {
	if choice, ok := PickAccount(cfg, backendCommand); ok {
		return choice.Account
	}
	return config.DefaultAccountName
}

// autoClaudeUsagePct is the Claude usage backend-tier routing reads for an
// auto-picked account: the higher of its ledger windows. A named account with
// no reading counts as 0%, as in Pick; the probe caches only learn a named
// account once one of its sessions reports, and an unknown tier loses to any
// measured one. ok is false only for an unmeasured default, which keeps the
// existing probe.
func autoClaudeUsagePct(account string) (float64, bool) {
	account = accountusage.NormalizeAccount(account)
	u := AccountUsage()[account]
	if u.FiveHour == nil && u.Weekly == nil {
		return 0, account != config.DefaultAccountName
	}
	pct := 0.0
	for _, r := range []*accountusage.Reading{u.FiveHour, u.Weekly} {
		if r != nil && r.Pct > pct {
			pct = r.Pct
		}
	}
	return pct, true
}

// pickAutoAccount resolves "auto" to a concrete account name for a new task
// on the backend command, logging the choice and every score.
func pickAutoAccount(cfg config.Config, backendCommand string) string {
	choice, ok := PickAccount(cfg, backendCommand)
	if !ok {
		return config.DefaultAccountName
	}
	msg := "[account] auto: picked " + choice.String()
	if choice.OverThreshold {
		msg += " (every account is over its usage threshold)"
	}
	uxlog.Log("%s", msg)
	slog.Info(msg)
	return choice.Account
}

// InheritAccount is the account selection a child task (hera worker, MCP
// task_create child, fork) inherits from a parent: "auto" when the parent was
// auto-routed, so the child is placed by usage too; otherwise the parent's
// account pinned verbatim (PinnedAccount).
func InheritAccount(parent *model.Task) string {
	if parent.AccountAuto {
		return config.AutoAccountName
	}
	return PinnedAccount(parent.Account)
}
