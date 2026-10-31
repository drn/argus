package codexcost

import (
	"database/sql"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/pricing"
	"github.com/drn/argus/internal/testutil"
)

// fixture is one sweep test's world: an in-memory argus DB, a fake Codex
// thread index, and rollout files written under a temp dir.
type fixture struct {
	t       *testing.T
	d       *db.DB
	dir     string
	stateDB string
	orch    *db.HeraOrchestrator
	table   *pricing.Table
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	d, err := db.OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })
	testutil.NoError(t, d.SetBackend("codex", config.Backend{Command: "codex"}))
	testutil.NoError(t, d.SetBackend("claude", config.Backend{Command: "claude"}))
	orch, err := d.CreateHeraOrchestrator("o", "")
	testutil.NoError(t, err)

	dir := t.TempDir()
	stateDB := filepath.Join(dir, "state_5.sqlite")
	conn, err := sql.Open("sqlite", stateDB)
	testutil.NoError(t, err)
	_, err = conn.Exec(`CREATE TABLE threads (id TEXT PRIMARY KEY, rollout_path TEXT NOT NULL, created_at INTEGER NOT NULL, cwd TEXT NOT NULL, source TEXT NOT NULL DEFAULT 'cli')`)
	testutil.NoError(t, err)
	testutil.NoError(t, conn.Close())

	return &fixture{t: t, d: d, dir: dir, stateDB: stateDB, orch: orch, table: &pricing.Table{Models: map[string]pricing.Rate{
		"gpt-a": {Input: 2, CacheRead: 0.1, CacheWrite5m: 2.5, Output: 10},
		"gpt-tier": {Input: 2, CacheRead: 0.1, Output: 10, LongContextThreshold: 272_000,
			LongContext: &pricing.Rate{Input: 4, CacheRead: 0.2, Output: 15}},
	}}}
}

func (f *fixture) sweeper() *Sweeper {
	return &Sweeper{
		Store:   f.d,
		StateDB: func(*model.Task, config.Config) (string, error) { return f.stateDB, nil },
		Rates:   func(string) (*pricing.Table, error) { return f.table, nil },
		Now:     time.Now,
	}
}

// worker creates a task on backend, a worker role, and a live binding.
func (f *fixture) worker(name, backend string) (*model.Task, *db.HeraBinding) {
	f.t.Helper()
	task := &model.Task{ID: "t-" + name, Name: name, Backend: backend, Worktree: "/w/" + name, Status: model.StatusInProgress}
	testutil.NoError(f.t, f.d.Add(task))
	role, err := f.d.CreateHeraRole(db.CreateHeraRoleInput{OrchestratorID: f.orch.ID, Name: name, Kind: db.HeraKindWorker, ArgusProject: "p"})
	testutil.NoError(f.t, err)
	return task, f.bind(role.ID, task)
}

func (f *fixture) bind(roleID int64, task *model.Task) *db.HeraBinding {
	f.t.Helper()
	b, err := f.d.CreateHeraBinding(db.CreateHeraBindingInput{RoleID: roleID, ArgusTaskID: task.ID, WorktreePath: task.Worktree})
	testutil.NoError(f.t, err)
	return b
}

// thread registers a Codex thread for cwd and returns its rollout path.
func (f *fixture) thread(id, cwd string) string {
	f.t.Helper()
	path := filepath.Join(f.dir, "rollout-"+id+".jsonl")
	conn, err := sql.Open("sqlite", f.stateDB)
	testutil.NoError(f.t, err)
	defer func() { _ = conn.Close() }()
	_, err = conn.Exec(`INSERT INTO threads(id, rollout_path, created_at, cwd) VALUES(?,?,?,?)`, id, path, time.Now().Unix(), cwd)
	testutil.NoError(f.t, err)
	return path
}

// rollout builds rollout lines: model switches and calls with a running total.
type rollout struct {
	b     strings.Builder
	total int64
}

func (r *rollout) model(m string) *rollout {
	fmt.Fprintf(&r.b, `{"timestamp":"%s","type":"turn_context","payload":{"model":%q}}`+"\n", time.Now().UTC().Format(time.RFC3339Nano), m)
	return r
}

