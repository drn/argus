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

// Store is the slice of *db.DB the sweeper uses.
type Store interface {
	ListCostSweepBindings(endedSince time.Time) ([]db.CostSweepBinding, error)
	CodexCostCursors(bindingID int64) (map[string]db.CodexCostCursor, error)
	AccrueCodexCalls(bindingID int64, delta db.HeraBindingCostTotals, cur db.CodexCostCursor) error
	Get(id string) (*model.Task, error)
	Config() config.Config
}

// Sweeper prices every new Codex call for every sweepable binding. The func
// fields are seams; New fills them with the real implementations.
type Sweeper struct {
	Store Store
	// StateDB resolves the Codex thread index (state_5.sqlite) for a task.
	StateDB func(task *model.Task, cfg config.Config) (string, error)
	// Rates loads the rate table for a task (in-repo override, then library).
	Rates func(task *model.Task) (*pricing.Table, error)
	Now   func() time.Time
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
		Rates: func(task *model.Task) (*pricing.Table, error) {
			return pricing.LoadInstalled(filepath.Join(db.DataDir(), "rates.toml"), task.Worktree)
		},
		Now: time.Now,
	}
}

// Stats summarizes one sweep for logging.
type Stats struct {
	Bindings  int // Codex bindings examined
	Calls     int // new calls inside a binding's window
	Unpriced  int // of Calls, those whose model has no rate entry
	USD       float64
	Ambiguous int // live bindings skipped: task bound under 2+ orchestrators
	Errors    int
}

// Sweep runs one pass. Per-binding failures are logged and counted, never
// abort the pass.
func (s *Sweeper) Sweep() Stats {
	var st Stats
	bindings, err := s.Store.ListCostSweepBindings(s.Now().Add(-EndedGrace))
	if err != nil {
		slog.Warn("[codexcost] list bindings failed", "err", err)
		st.Errors++
		return st
	}
	cfg := s.Store.Config()
	for _, b := range bindings {
		task, err := s.Store.Get(b.ArgusTaskID)
		if err != nil {
			continue // task row gone (pruned); nothing to resolve a backend from
		}
		backend, err := agent.ResolveBackend(task, cfg)
		if err != nil || agent.FamilyOf(backend.Command).CostSource != agent.CostSourceCodexRollout {
			continue
		}
		st.Bindings++
		if b.EndedAt == nil && b.TaskLiveBindings > 1 {
			st.Ambiguous++
			slog.Info("[codexcost] skipped binding: task has multiple live hera bindings", "binding", b.ID, "task", b.ArgusTaskID)
			continue
		}
		if err := s.sweepBinding(b, task, cfg, &st); err != nil {
			st.Errors++
			slog.Warn("[codexcost] binding sweep failed", "binding", b.ID, "task", b.ArgusTaskID, "err", err)
		}
	}
	if st.Calls > 0 || st.Errors > 0 {
		slog.Info("[codexcost] sweep", "bindings", st.Bindings, "calls", st.Calls, "unpriced", st.Unpriced,
			"usd", fmt.Sprintf("%.4f", st.USD), "ambiguous", st.Ambiguous, "errors", st.Errors)
	}
	return st
}

func (s *Sweeper) sweepBinding(b db.CostSweepBinding, task *model.Task, cfg config.Config, st *Stats) error {
	stateDB, err := s.StateDB(task, cfg)
	if err != nil {
		return fmt.Errorf("codex state db: %w", err)
	}
	paths, err := Rollouts(stateDB, b.WorktreePath)
	if err != nil {
		return err
	}
	if len(paths) == 0 {
		return nil
	}
	table, err := s.Rates(task)
	if err != nil {
		return fmt.Errorf("rate table: %w", err)
	}
	cursors, err := s.Store.CodexCostCursors(b.ID)
	if err != nil {
		return err
	}
	for _, path := range paths {
		cur, ok := cursors[path]
		if !ok {
			cur = db.CodexCostCursor{RolloutPath: path, LastTotal: -1}
		}
		if err := s.sweepRollout(b, table, cur, st); err != nil {
			return fmt.Errorf("rollout %s: %w", filepath.Base(path), err)
		}
	}
	return nil
}

func (s *Sweeper) sweepRollout(b db.CostSweepBinding, table *pricing.Table, cur db.CodexCostCursor, st *Stats) error {
	f, err := os.Open(cur.RolloutPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // archived/deleted by Codex; nothing more to read
		}
		return err
	}
	defer f.Close() //nolint:errcheck // read-only fd
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if info.Size() <= cur.Offset {
		return nil
	}
	if _, err := f.Seek(cur.Offset, 0); err != nil {
		return err
	}
	calls, next, err := Parse(f, cur)
	if err != nil {
		return err
	}
	if next.Offset == cur.Offset {
		return nil
	}
	var delta db.HeraBindingCostTotals
	for _, c := range calls {
		if !inWindow(c.At, b.HeraBinding) {
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
		return err
	}
	st.USD += delta.CostUSDAccrued
	return nil
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

// Rollouts returns the rollout file of every Codex thread recorded with cwd
// in the thread index at stateDB — the worker's own threads plus any Codex
// sub-agent threads it spawned there. A missing index yields none.
func Rollouts(stateDB, cwd string) ([]string, error) {
	if _, err := os.Stat(stateDB); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	conn, err := sql.Open("sqlite", "file:"+stateDB+"?mode=ro")
	if err != nil {
		return nil, fmt.Errorf("open codex thread index: %w", err)
	}
	defer conn.Close() //nolint:errcheck // read-only handle
	rows, err := conn.Query(`SELECT rollout_path FROM threads WHERE cwd = ? AND rollout_path != '' ORDER BY created_at, id`, cwd)
	if err != nil {
		return nil, fmt.Errorf("query codex thread index: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, fmt.Errorf("scan codex thread index: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
