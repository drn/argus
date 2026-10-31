package usagebudget

import (
	"context"
	"errors"
	"regexp"
	"time"

	"github.com/drn/argus/internal/config"
)

// SelectReading selects the fullest valid window at read time, so expiry of
// one window never hides the remaining window until the next probe.
func SelectReading(r Reading, now time.Time) (Reading, bool) {
	if r.FiveHour == nil && r.SevenDay == nil {
		return r, now.Sub(r.LastProbedAt) <= CacheMaxAge && (r.ResetAt.IsZero() || r.ResetAt.After(now))
	}
	var best *Window
	fallbackAt := r.LastProbedAt
	for i, w := range []*Window{r.SevenDay, r.FiveHour} {
		if w == nil || !w.ResetAt.After(now) {
			continue
		}
		at := w.LastProbedAt
		if at.IsZero() {
			at = fallbackAt
		}
		if now.Sub(at) > CacheMaxAge {
			continue
		}
		if best == nil || w.Percentage > best.Percentage {
			best = w
			r.Percentage, r.ResetAt, r.LastProbedAt = w.Percentage, w.ResetAt, at
			if i == 0 {
				r.Window = "seven_day"
			} else {
				r.Window = "five_hour"
			}
		}
	}
	return r, best != nil
}

var sessionResetRe = regexp.MustCompile(`(?i)\bResets\s+([0-9]{1,2}(?::[0-9]{2})?\s*(?:AM|PM)?)\s*\(([^)]+)\)`)

// A parse miss in one section must not erase a still-valid prior window or
// refresh its timestamp using the other section's successful parse.
func mergeWindows(old, next Reading) Reading {
	if next.FiveHour != nil || next.SevenDay != nil {
		if next.FiveHour == nil {
			next.FiveHour = old.FiveHour
		}
		if next.SevenDay == nil {
			next.SevenDay = old.SevenDay
		}
	}
	return next
}

func parseUsageOutput(output string, base time.Time) (Reading, error) {
	var r Reading
	for _, session := range []bool{false, true} {
		part, err := parseUsageSection(output, base, session)
		if err != nil {
			continue
		}
		w := &Window{Percentage: part.Percentage, ResetAt: part.ResetAt, LastProbedAt: base}
		if session {
			r.FiveHour = w
		} else {
			r.SevenDay = w
		}
	}
	r, ok := SelectReading(r, base)
	if !ok {
		return Reading{}, errors.New("no valid usage windows")
	}
	return r, nil
}

var startAccountUsageSession = func(ctx context.Context, dir, configDir string) (usageSession, error) {
	s, err := startAccountPTYSession(ctx, dir, configDir, "claude", "--", "/usage")
	if err != nil {
		return nil, err
	}
	return s, nil
}

// ProbeConfiguredAccounts runs on the daemon's existing startup/ticker path.
// A failed account probe never borrows a different identity's measurement.
func ProbeConfiguredAccounts(ctx context.Context, cfg config.Config) error {
	_ = Probe(ctx)
	seen := map[string]bool{"": true, "default": true}
	accounts := []string{cfg.ResolveAccount("", "")}
	for _, tier := range cfg.BackendRouting.Tiers {
		if tier.Probe == config.ProbeClaudeUsage {
			accounts = append(accounts, tier.Account)
		}
	}
	for _, account := range accounts {
		if seen[account] {
			continue
		}
		seen[account] = true
		dir, _, err := cfg.ClaudeConfigDir(account)
		if err != nil {
			logWarn("[usagebudget] account=%q skipped: %v", account, err)
			continue
		}
		if ctx.Err() != nil {
			break
		}
		_ = probeAccount(ctx, account, dir)
	}
	return nil
}