func (r *rollout) call(at time.Time, input, cached, output int64) *rollout {
	r.total += input + output
	fmt.Fprintf(&r.b, `{"timestamp":"%s","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"total_tokens":%d},"last_token_usage":{"input_tokens":%d,"cached_input_tokens":%d,"cache_write_input_tokens":0,"output_tokens":%d,"total_tokens":%d}}}}`+"\n",
		at.UTC().Format(time.RFC3339Nano), r.total, input, cached, output, input+output)
	return r
}

func (f *fixture) write(path string, r *rollout) {
	f.t.Helper()
	testutil.NoError(f.t, os.WriteFile(path, []byte(r.b.String()), 0o644))
}

func (f *fixture) totals(bindingID int64) db.HeraBindingCostTotals {
	f.t.Helper()
	got, err := f.d.GetHeraBindingCostTotals(bindingID)
	testutil.NoError(f.t, err)
	return *got
}

func approx(t *testing.T, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestSweep_AccruesIncrementallyAndIdempotently(t *testing.T) {
	f := newFixture(t)
	_, b := f.worker("w", "codex")
	path := f.thread("main", "/w/w")
	soon := time.Now().Add(time.Minute)
	r := (&rollout{}).model("gpt-a").call(soon, 1_000_000, 600_000, 100_000)
	f.write(path, r)

	st := f.sweeper().Sweep()
	testutil.Equal(t, st.Bindings, 1)
	testutil.Equal(t, st.Calls, 1)
	// 400K uncached × $2 + 600K cached × $0.10 + 100K output × $10 = $0.80 + $0.06 + $1.00
	got := f.totals(b.ID)
	approx(t, got.CostUSDAccrued, 1.86)
	testutil.Equal(t, got.TokensInput, int64(400_000))
	testutil.Equal(t, got.TokensCacheRead, int64(600_000))
	testutil.Equal(t, got.TokensOutput, int64(100_000))

	t.Run("unchanged rollout adds nothing", func(t *testing.T) {
		st := f.sweeper().Sweep()
		testutil.Equal(t, st.Calls, 0)
		approx(t, f.totals(b.ID).CostUSDAccrued, 1.86)
	})

	t.Run("appended call accrues only the new call", func(t *testing.T) {
		r.call(soon.Add(time.Second), 1_000_000, 1_000_000, 0)
		f.write(path, r)
		st := f.sweeper().Sweep()
		testutil.Equal(t, st.Calls, 1)
		approx(t, f.totals(b.ID).CostUSDAccrued, 1.86+0.10)
	})
}

func TestSweep_IncludesSubAgentThreads(t *testing.T) {
	f := newFixture(t)
	_, b := f.worker("w", "codex")
	soon := time.Now().Add(time.Minute)
	f.write(f.thread("main", "/w/w"), (&rollout{}).model("gpt-a").call(soon, 100, 0, 0))
	f.write(f.thread("sub", "/w/w"), (&rollout{}).model("gpt-a").call(soon, 200, 0, 0))
	f.write(f.thread("other", "/w/elsewhere"), (&rollout{}).model("gpt-a").call(soon, 999, 0, 0))

	st := f.sweeper().Sweep()
	testutil.Equal(t, st.Calls, 2)
	testutil.Equal(t, f.totals(b.ID).TokensInput, int64(300))
}

func TestSweep_LongContextCallUsesTier(t *testing.T) {
	f := newFixture(t)
	_, b := f.worker("w", "codex")
	f.write(f.thread("main", "/w/w"), (&rollout{}).model("gpt-tier").call(time.Now().Add(time.Minute), 1_000_000, 0, 0))
	f.sweeper().Sweep()
	approx(t, f.totals(b.ID).CostUSDAccrued, 4.0)
}

func TestSweep_UnpricedModelAdvancesTokensOnly(t *testing.T) {
	f := newFixture(t)
	_, b := f.worker("w", "codex")
	path := f.thread("main", "/w/w")
	r := (&rollout{}).model("gpt-unknown").call(time.Now().Add(time.Minute), 500, 0, 5)
	f.write(path, r)

	st := f.sweeper().Sweep()
	testutil.Equal(t, st.Unpriced, 1)
	got := f.totals(b.ID)
	testutil.Equal(t, got.TokensInput, int64(500))
	testutil.Equal(t, got.CostUSDAccrued, 0.0)

	// Adding the rate later never prices the already-observed call.
	f.table.Models["gpt-unknown"] = pricing.Rate{Input: 1000}
	f.sweeper().Sweep()
	testutil.Equal(t, f.totals(b.ID).CostUSDAccrued, 0.0)
}

func TestSweep_SkipsNonCodexAndAmbiguousBindings(t *testing.T) {
	f := newFixture(t)
	_, claude := f.worker("c", "claude")
	f.write(f.thread("c", "/w/c"), (&rollout{}).model("gpt-a").call(time.Now().Add(time.Minute), 100, 0, 0))

	dualTask, dual := f.worker("d", "codex")
	other, err := f.d.CreateHeraOrchestrator("o2", "")
	testutil.NoError(t, err)
	role2, err := f.d.CreateHeraRole(db.CreateHeraRoleInput{OrchestratorID: other.ID, Name: "d2", Kind: db.HeraKindCoordinator, ArgusProject: "p"})
	testutil.NoError(t, err)
	f.bind(role2.ID, dualTask)
	f.write(f.thread("d", "/w/d"), (&rollout{}).model("gpt-a").call(time.Now().Add(time.Minute), 100, 0, 0))

	st := f.sweeper().Sweep()
	testutil.Equal(t, st.Bindings, 2)  // the dual task's two bindings; never the Claude one
	testutil.Equal(t, st.Ambiguous, 2) // the one call, seen from each binding
	testutil.Equal(t, st.Calls, 0)
	testutil.Equal(t, f.totals(claude.ID).TokensInput, int64(0))
	testutil.Equal(t, f.totals(dual.ID).TokensInput, int64(0))
}

func TestSweep_RebindingDoesNotDoubleCount(t *testing.T) {
	f := newFixture(t)
	task, first := f.worker("w", "codex")
	time.Sleep(5 * time.Millisecond)
	testutil.NoError(t, f.d.EndHeraBinding(first.ID, "test"))
	time.Sleep(5 * time.Millisecond)
	second := f.bind(first.RoleID, task)

	bindings, err := f.d.ListCostSweepBindings(time.Now().Add(-time.Hour))
	testutil.NoError(t, err)
	var ended time.Time
	for _, b := range bindings {
		if b.ID == first.ID {
			ended = *b.EndedAt
		}
	}
	during := first.StartedAt.Add(ended.Sub(first.StartedAt) / 2)
	f.write(f.thread("main", "/w/w"), (&rollout{}).model("gpt-a").
		call(during, 100, 0, 0).
		call(second.StartedAt.Add(time.Second), 7, 0, 0))

	f.sweeper().Sweep()
	testutil.Equal(t, f.totals(first.ID).TokensInput, int64(100))
	testutil.Equal(t, f.totals(second.ID).TokensInput, int64(7))
}

func TestSweep_FailuresAreIsolated(t *testing.T) {
	t.Run("state db error counts and continues", func(t *testing.T) {
		f := newFixture(t)
		f.worker("a", "codex")
		s := f.sweeper()
		s.StateDB = func(*model.Task, config.Config) (string, error) { return "", errors.New("no account") }
		testutil.Equal(t, s.Sweep().Errors, 1)
	})
	t.Run("rate table error counts", func(t *testing.T) {
		f := newFixture(t)
		f.worker("a", "codex")
		f.write(f.thread("a", "/w/a"), (&rollout{}).model("gpt-a").call(time.Now().Add(time.Minute), 1, 0, 0))
		s := f.sweeper()
		s.Rates = func(string) (*pricing.Table, error) { return nil, errors.New("bad toml") }
		testutil.Equal(t, s.Sweep().Errors, 1)
	})
	t.Run("missing thread index and missing rollout are quiet", func(t *testing.T) {
		f := newFixture(t)
		f.worker("a", "codex")
		f.thread("gone", "/w/a") // registered, never written
		testutil.Equal(t, f.sweeper().Sweep().Errors, 0)
		s := f.sweeper()
		s.StateDB = func(*model.Task, config.Config) (string, error) { return filepath.Join(f.dir, "nope.sqlite"), nil }
		testutil.Equal(t, s.Sweep().Errors, 0)
	})
	t.Run("deleted task falls back to the default Codex home", func(t *testing.T) {
		f := newFixture(t)
		task, b := f.worker("a", "codex")
		time.Sleep(5 * time.Millisecond)
		during := time.Now()
		time.Sleep(5 * time.Millisecond)
		f.write(f.thread("a", "/w/a"), (&rollout{}).model("gpt-a").call(during, 9, 0, 0))
		testutil.NoError(t, f.d.Delete(task.ID)) // ends the binding; the row stays
		var gotTask *model.Task
		s := f.sweeper()
		s.StateDB = func(task *model.Task, _ config.Config) (string, error) { gotTask = task; return f.stateDB, nil }
		st := s.Sweep()
		testutil.Equal(t, st.Errors, 0)
		testutil.Equal(t, gotTask.Account, "")
		testutil.Equal(t, f.totals(b.ID).TokensInput, int64(9))
	})
}

func TestThreadIndex_CorruptIndexErrors(t *testing.T) {
	bad := filepath.Join(t.TempDir(), "state_5.sqlite")
	testutil.NoError(t, os.WriteFile(bad, []byte("not a sqlite file"), 0o644))
	_, err := ThreadIndex(bad)
	testutil.True(t, err != nil)
}

func TestNew_DefaultSeams(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_SQLITE_HOME", "")
	d, err := db.OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })
	s := New(d)

	path, err := s.StateDB(&model.Task{ID: "t"}, d.Config())
	testutil.NoError(t, err)
	testutil.True(t, strings.HasSuffix(path, "state_5.sqlite"))

	table, err := s.Rates("")
	testutil.NoError(t, err)
	_, ok := table.Models["gpt-6.1-sol"]
	testutil.True(t, ok)
	_, err = os.Stat(filepath.Join(home, ".argus", "rates.toml"))
	testutil.NoError(t, err)
	testutil.Equal(t, s.Sweep().Errors, 0)
}

