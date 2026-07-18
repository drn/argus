package profiles

import (
	"fmt"
	"os"
	"path/filepath"
)

// InstallDefaults writes each embedded seed profile (SeedNames) into
// profilesDir — the per-user library, ~/.argus/profiles/ in production —
// that isn't already present at the destination. An existing file is never
// overwritten (an operator may have customized it); its name is reported in
// skipped instead. Installation is always explicit — callers decide when to
// invoke this; it is never run automatically.
func InstallDefaults(profilesDir string) (installed []string, skipped []string, err error) {
	if err := os.MkdirAll(profilesDir, 0o755); err != nil {
		return nil, nil, fmt.Errorf("creating profiles dir: %w", err)
	}
	for _, name := range SeedNames {
		dest := filepath.Join(profilesDir, name+".toml")
		if fileExists(dest) {
			skipped = append(skipped, name)
			continue
		}
		data, err := seedFS.ReadFile("seeds/" + name + ".toml")
		if err != nil {
			return installed, skipped, fmt.Errorf("reading embedded seed %q: %w", name, err)
		}
		if err := os.WriteFile(dest, data, 0o644); err != nil {
			return installed, skipped, fmt.Errorf("writing profile %q: %w", name, err)
		}
		installed = append(installed, name)
	}
	return installed, skipped, nil
}
