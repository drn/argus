package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/pricing"
	"github.com/drn/argus/internal/uxlog"
)

// heraTokensPutReq is the body PUT /api/tasks/{id}/hera/tokens accepts — the
// hook's freshly-resummed FULL raw totals (add-coordinator-cost-estimate),
// not a delta. The handler computes the delta itself against the binding's
// previously-persisted totals.
type heraTokensPutReq struct {
	TokensInput        int64 `json:"tokens_input"`
	TokensCacheWrite1h int64 `json:"tokens_cache_write_1h"`
	TokensCacheWrite5m int64 `json:"tokens_cache_write_5m"`
	TokensCacheRead    int64 `json:"tokens_cache_read"`
	TokensOutput       int64 `json:"tokens_output"`
}

// handleHeraTokensPut is the daemon-side half of accrual-time cost stamping
// (design.md Decision 2): it resolves task {id}'s currently-live hera
// binding, computes the delta between the newly-POSTed raw totals and the
// binding's previously-persisted ones, prices that delta against the rate
// table AS IT STANDS RIGHT NOW (never re-pricing any earlier delta), and
// persists the new raw totals plus the incremented cost_usd_accrued
// together. A model with no rate-table entry leaves cost_usd_accrued
// unchanged for this delta — permanently unpriced, per Decision 6's
// corollary — while raw totals still advance regardless.
func (s *Server) handleHeraTokensPut(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	task, err := s.db.Get(id)
	if err != nil {
		if errors.Is(err, db.ErrTaskNotFound) {
			writeErr(w, http.StatusNotFound, "", err)
			return
		}
		writeErr(w, http.StatusInternalServerError, "", err)
		return
	}

	var req heraTokensPutReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON", err)
		return
	}

	binding, err := s.db.HeraLiveBindingByTask(id)
	if err != nil {
		switch {
		case errors.Is(err, db.ErrHeraNotFound):
			writeErr(w, http.StatusNotFound, "no live hera binding for task", err)
		case errors.Is(err, db.ErrHeraAmbiguous):
			// A task simultaneously live under 2+ orchestrators has no single
			// binding to attribute this usage to — skip rather than guess
			// (fail-soft; the hook's caller logs and moves on, mirroring how
			// every other coord-hook REST call degrades on error).
			writeErr(w, http.StatusConflict, "task has multiple live hera bindings; cannot disambiguate", err)
		default:
			writeErr(w, http.StatusInternalServerError, "", err)
		}
		return
	}

	current, err := s.db.GetHeraBindingCostTotals(binding.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "", err)
		return
	}

	deltas := pricing.TokenDeltas{
		Input:        clampDelta(req.TokensInput, current.TokensInput),
		CacheWrite1h: clampDelta(req.TokensCacheWrite1h, current.TokensCacheWrite1h),
		CacheWrite5m: clampDelta(req.TokensCacheWrite5m, current.TokensCacheWrite5m),
		CacheRead:    clampDelta(req.TokensCacheRead, current.TokensCacheRead),
		Output:       clampDelta(req.TokensOutput, current.TokensOutput),
	}

	newAccrued := current.CostUSDAccrued
	if appliedModel := s.heraAppliedModel(task); appliedModel != "" {
		table, err := s.heraRateTable(task)
		if err != nil {
			uxlog.Log("[api] load rate table failed for task %s, leaving delta unpriced: %v", id, err)
		} else if usd, ok := table.PriceDelta(appliedModel, deltas); ok {
			newAccrued += usd
		}
	}

	newTotals := db.HeraBindingCostTotals{
		TokensInput:        req.TokensInput,
		TokensCacheWrite1h: req.TokensCacheWrite1h,
		TokensCacheWrite5m: req.TokensCacheWrite5m,
		TokensCacheRead:    req.TokensCacheRead,
		TokensOutput:       req.TokensOutput,
		CostUSDAccrued:     newAccrued,
	}
	if err := s.db.UpdateHeraBindingCostTotals(binding.ID, newTotals); err != nil {
		writeErr(w, http.StatusInternalServerError, "", err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"cost_usd_accrued": newAccrued})
}

// clampDelta returns newTotal-prevTotal, clamped at 0. A transcript is
// append-only so newTotal should never be smaller than prevTotal (design.md
// Decision 1); clamping is a defensive guard against ever subtracting cost
// rather than an expected path.
func clampDelta(newTotal, prevTotal int64) int64 {
	if newTotal <= prevTotal {
		return 0
	}
	return newTotal - prevTotal
}

// heraAppliedModel resolves task's live model via the same
// agent.ResolveBackend + agent.ResolveModel pair
// internal/tui/hera_tiering.go's resolveHeraTier already uses, reused here
// rather than a new resolution path. A ResolveBackend failure yields the
// zero-value config.Backend rather than bailing out early: ResolveModel
// checks task.Model FIRST, before ever consulting backend, so an explicit
// per-task model override (the common case for a hera-spawned role) still
// resolves correctly even with no configured backend; only the
// profile-driven archetype fallback path needs a real backend, and that
// path already falls open to "" on its own.
func (s *Server) heraAppliedModel(task *model.Task) string {
	cfg := s.db.Config()
	backend, _ := agent.ResolveBackend(task, cfg)
	applied, _ := agent.ResolveModel(task, backend, cfg)
	return applied
}

// heraRateTable loads the rate table via the same in-repo-then-library
// precedence diligence profiles use (design.md Decision 3), installing the
// embedded default to the library path first if absent.
func (s *Server) heraRateTable(task *model.Task) (*pricing.Table, error) {
	libraryPath := filepath.Join(db.DataDir(), "rates.toml")
	if _, err := pricing.InstallDefault(libraryPath); err != nil {
		return nil, err
	}
	loader := &pricing.Loader{LibraryPath: libraryPath}
	if task.Worktree != "" {
		loader.RepoPath = filepath.Join(task.Worktree, ".argus", "rates.toml")
	}
	return loader.Load()
}
