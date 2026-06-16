package db

// railStateConfigKey is the single config-table key under which the Hera rail's
// UI state (fold/collapse + selection) is persisted as a JSON blob. The rail
// owns the serialization shape; the DB layer only stores the opaque string.
const railStateConfigKey = "hera.rail_view_state"

// LoadRailState returns the persisted Hera rail UI-state blob, or "" (no error)
// when none has been saved. Thin wrapper over the config key-value table so the
// rail's persistence seam (internal/tui/hera.RailStateStore) stays DB-agnostic.
func (d *DB) LoadRailState() (string, error) {
	return d.GetConfigValue(railStateConfigKey)
}

// SaveRailState persists the Hera rail UI-state blob (opaque JSON owned by the
// rail). Survives daemon restart / crash because it lives in the on-disk config
// table of ~/.argus/data.sql.
func (d *DB) SaveRailState(state string) error {
	return d.SetConfigValue(railStateConfigKey, state)
}
