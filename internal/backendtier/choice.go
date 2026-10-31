package backendtier

import (
	"fmt"
	"log/slog"

	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/uxlog"
)

// Choice carries the identity scored by routing through to task creation.
type Choice struct{ Backend, Account, Reason string }

// ResolveChoice optionally restricts selection to a backend and an account
// pin. A blank account permits each tier to select its own identity.
func ResolveChoice(cfg config.Config, backend, account string) Choice {
	strategy := cfg.BackendRouting.Strategy
	if strategy != "" && strategy != config.StrategyOrdered && strategy != config.StrategyHeadroom {
		warnUnknownStrategy(strategy)
	}
	var best, unread Choice
	var score float64
	for _, tier := range cfg.BackendRouting.Tiers {
		b, ok := cfg.Backends[tier.Backend]
		if !ok || (backend != "" && tier.Backend != backend) {
			continue
		}
		if tier.Account != "" {
			err := cfg.ValidateAccount(tier.Account)
			if err == nil && !cfg.AccountSupports(tier.Account, b.Command) {
				err = fmt.Errorf("account does not support backend")
			}
			if err != nil {
				msg := fmt.Sprintf("[backendtier] skipping backend=%q account=%q: %v", tier.Backend, tier.Account, err)
				uxlog.Log("%s", msg)
				slog.Warn(msg)
				continue
			}
		}
		selected := account
		if selected == "" {
			selected = tier.Account
		}
		if selected == "" {
			selected = cfg.ResolveAccount("", "")
		}
		choice := Choice{Backend: tier.Backend, Account: selected}
		var pct float64
		var known bool
		switch tier.Probe {
		case config.ProbeNone:
		case config.ProbeClaudeUsage:
			pct, known = claudeUsageForAccount([]string{selected})()
		case config.ProbeCodexUsage:
			if selected == "" || selected == config.DefaultAccountName {
				pct, known = codexCachedPct()
			}
		default:
			continue
		}
		choice.Reason = fmt.Sprintf("strategy=%s probe=%s known=%t usage=%.2f threshold=%d", strategy, tier.Probe, known, pct, tier.ThresholdPct)
		if strategy != config.StrategyHeadroom {
			if tier.Probe == config.ProbeNone || !known || pct < float64(tier.ThresholdPct) {
				return choice
			}
			continue
		}
		if !known {
			if unread.Backend == "" {
				unread = choice
			}
			continue
		}
		headroom := float64(tier.ThresholdPct) - pct
		if headroom > 0 && (best.Backend == "" || headroom > score) {
			best, score = choice, headroom
		}
	}
	if best.Backend != "" {
		return best
	}
	return unread
}
