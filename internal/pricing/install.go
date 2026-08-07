package pricing

import (
	"fmt"
	"os"
	"path/filepath"
)

// InstallDefault writes the embedded seed rates.toml to dest — the per-user
// rate table, ~/.argus/rates.toml in production — if and only if dest does
// not already exist. An existing file is never overwritten (an operator may
// have hand-corrected pricing), mirroring profiles.InstallDefaults' contract
// exactly (internal/profiles/install.go). Unlike that function — invoked
// only explicitly, from a Settings UI action — callers of InstallDefault are
// expected to invoke it automatically and idempotently (e.g. at daemon
// startup): rate data is required infrastructure for the always-on Stop-hook
// accrual mechanism, not an opt-in customization a user deliberately turns
// on. installed reports whether a write actually happened.
func InstallDefault(dest string) (installed bool, err error) {
	if fileExists(dest) {
		return false, nil
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return false, fmt.Errorf("creating rates dir: %w", err)
	}
	data, err := seedFS.ReadFile(seedFileName)
	if err != nil {
		return false, fmt.Errorf("reading embedded seed: %w", err)
	}
	if err := os.WriteFile(dest, data, 0o644); err != nil {
		return false, fmt.Errorf("writing %s: %w", dest, err)
	}
	return true, nil
}
