package config

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// DiscoverICloudVaults scans the iCloud Obsidian base directory for vaults.
// A vault is identified by having a .obsidian/ subdirectory.
// Returns sorted absolute paths. Returns nil if the base directory does not exist.
func DiscoverICloudVaults() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	return discoverVaultsIn(filepath.Join(home, iCloudObsidianBase))
}

// discoverVaultsIn scans a base directory for Obsidian vaults. Checks the base
// itself and its immediate (non-hidden) children for a .obsidian/ subdirectory.
// Returns sorted absolute paths. Returns nil if base doesn't exist or has no vaults.
func discoverVaultsIn(base string) []string {
	entries, err := os.ReadDir(base)
	if err != nil {
		return nil
	}

	var vaults []string

	// Check if the base itself is a vault (the Documents root can be a vault
	// while also containing child vaults as subdirectories).
	if isVault(base) {
		vaults = append(vaults, base)
	}

	// Check immediate children.
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		child := filepath.Join(base, e.Name())
		if isVault(child) {
			vaults = append(vaults, child)
		}
	}

	if len(vaults) == 0 {
		return nil
	}
	sort.Strings(vaults)
	return vaults
}

// isVault returns true if dir contains a .obsidian/ subdirectory.
func isVault(dir string) bool {
	info, err := os.Stat(filepath.Join(dir, ".obsidian"))
	return err == nil && info.IsDir()
}
