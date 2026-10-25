package db

import (
	"database/sql"
	"errors"
	"fmt"
	"math"
)

// AccrueHeraBridgeCost atomically advances the persisted session-cumulative
// baseline and immutable accrued total. A lower counter starts a new epoch.
func (d *DB) AccrueHeraBridgeCost(bindingID int64, session string, cost float64, generations ...string) error {
	generation := ""
	if len(generations) > 0 {
		generation = generations[0]
	}
	if session == "" || cost < 0 || math.IsNaN(cost) || math.IsInf(cost, 0) {
		return fmt.Errorf("invalid bridge cost")
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	tx, err := d.conn.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck // rollback after commit is harmless
	var previousSession string
	var previousGeneration string
	var source string
	var previous float64
	err = tx.QueryRow(`SELECT session_id, generation, cumulative, source FROM hera_bridge_cost WHERE binding_id=?`, bindingID).Scan(&previousSession, &previousGeneration, &previous, &source)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	delta := cost
	// A daemon bounce forgets retired mod boots. A late sample from the same
	// process must not look like a reset and bill its old total a second time.
	if generation != "" && generation == previousGeneration && session == previousSession && (cost < previous || (source == "transcript" && cost == previous)) {
		return tx.Commit()
	}
	if previousSession == session && cost >= previous {
		delta = cost - previous
	}
	// Sources cannot reconcile token-priced totals with engine USD exactly.
	// Baseline the first reading on handoff; missing a transition turn is
	// preferable to billing the same turn twice.
	if source != "" && source != "bridge" {
		delta = 0
	}
	res, err := tx.Exec(`UPDATE hera_bindings SET cost_usd_accrued=cost_usd_accrued+? WHERE id=?`, delta, bindingID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrHeraNotFound
	}
	_, err = tx.Exec(`INSERT INTO hera_bridge_cost(binding_id,session_id,generation,cumulative,source) VALUES(?,?,?,?,'bridge') ON CONFLICT(binding_id) DO UPDATE SET session_id=excluded.session_id,generation=excluded.generation,cumulative=excluded.cumulative,source='bridge'`, bindingID, session, generation, cost)
	if err != nil {
		return err
	}
	return tx.Commit()
}

// UpdateHeraTranscriptCost switches back to transcript accounting. The
// first transcript after bridge accounting establishes its raw-token offset
// without adding cost. The caller serializes this with measurement ingestion.
func (d *DB) UpdateHeraTranscriptCost(bindingID int64, totals HeraBindingCostTotals) (float64, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	tx, err := d.conn.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback() //nolint:errcheck // committed below
	var source string
	err = tx.QueryRow(`SELECT source FROM hera_bridge_cost WHERE binding_id=?`, bindingID).Scan(&source)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	if source == "bridge" {
		if err = tx.QueryRow(`SELECT cost_usd_accrued FROM hera_bindings WHERE id=?`, bindingID).Scan(&totals.CostUSDAccrued); err != nil {
			return 0, err
		}
	}
	res, err := tx.Exec(`UPDATE hera_bindings SET tokens_input=?, tokens_cache_write_1h=?, tokens_cache_write_5m=?, tokens_cache_read=?, tokens_output=?, cost_usd_accrued=? WHERE id=?`, totals.TokensInput, totals.TokensCacheWrite1h, totals.TokensCacheWrite5m, totals.TokensCacheRead, totals.TokensOutput, totals.CostUSDAccrued, bindingID)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	if n == 0 {
		return 0, ErrHeraNotFound
	}
	_, err = tx.Exec(`INSERT INTO hera_bridge_cost(binding_id,session_id,cumulative,source) VALUES(?,'',0,'transcript') ON CONFLICT(binding_id) DO UPDATE SET source='transcript'`, bindingID)
	if err != nil {
		return 0, err
	}
	return totals.CostUSDAccrued, tx.Commit()
}
