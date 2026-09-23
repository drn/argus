package db

import (
	"fmt"

	"github.com/drn/argus/internal/config"
)

// BackendTiers returns the Settings-UI-edited backend-tier-routing list, in
// order. Consulted by db.Config only when config.toml defines no
// [[backend_routing.tier]] entries (see specs/config-management/spec.md's
// storage-precedence requirement) — this store is never merged with a
// config.toml-defined list.
func (d *DB) BackendTiers() ([]config.BackendTier, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	rows, err := d.conn.Query(`SELECT backend, probe, threshold_pct FROM backend_tiers ORDER BY position`)
	if err != nil {
		return nil, fmt.Errorf("query backend_tiers: %w", err)
	}
	defer rows.Close()

	var tiers []config.BackendTier
	for rows.Next() {
		var t config.BackendTier
		if err := rows.Scan(&t.Backend, &t.Probe, &t.ThresholdPct); err != nil {
			continue
		}
		tiers = append(tiers, t)
	}
	return tiers, nil
}

// SetBackendTiers replaces the entire Settings-UI-edited tier list with tiers,
// in the given order. Whole-list replacement (delete-then-insert inside one
// transaction), not a merge — mirroring the tier list's own
// config.toml-wins-wholesale semantics rather than backends.go's per-row
// upsert, since a reordered or shortened list has no stable per-row identity
// to upsert against.
func (d *DB) SetBackendTiers(tiers []config.BackendTier) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	tx, err := d.conn.Begin()
	if err != nil {
		return fmt.Errorf("begin backend_tiers tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	if _, err := tx.Exec(`DELETE FROM backend_tiers`); err != nil {
		return fmt.Errorf("clear backend_tiers: %w", err)
	}
	for i, t := range tiers {
		if _, err := tx.Exec(`INSERT INTO backend_tiers (position, backend, probe, threshold_pct) VALUES (?, ?, ?, ?)`,
			i, t.Backend, t.Probe, t.ThresholdPct); err != nil {
			return fmt.Errorf("insert backend_tiers[%d]: %w", i, err)
		}
	}
	return tx.Commit()
}

// BackendTiersFromConfigToml reports whether config.toml — not the DB — is
// the source of the active tier list Config() just returned (the Settings
// backend-tier category's read-only signal, per
// specs/settings-view/spec.md's config.toml-sourced-list requirement). Only
// meaningful immediately after a Config() call, which is what actually
// re-applies config.toml and refreshes the underlying FileLoader's cached
// finding; always false for an in-memory/test DB (nil cfgLoader), since there
// is no file that could be authoritative.
func (d *DB) BackendTiersFromConfigToml() bool {
	return d.cfgLoader.DefinesBackendRoutingTiers()
}
