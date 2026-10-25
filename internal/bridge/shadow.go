package bridge

import (
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/drn/argus/internal/uxlog"
)

// Signal names a compared verdict.
type Signal string

const (
	SignalIdle       Signal = "idle"
	SignalNeedsInput Signal = "needs_input"
	// SignalNeedsInputPermission holds the needs-input ticks where the bridge
	// reports a permission-kind dialog. That kind can span the tool's execution
	// after approval (pending-or-running), so its disagreements are reported
	// apart and never pollute the headline needs_input numbers.
	SignalNeedsInputPermission Signal = "needs_input_permission"
)

// DefaultSettle is how long a disagreement must persist before it counts: the
// scraper lags ~3s by design and the watcher ticks every 5s, so anything
// shorter is expected skew rather than a screen-reading error.
const DefaultSettle = 10 * time.Second

// Counters are per-signal observation counts, one increment per task per tick.
type Counters struct {
	Agree    int64 `json:"agree"`
	Disagree int64 `json:"disagree"`
	Settling int64 `json:"settling"`
	NoBridge int64 `json:"no_bridge"`
}

// OpenDisagreement is a disagreement currently in progress.
type OpenDisagreement struct {
	TaskID     string  `json:"task_id"`
	Signal     Signal  `json:"signal"`
	Scraper    bool    `json:"scraper"`
	Bridge     bool    `json:"bridge"`
	ForSeconds float64 `json:"for_seconds"`
	Settled    bool    `json:"settled"`
}

// Stats is the GET /api/bridge/shadow payload.
type Stats struct {
	SettleSeconds float64             `json:"settle_seconds"`
	BridgeTasks   int                 `json:"bridge_tasks"`
	Signals       map[Signal]Counters `json:"signals"`
	Open          []OpenDisagreement  `json:"open"`
}

type openKey struct {
	task string
	sig  Signal
}

type openState struct {
	since   time.Time
	scraper bool
	bridge  bool
	logged  bool
}

// Shadow compares the daemon's screen-reading verdicts with Hub state. It
// never feeds anything back: phase 1 is measurement only.
type Shadow struct {
	hub    *Hub
	settle time.Duration
	logf   func(format string, args ...any)

	mu       sync.Mutex
	counters map[Signal]*Counters
	open     map[openKey]*openState
}

// NewShadow builds a comparator over hub with the given settle window
// (DefaultSettle when <= 0).
func NewShadow(hub *Hub, settle time.Duration) *Shadow {
	if settle <= 0 {
		settle = DefaultSettle
	}
	return &Shadow{
		hub:    hub,
		settle: settle,
		logf:   defaultLogf,
		counters: map[Signal]*Counters{
			SignalIdle:                 {},
			SignalNeedsInput:           {},
			SignalNeedsInputPermission: {},
		},
		open: make(map[openKey]*openState),
	}
}

// defaultLogf writes to uxlog (the TUI process's ux.log) and slog: the daemon
// never calls uxlog.Init, so slog is what actually lands in daemon.log there.
func defaultLogf(format string, args ...any) {
	uxlog.Log(format, args...)
	slog.Info(fmt.Sprintf(format, args...))
}

// Hub returns the state hub the comparator reads.
func (s *Shadow) Hub() *Hub { return s.hub }

// Observe compares one watcher tick. running is every running task id; idle
// and needs are the scraper's FINAL verdict sets for that tick. Idle is
// compared as "screen quiescent": a bridge that is not busy, or is waiting on a
// dialog (the screen is parked there too), counts as idle.
func (s *Shadow) Observe(now time.Time, running []string, idle, needs map[string]bool, lookups ...func(string, time.Time) (State, bool)) {
	s.hub.Prune(now)
	s.mu.Lock()
	defer s.mu.Unlock()
	runningSet := make(map[string]bool, len(running))
	for _, id := range running {
		runningSet[id] = true
		st, live := s.hub.Live(id, now)
		if len(lookups) > 0 {
			st, live = lookups[0](id, now)
		}
		if !live {
			s.noBridge(id)
			continue
		}
		s.compare(now, id, SignalIdle, idle[id], !st.Busy || st.NeedsInput)
		// Needs-input counts under the headline signal unless the bridge holds a
		// permission-kind dialog; the other variant's open disagreement (the kind
		// just flipped) is dropped so it cannot linger unresolved.
		sig, other := SignalNeedsInput, SignalNeedsInputPermission
		if st.NeedsInput && st.NeedsInputKind == KindPermission {
			sig, other = SignalNeedsInputPermission, SignalNeedsInput
		}
		delete(s.open, openKey{id, other})
		s.compare(now, id, sig, needs[id], st.NeedsInput)
	}
	// A task that left the running set takes its open disagreements with it.
	for k := range s.open {
		if !runningSet[k.task] {
			s.closeOpen(now, k, "task stopped")
		}
	}
}

func (s *Shadow) noBridge(id string) {
	for _, sig := range []Signal{SignalIdle, SignalNeedsInput} {
		s.counters[sig].NoBridge++
	}
	for _, sig := range []Signal{SignalIdle, SignalNeedsInput, SignalNeedsInputPermission} {
		delete(s.open, openKey{id, sig})
	}
}

func (s *Shadow) compare(now time.Time, id string, sig Signal, scraper, bridge bool) {
	c := s.counters[sig]
	k := openKey{id, sig}
	o := s.open[k]
	if scraper == bridge {
		c.Agree++
		if o != nil {
			s.closeOpen(now, k, "agreed")
		}
		return
	}
	if o == nil {
		o = &openState{since: now}
		s.open[k] = o
	}
	o.scraper, o.bridge = scraper, bridge
	if now.Sub(o.since) < s.settle {
		c.Settling++
		return
	}
	c.Disagree++
	if !o.logged {
		o.logged = true
		s.logf("[bridge] disagree signal=%s task=%s scraper=%t bridge=%t for=%s",
			sig, id, scraper, bridge, now.Sub(o.since).Round(time.Second))
	}
}

// closeOpen ends an open disagreement, logging the resolution only when the
// disagreement had been logged as persistent.
func (s *Shadow) closeOpen(now time.Time, k openKey, why string) {
	o := s.open[k]
	delete(s.open, k)
	if o != nil && o.logged {
		s.logf("[bridge] resolved signal=%s task=%s after=%s (%s)",
			k.sig, k.task, now.Sub(o.since).Round(time.Second), why)
	}
}

// Stats snapshots the counters and open disagreements.
func (s *Shadow) Stats(now time.Time) Stats {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := Stats{
		SettleSeconds: s.settle.Seconds(),
		BridgeTasks:   s.hub.Len(),
		Signals:       make(map[Signal]Counters, len(s.counters)),
		Open:          []OpenDisagreement{},
	}
	for sig, c := range s.counters {
		out.Signals[sig] = *c
	}
	for k, o := range s.open {
		d := now.Sub(o.since)
		out.Open = append(out.Open, OpenDisagreement{
			TaskID: k.task, Signal: k.sig, Scraper: o.scraper, Bridge: o.bridge,
			ForSeconds: d.Seconds(), Settled: d >= s.settle,
		})
	}
	sort.Slice(out.Open, func(i, j int) bool {
		if out.Open[i].TaskID != out.Open[j].TaskID {
			return out.Open[i].TaskID < out.Open[j].TaskID
		}
		return out.Open[i].Signal < out.Open[j].Signal
	})
	return out
}