// faultyStore wraps a real store and fails selected calls.
type faultyStore struct {
	Store
	listErr, cursorErr, accrueErr error
}

func (s faultyStore) ListCostSweepBindings(t time.Time) ([]db.HeraBinding, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	return s.Store.ListCostSweepBindings(t)
}

func (s faultyStore) CodexCostCursors(id int64) (map[string]db.CodexCostCursor, error) {
	if s.cursorErr != nil {
		return nil, s.cursorErr
	}
	return s.Store.CodexCostCursors(id)
}

func (s faultyStore) AccrueCodexCalls(id int64, d db.HeraBindingCostTotals, c db.CodexCostCursor) error {
	if s.accrueErr != nil {
		return s.accrueErr
	}
	return s.Store.AccrueCodexCalls(id, d, c)
}

func TestSweep_StoreFailures(t *testing.T) {
	boom := errors.New("boom")
	for _, tc := range []struct {
		name  string
		store func(Store) Store
	}{
		{"list", func(s Store) Store { return faultyStore{Store: s, listErr: boom} }},
		{"cursors", func(s Store) Store { return faultyStore{Store: s, cursorErr: boom} }},
		{"accrue", func(s Store) Store { return faultyStore{Store: s, accrueErr: boom} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			f.worker("a", "codex")
			f.write(f.thread("a", "/w/a"), (&rollout{}).model("gpt-a").call(time.Now().Add(time.Minute), 1, 0, 0))
			s := f.sweeper()
			s.Store = tc.store(s.Store)
			testutil.Equal(t, s.Sweep().Errors, 1)
		})
	}
}

