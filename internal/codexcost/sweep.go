package codexcost

import (
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/pricing"
)

// EndedGrace is how long after a binding ends the sweeper keeps reading its
// rollouts, so the final calls made just before the end still accrue.
const EndedGrace = 15 * time.Minute

// BackfillWindow bounds the one-time backfill each Sweeper runs on its first
// sweep: bindings that ended up to this long ago and were never read are
// priced from the Codex rollouts still on disk.
const BackfillWindow = 30 * 24 * time.Hour

// Store is the slice of *db.DB the sweeper uses.
type Store interface {
	ListCostSweepBindings(endedSince time.Time) ([]db.HeraBinding, error)
	CodexCostCursors(bindingID int64) (map[string]db.CodexCostCursor, error)
	AccrueCodexCalls(bindingID int64, delta db.HeraBindingCostTotals, cur db.CodexCostCursor) error
	Get(id string) (*model.Task, error)
	Config() config.Config
}

// Sweeper prices every new Codex call for every sweepable binding. The func
// fields are seams; New fills them with the real implementations. A Sweeper
// is meant to be reused across ticks: its first Sweep also runs the one-time
// backfill.
type Sweeper struct {
	Store Store
	// StateDB resolves the Codex thread index (state_5.sqlite) for a task.
	StateDB func(task *model.Task, cfg config.Config) (string, error)
	// Rates loads the rate table for a worktree (in-repo override, then
	// library).
	Rates func(worktree string) (*pricing.Table, error)
	Now   func() time.Time

	backfilled bool
}

// New returns a Sweeper wired to the real Codex home and rate table.
func New(store Store) *Sweeper {
	return &Sweeper{
		Store: store,
		StateDB: func(task *model.Task, cfg config.Config) (string, error) {
			home, err := agent.CodexHomeForTask(task, cfg)
			if err != nil {
				return "", err
			}
			return agent.CodexStateDBPath(home)
		},
		Rates: func(worktree string) (*pricing.Table, error) {
			return pricing.LoadInstalled(filepath.Join(db.DataDir(), "rates.toml"), worktree)
		},
		Now: time.Now,
	}
}

// Stats summarizes one sweep for logging.
type Stats struct {
	Bindings   int // bindings whose Codex thread index was consulted
	Backfilled int // of Bindings, never-read bindings ended before the grace window that were read now
	Calls      int // new calls inside a binding's window
	Unpriced   int // of Calls, those whose model has no rate entry
	USD        float64
	Ambiguous  int // calls skipped: made while the task was bound under 2+ orchestrators
	Errors     int
}

// Sweep runs one pass over live bindings and bindings that ended within
// EndedGrace. The first pass on a Sweeper additionally backfills bindings
// that ended within BackfillWindow and were never read. Per-binding failures
// are logged and counted, never abort the pass.
func (s *Sweeper) Sweep() Stats {
	var st Stats
	now := s.Now()
	graceCut := now.Add(-EndedGrace)
	since := graceCut
	backfill := !s.backfilled
	if backfill {
		since = now.Add(-BackfillWindow)
	}
	bindings, err := s.Store.ListCostSweepBindings(since)
	if err != nil {
		slog.Warn("[codexcost] list bindings failed", "err", err)
		st.Errors++
		return st
	}
	s.backfilled = true
	byTask := make(map[string][]db.HeraBinding)
	for _, b := range bindings {
		byTask[b.ArgusTaskID] = append(byTask[b.ArgusTaskID], b)
	}
	cfg := s.Store.Config()
	idx := indexCache{}
	for _, b := range bindings {
		stale := b.EndedAt != nil && b.EndedAt.Before(graceCut)
		task, ok := s.codexTask(b, cfg)
		if !ok {
			continue
		}
		st.Bindings++
		if err := s.sweepBinding(b, byTask[b.ArgusTaskID], task, cfg, idx, stale, &st); err != nil {
			st.Errors++
			slog.Warn("[codexcost] binding sweep failed", "binding", b.ID, "task", b.ArgusTaskID, "err", err)
		}
	}
	if st.Calls > 0 || st.Errors > 0 || backfill {
		slog.Info("[codexcost] sweep", "backfill", backfill, "bindings", st.Bindings, "backfilled", st.Backfilled,
			"calls", st.Calls, "unpriced", st.Unpriced, "usd", fmt.Sprintf("%.4f", st.USD),
			"ambiguous", st.Ambiguous, "errors", st.Errors)
	}
	return st
}

// codexTask resolves the task to read Codex state for. A task whose family
// isn't a Codex-rollout cost source is skipped. A deleted task row (nuked
// workers are deleted, often right after their last calls) can't name its
// backend, so it falls back to the default Codex home: whether Codex recorded
// threads in that worktree is what decides, and a Claude worker has none.
func (s *Sweeper) codexTask(b db.HeraBinding, cfg config.Config) (*model.Task, bool) {
	task, err := s.Store.Get(b.ArgusTaskID)
	if err != nil {
		return &model.Task{ID: b.ArgusTaskID, Worktree: b.WorktreePath}, true
	}
	backend, err := agent.ResolveBackend(task, cfg)
	if err != nil || agent.FamilyOf(backend.Command).CostSource != agent.CostSourceCodexRollout {
		return nil, false
	}
	return task, true
}

// indexCache memoizes each Codex thread index for one sweep.
type indexCache map[string]map[string][]string

