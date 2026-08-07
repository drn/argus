package db

import (
	"database/sql"
	"errors"
	"fmt"
)

// HeraBindingCostTotals is one binding's accrued token/cost state
// (add-coordinator-cost-estimate). The five raw fields are a full-resum
// overwrite target (design.md Decision 1); CostUSDAccrued is a SEPARATE
// incrementally-accumulated dollar total, priced at accrual time against
// the rate table as it stood at that moment (Decision 2) — callers must
// never recompute it from a later rate table.
type HeraBindingCostTotals struct {
	TokensInput        int64
	TokensCacheWrite1h int64
	TokensCacheWrite5m int64
	TokensCacheRead    int64
	TokensOutput       int64
	CostUSDAccrued     float64
}

// GetHeraBindingCostTotals reads a binding's current accrual state by
// binding id. Returns ErrHeraNotFound if the binding doesn't exist. A
// binding that has never accrued anything reads back the zero value (see
// design.md Decision 6 — zero-across-all-columns is the "unmeasured"
// signal, not backfilled or defaulted differently here).
func (d *DB) GetHeraBindingCostTotals(bindingID int64) (*HeraBindingCostTotals, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	row := d.conn.QueryRow(
		`SELECT tokens_input, tokens_cache_write_1h, tokens_cache_write_5m, tokens_cache_read, tokens_output, cost_usd_accrued
		 FROM hera_bindings WHERE id=?`, bindingID)
	var t HeraBindingCostTotals
	if err := row.Scan(&t.TokensInput, &t.TokensCacheWrite1h, &t.TokensCacheWrite5m, &t.TokensCacheRead, &t.TokensOutput, &t.CostUSDAccrued); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrHeraNotFound
		}
		return nil, fmt.Errorf("get hera binding cost totals: %w", err)
	}
	return &t, nil
}

// UpdateHeraBindingCostTotals overwrites a binding's raw token totals and
// cost_usd_accrued together in one atomic statement — the read-modify-write
// contract design.md Decision 2 requires: raw totals are the caller's fresh
// full-resum, CostUSDAccrued is the caller's already-computed NEW accrued
// total (previous + this call's priced delta), not a value this function
// derives itself. Returns ErrHeraNotFound if the binding doesn't exist.
func (d *DB) UpdateHeraBindingCostTotals(bindingID int64, t HeraBindingCostTotals) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	res, err := d.conn.Exec(
		`UPDATE hera_bindings SET tokens_input=?, tokens_cache_write_1h=?, tokens_cache_write_5m=?, tokens_cache_read=?, tokens_output=?, cost_usd_accrued=?
		 WHERE id=?`,
		t.TokensInput, t.TokensCacheWrite1h, t.TokensCacheWrite5m, t.TokensCacheRead, t.TokensOutput, t.CostUSDAccrued, bindingID)
	if err != nil {
		return fmt.Errorf("update hera binding cost totals: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("update hera binding cost totals: rows affected: %w", err)
	}
	if n == 0 {
		return ErrHeraNotFound
	}
	return nil
}

// SumHeraRoleCostAccrued sums cost_usd_accrued across every binding
// (live and ended alike) for roleID — pure addition of already-priced
// values, no rate-table lookup (design.md Decision 2: a role can be
// re-bound/recycled, so its lifetime cost spans every incarnation). Zero
// for a role with no bindings or no accrued cost; never an error for that
// case.
func (d *DB) SumHeraRoleCostAccrued(roleID int64) (float64, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	var sum float64
	row := d.conn.QueryRow(`SELECT COALESCE(SUM(cost_usd_accrued), 0) FROM hera_bindings WHERE role_id=?`, roleID)
	if err := row.Scan(&sum); err != nil {
		return 0, fmt.Errorf("sum hera role cost accrued: %w", err)
	}
	return sum, nil
}

