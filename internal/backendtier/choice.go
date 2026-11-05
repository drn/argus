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
	var best, unread, over Choice
	var score, overPct float64
	var rd readings
	haveReadings := false
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
		case config.ProbeClaudeUsage, config.ProbeCodexUsage:
			if !haveReadings {
				rd, haveReadings = currentReadings(), true
			}
			if tier.Probe == config.ProbeClaudeUsage {
				pct, known = rd.claude(selected)
			} else if selected == "" || selected == config.DefaultAccountName {
				pct, known = rd.codex()
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
		if headroom > 0 {
			if best.Backend == "" || headroom > score {
				best, score = choice, headroom
			}
		} else if over.Backend == "" || pct < overPct {
			over, overPct = choice, pct
		}
	}
	// Preference under headroom: a known tier with room > a tier with no
	// reading (unknown fails open) > the least-used tier when every known tier
	// is at/over its threshold. The last case must still pick a tier: returning
	// empty would let callers fall back to the default backend on the default
	// account, which is the most likely to be the depleted one.
	if best.Backend != "" {
		return best
	}
	if unread.Backend != "" {
		return unread
	}
	if over.Backend != "" {
		over.Reason += " (all tiers over threshold; least-used)"
		uxlog.Log("[backendtier] all tiers over threshold; least-used backend=%q account=%q usage=%.2f", over.Backend, over.Account, overPct)
		return over
	}
	return Choice{}
}