func (c indexCache) rollouts(stateDB, cwd string) ([]string, error) {
	idx, ok := c[stateDB]
	if !ok {
		var err error
		if idx, err = ThreadIndex(stateDB); err != nil {
			return nil, err
		}
		c[stateDB] = idx
	}
	return idx[cwd], nil
}

func (s *Sweeper) sweepBinding(b db.HeraBinding, siblings []db.HeraBinding, task *model.Task, cfg config.Config, idx indexCache, stale bool, st *Stats) error {
	stateDB, err := s.StateDB(task, cfg)
	if err != nil {
		return fmt.Errorf("codex state db: %w", err)
	}
	paths, err := idx.rollouts(stateDB, b.WorktreePath)
	if err != nil {
		return err
	}
	if len(paths) == 0 {
		return nil
	}
	cursors, err := s.Store.CodexCostCursors(b.ID)
	if err != nil {
		return err
	}
	if stale && len(cursors) > 0 {
		return nil // read while live or in grace; backfill is once only
	}
	table, err := s.Rates(b.WorktreePath)
	if err != nil {
		return fmt.Errorf("rate table: %w", err)
	}
	read := false
	for _, path := range paths {
		cur, ok := cursors[path]
		if !ok {
			cur = db.CodexCostCursor{RolloutPath: path, LastTotal: -1}
		}
		advanced, err := s.sweepRollout(b, siblings, table, cur, st)
		if err != nil {
			return fmt.Errorf("rollout %s: %w", filepath.Base(path), err)
		}
		read = read || advanced
	}
	if stale && read {
		st.Backfilled++
	}
	return nil
}

// sweepRollout prices one rollout's new calls for b and reports whether the
// cursor advanced.
func (s *Sweeper) sweepRollout(b db.HeraBinding, siblings []db.HeraBinding, table *pricing.Table, cur db.CodexCostCursor, st *Stats) (bool, error) {
	f, err := os.Open(cur.RolloutPath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil // archived/deleted by Codex; nothing more to read
		}
		return false, err
	}
	defer f.Close() //nolint:errcheck // read-only fd
	info, err := f.Stat()
	if err != nil {
		return false, err
	}
	if info.Size() <= cur.Offset {
		return false, nil
	}
	if _, err := f.Seek(cur.Offset, 0); err != nil {
		return false, err
	}
	calls, next, err := Parse(f, cur)
	if err != nil {
		return false, err
	}
	if next.Offset == cur.Offset {
		return false, nil
	}
	var delta db.HeraBindingCostTotals
	for _, c := range calls {
		if !inWindow(c.At, b) {
			continue
		}
		if heldElsewhere(c.At, b.ID, siblings) {
			st.Ambiguous++
			continue
		}
		st.Calls++
		delta.TokensInput += c.Deltas.Input
		delta.TokensCacheRead += c.Deltas.CacheRead
		delta.TokensCacheWrite5m += c.Deltas.CacheWrite5m
		delta.TokensOutput += c.Deltas.Output
		if usd, ok := table.PriceCall(c.Model, c.Deltas, c.InputTokens); ok {
			delta.CostUSDAccrued += usd
		} else {
			st.Unpriced++
		}
	}
	if err := s.Store.AccrueCodexCalls(b.ID, delta, next); err != nil {
		return false, err
	}
	st.USD += delta.CostUSDAccrued
	return true, nil
}

// inWindow reports whether a call made at `at` belongs to binding b: at or
// after its start and, once ended, before its end. A call with no parseable
// timestamp is never attributed — under-counting beats charging a call to
// the wrong binding.
func inWindow(at time.Time, b db.HeraBinding) bool {
	if at.IsZero() || at.Before(b.StartedAt) {
		return false
	}
	return b.EndedAt == nil || at.Before(*b.EndedAt)
}

// heldElsewhere reports whether another of the task's bindings was also live
// at `at` — the task was bound under 2+ orchestrators then, so there is no
// single binding to charge (the Codex twin of the Stop-hook PUT handler's
// ErrHeraAmbiguous skip). Skipped, not split: under-counting beats charging
// one call twice.
func heldElsewhere(at time.Time, bindingID int64, siblings []db.HeraBinding) bool {
	for _, o := range siblings {
		if o.ID != bindingID && inWindow(at, o) {
			return true
		}
	}
	return false
}

// ThreadIndex maps each working directory to the rollout files of every
// Codex thread recorded there in the thread index at stateDB — a worker's own
// threads plus any Codex sub-agent threads it spawned. A missing index yields
// an empty map.
func ThreadIndex(stateDB string) (map[string][]string, error) {
	out := make(map[string][]string)
	if _, err := os.Stat(stateDB); err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return nil, err
	}
	conn, err := sql.Open("sqlite", "file:"+stateDB+"?mode=ro")
	if err != nil {
		return nil, fmt.Errorf("open codex thread index: %w", err)
	}
	defer conn.Close() //nolint:errcheck // read-only handle
	rows, err := conn.Query(`SELECT cwd, rollout_path FROM threads WHERE rollout_path != '' ORDER BY created_at, id`)
	if err != nil {
		return nil, fmt.Errorf("query codex thread index: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var cwd, p string
		if err := rows.Scan(&cwd, &p); err != nil {
			return nil, fmt.Errorf("scan codex thread index: %w", err)
		}
		out[cwd] = append(out[cwd], p)
	}
	return out, rows.Err()
}