// SumHeraRoleCostAccruedByOrchestrator is the bulk, one-query-per-orchestrator
// twin of SumHeraRoleCostAccrued — a role_id → total map covering every role
// (any kind, including nuked ones — callers reading the model's non-nuked
// role list simply never look those entries up) under orchID, in one round
// trip, matching the existing "one bulk read per orchestrator" convention
// BuildModel already follows for e.g. ListHeraBlocks. A role with no
// bindings or no accrued cost is simply absent from the map — callers treat
// a missing key as 0, not an error.
func (d *DB) SumHeraRoleCostAccruedByOrchestrator(orchID int64) (map[int64]float64, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	rows, err := d.conn.Query(
		`SELECT hr.id, COALESCE(SUM(hb.cost_usd_accrued), 0)
		 FROM hera_roles hr
		 LEFT JOIN hera_bindings hb ON hb.role_id = hr.id
		 WHERE hr.orchestrator_id = ?
		 GROUP BY hr.id`, orchID)
	if err != nil {
		return nil, fmt.Errorf("sum hera role cost accrued by orchestrator: %w", err)
	}
	defer rows.Close()
	out := make(map[int64]float64)
	for rows.Next() {
		var roleID int64
		var sum float64
		if err := rows.Scan(&roleID, &sum); err != nil {
			return nil, fmt.Errorf("scan hera role cost accrued: %w", err)
		}
		out[roleID] = sum
	}
	return out, rows.Err()
}

// SumHeraRoleRawTokensByOrchestrator is the raw-token twin of
// SumHeraRoleCostAccruedByOrchestrator: a role_id → HeraBindingCostTotals
// map (CostUSDAccrued left at 0 here — callers wanting cost use the other
// bulk function) summing all five raw columns across every one of each
// role's bindings under orchID, in one round trip. Used by GET /api/hera to
// expose the raw per-rate-class breakdown alongside the blended cost_usd
// figure (design.md Decision 7: the breakdown stays REST-exposed even
// though the TUI renders only the blended total).
func (d *DB) SumHeraRoleRawTokensByOrchestrator(orchID int64) (map[int64]HeraBindingCostTotals, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	rows, err := d.conn.Query(
		`SELECT hr.id,
		        COALESCE(SUM(hb.tokens_input), 0),
		        COALESCE(SUM(hb.tokens_cache_write_1h), 0),
		        COALESCE(SUM(hb.tokens_cache_write_5m), 0),
		        COALESCE(SUM(hb.tokens_cache_read), 0),
		        COALESCE(SUM(hb.tokens_output), 0)
		 FROM hera_roles hr
		 LEFT JOIN hera_bindings hb ON hb.role_id = hr.id
		 WHERE hr.orchestrator_id = ?
		 GROUP BY hr.id`, orchID)
	if err != nil {
		return nil, fmt.Errorf("sum hera role raw tokens by orchestrator: %w", err)
	}
	defer rows.Close()
	out := make(map[int64]HeraBindingCostTotals)
	for rows.Next() {
		var roleID int64
		var t HeraBindingCostTotals
		if err := rows.Scan(&roleID, &t.TokensInput, &t.TokensCacheWrite1h, &t.TokensCacheWrite5m, &t.TokensCacheRead, &t.TokensOutput); err != nil {
			return nil, fmt.Errorf("scan hera role raw tokens: %w", err)
		}
		out[roleID] = t
	}
	return out, rows.Err()
}

// SumNukedHeraRolesCostByOrchestrator sums cost_usd_accrued across every
// binding belonging to a NUKED role under orchID — the deliberate,
// documented divergence from every other hera rollup (agent count,
// needs-input), which excludes nuked roles because they are torn down from
// every display. Money genuinely accrued by a since-nuked child must still
// count toward its (still-active) coordinator's subtree total (design.md
// Decision 4). Implemented as its own dedicated query, NOT a parameter
// threaded through ListHeraRoles — that function's nuked_at exclusion stays
// unconditional for every other caller.
func (d *DB) SumNukedHeraRolesCostByOrchestrator(orchID int64) (float64, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	var sum float64
	row := d.conn.QueryRow(
		`SELECT COALESCE(SUM(hb.cost_usd_accrued), 0)
		 FROM hera_bindings hb
		 JOIN hera_roles hr ON hb.role_id = hr.id
		 WHERE hr.orchestrator_id = ? AND hr.nuked_at IS NOT NULL`, orchID)
	if err := row.Scan(&sum); err != nil {
		return 0, fmt.Errorf("sum nuked hera roles cost by orchestrator: %w", err)
	}
	return sum, nil
}