func TestSweep_UnresolvableBackendIsSkipped(t *testing.T) {
	f := newFixture(t)
	f.worker("a", "no-such-backend")
	testutil.Equal(t, f.sweeper().Sweep().Bindings, 0)
}

func TestSweep_UnreadableRolloutCounts(t *testing.T) {
	f := newFixture(t)
	f.worker("a", "codex")
	path := f.thread("a", "/w/a")
	testutil.NoError(t, os.MkdirAll(path, 0o755)) // a directory where the rollout should be
	testutil.Equal(t, f.sweeper().Sweep().Errors, 1)
}

func TestNew_UnknownAccountErrors(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	d, err := db.OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })
	_, err = New(d).StateDB(&model.Task{ID: "t", Account: "missing"}, d.Config())
	testutil.True(t, err != nil)
}

func TestThreadIndex_StatErrorSurfaces(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	testutil.NoError(t, os.WriteFile(file, nil, 0o644))
	_, err := ThreadIndex(filepath.Join(file, "state_5.sqlite"))
	testutil.True(t, err != nil)
}

// endedBinding creates a codex worker whose binding has ended, and returns a
// timestamp inside its window.
func (f *fixture) endedBinding(name string) (*model.Task, *db.HeraBinding, time.Time) {
	f.t.Helper()
	task, b := f.worker(name, "codex")
	time.Sleep(5 * time.Millisecond)
	testutil.NoError(f.t, f.d.EndHeraBinding(b.ID, "test"))
	bindings, err := f.d.ListCostSweepBindings(time.Now().Add(-time.Hour))
	testutil.NoError(f.t, err)
	for _, x := range bindings {
		if x.ID == b.ID {
			return task, b, b.StartedAt.Add(x.EndedAt.Sub(b.StartedAt) / 2)
		}
	}
	f.t.Fatal("ended binding not listed")
	return nil, nil, time.Time{}
}

