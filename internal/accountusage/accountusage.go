// Package accountusage tracks Claude plan usage per account and picks the
// account with the most headroom for "auto" account selection.
//
// The Ledger is fed by the daemon from bridge session.measure rate-limit
// readings. A reading stays valid until its own reset time, so an account that
// hit its cap is remembered as capped even after its sessions end. Nothing is
// persisted: after a daemon restart every account is unknown, and unknown
// counts as available (fail open).
package accountusage

import (
	"fmt"
	"math"
	"strings"
	"sync"
	"time"
)

// Window names, matching the bridge's rate_limits kinds.
const (
	WindowFiveHour = "five_hour"
	WindowWeekly   = "seven_day"
)

// Reading is one valid usage measurement of one window.
type Reading struct {
	Pct        float64   `json:"pct"`
	ResetsAt   time.Time `json:"resets_at"`
	MeasuredAt time.Time `json:"measured_at"`
}

// Usage is an account's currently valid readings; nil means unknown.
type Usage struct {
	FiveHour *Reading `json:"five_hour,omitempty"`
	Weekly   *Reading `json:"weekly,omitempty"`
}

// NormalizeAccount maps a stored task account ("" for the default login) to
// the ledger key.
func NormalizeAccount(account string) string {
	if account == "" {
		return "default"
	}
	return account
}

// Ledger is a concurrency-safe store of the newest reading per (account,
// window). The zero value is not usable; use NewLedger. A nil *Ledger is a
// no-op that reports nothing.
type Ledger struct {
	mu  sync.Mutex
	now func() time.Time
	m   map[string]map[string]Reading
}

// NewLedger returns an empty ledger on the wall clock.
func NewLedger() *Ledger {
	return &Ledger{now: time.Now, m: make(map[string]map[string]Reading)}
}

// Record stores a measurement unless it is invalid (unknown window,
// non-finite or out-of-range percentage, reset time not in the future) or
// older than the one already held for that (account, window).
func (l *Ledger) Record(account, window string, pct float64, resetsAt, measuredAt time.Time) {
	if l == nil || (window != WindowFiveHour && window != WindowWeekly) {
		return
	}
	if math.IsNaN(pct) || math.IsInf(pct, 0) || pct < 0 || pct > 100 {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if !resetsAt.After(l.now()) {
		return
	}
	account = NormalizeAccount(account)
	windows := l.m[account]
	if windows == nil {
		windows = make(map[string]Reading)
		l.m[account] = windows
	}
	if prev, ok := windows[window]; ok && measuredAt.Before(prev.MeasuredAt) {
		return
	}
	windows[window] = Reading{Pct: pct, ResetsAt: resetsAt, MeasuredAt: measuredAt}
}

// Snapshot returns every account's still-valid readings, dropping (and
// forgetting) any whose reset time has passed.
func (l *Ledger) Snapshot() map[string]Usage {
	out := make(map[string]Usage)
	if l == nil {
		return out
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	for account, windows := range l.m {
		var u Usage
		for window, r := range windows {
			if !r.ResetsAt.After(now) {
				delete(windows, window)
				continue
			}
			r := r
			if window == WindowFiveHour {
				u.FiveHour = &r
			} else {
				u.Weekly = &r
			}
		}
		if len(windows) == 0 {
			delete(l.m, account)
			continue
		}
		out[account] = u
	}
	return out
}

// Thresholds are the per-window usage percentages headroom is measured to.
type Thresholds struct {
	FiveHour int
	Weekly   int
}

// Score is one candidate's headroom: the smaller of its two windows'
// threshold-minus-used, an unknown window counting as 0% used.
type Score struct {
	Account string
	Score   float64
}

// Choice is the outcome of Pick.
type Choice struct {
	Account string
	Scores  []Score
	// OverThreshold is true when no candidate had positive headroom; Account
	// is then the least-over candidate.
	OverThreshold bool
}

// String renders the choice and every score for logs.
func (c Choice) String() string {
	parts := make([]string, len(c.Scores))
	for i, s := range c.Scores {
		parts[i] = fmt.Sprintf("%s=%g", s.Account, s.Score)
	}
	return fmt.Sprintf("%s (scores: %s)", c.Account, strings.Join(parts, " "))
}

// Pick chooses the candidate with the highest headroom score, ties going to
// candidate order. Candidates in exclude are skipped. It reports false only
// when no candidate remains.
func Pick(th Thresholds, candidates []string, snap map[string]Usage, exclude ...string) (Choice, bool) {
	var c Choice
	best := math.Inf(-1)
	for _, name := range candidates {
		if contains(exclude, name) {
			continue
		}
		u := snap[NormalizeAccount(name)]
		score := math.Min(headroom(th.FiveHour, u.FiveHour), headroom(th.Weekly, u.Weekly))
		c.Scores = append(c.Scores, Score{Account: name, Score: score})
		if score > best {
			best, c.Account = score, name
		}
	}
	if len(c.Scores) == 0 {
		return Choice{}, false
	}
	c.OverThreshold = best <= 0
	return c, true
}

func headroom(threshold int, r *Reading) float64 {
	if r == nil {
		return float64(threshold)
	}
	return float64(threshold) - r.Pct
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