func TestSweep_BackfillsStaleBindingsOnce(t *testing.T) {
	f := newFixture(t)
	_, b, during := f.endedBinding("old")
	path := f.thread("old", "/w/old")
	r := (&rollout{}).model("gpt-a").call(during, 100, 0, 0)
	f.write(path, r)
	later := func() time.Time { return time.Now().Add(time.Hour) } // the binding ended well before the grace window

	s := f.sweeper()
	s.Now = later
	st := s.Sweep()
	testutil.Equal(t, st.Backfilled, 1)
	testutil.Equal(t, f.totals(b.ID).TokensInput, int64(100))

	t.Run("later sweeps on the same sweeper skip stale bindings", func(t *testing.T) {
		r.call(during, 50, 0, 0)
		f.write(path, r)
		st := s.Sweep()
		testutil.Equal(t, st.Bindings, 0)
		testutil.Equal(t, f.totals(b.ID).TokensInput, int64(100))
	})

	t.Run("a fresh sweeper's backfill skips an already-read binding", func(t *testing.T) {
		fresh := f.sweeper()
		fresh.Now = later
		st := fresh.Sweep()
		testutil.Equal(t, st.Backfilled, 0)
		testutil.Equal(t, f.totals(b.ID).TokensInput, int64(100))
	})
}

func TestSweep_SkipsCallsMadeWhileDualBound(t *testing.T) {
	f := newFixture(t)
	task, b := f.worker("dual", "codex")
	// The same task bound under a second orchestrator while the first binding
	// is still live, so their windows overlap once the first ends.
	other, err := f.d.CreateHeraOrchestrator("o2", "")
	testutil.NoError(t, err)
	role2, err := f.d.CreateHeraRole(db.CreateHeraRoleInput{OrchestratorID: other.ID, Name: "dual2", Kind: db.HeraKindCoordinator, ArgusProject: "p"})
	testutil.NoError(t, err)
	f.bind(role2.ID, task)
	time.Sleep(5 * time.Millisecond)
	during := time.Now()
	time.Sleep(5 * time.Millisecond)
	testutil.NoError(t, f.d.EndHeraBinding(b.ID, "test"))
	f.write(f.thread("dual", "/w/dual"), (&rollout{}).model("gpt-a").call(during, 100, 0, 0))

	s := f.sweeper()
	s.Now = func() time.Time { return time.Now().Add(time.Hour) }
	st := s.Sweep()
	testutil.Equal(t, st.Ambiguous, 2) // the one call, seen from each binding
	testutil.Equal(t, st.Calls, 0)
	testutil.Equal(t, f.totals(b.ID).TokensInput, int64(0))
}

func TestSweep_BackfillOfDeletedRolloutIsNotCounted(t *testing.T) {
	f := newFixture(t)
	f.endedBinding("gone")
	f.thread("gone", "/w/gone") // registered in the index, file never written
	s := f.sweeper()
	s.Now = func() time.Time { return time.Now().Add(time.Hour) }
	st := s.Sweep()
	testutil.Equal(t, st.Backfilled, 0)
	testutil.Equal(t, st.Errors, 0)
}
