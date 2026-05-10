package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/drn/argus/internal/testutil"
)

func TestDiscoverVaultsIn(t *testing.T) {
	t.Run("discovers child vaults with .obsidian subdir", func(t *testing.T) {
		base := t.TempDir()
		os.MkdirAll(filepath.Join(base, "MyVault", ".obsidian"), 0o755)

		got := discoverVaultsIn(base)
		testutil.DeepEqual(t, got, []string{filepath.Join(base, "MyVault")})
	})

	t.Run("discovers root vault", func(t *testing.T) {
		base := t.TempDir()
		os.MkdirAll(filepath.Join(base, ".obsidian"), 0o755)

		got := discoverVaultsIn(base)
		testutil.DeepEqual(t, got, []string{base})
	})

	t.Run("discovers root and child vaults", func(t *testing.T) {
		base := t.TempDir()
		os.MkdirAll(filepath.Join(base, ".obsidian"), 0o755)
		os.MkdirAll(filepath.Join(base, "Argus", ".obsidian"), 0o755)
		os.MkdirAll(filepath.Join(base, "Metis", ".obsidian"), 0o755)

		got := discoverVaultsIn(base)
		testutil.DeepEqual(t, got, []string{
			base,
			filepath.Join(base, "Argus"),
			filepath.Join(base, "Metis"),
		})
	})

	t.Run("skips dirs without .obsidian", func(t *testing.T) {
		base := t.TempDir()
		os.MkdirAll(filepath.Join(base, "NotAVault"), 0o755)
		os.MkdirAll(filepath.Join(base, "IsAVault", ".obsidian"), 0o755)

		got := discoverVaultsIn(base)
		testutil.DeepEqual(t, got, []string{filepath.Join(base, "IsAVault")})
	})

	t.Run("skips hidden directories", func(t *testing.T) {
		base := t.TempDir()
		os.MkdirAll(filepath.Join(base, ".hidden", ".obsidian"), 0o755)
		os.MkdirAll(filepath.Join(base, "Visible", ".obsidian"), 0o755)

		got := discoverVaultsIn(base)
		testutil.DeepEqual(t, got, []string{filepath.Join(base, "Visible")})
	})

	t.Run("returns nil for nonexistent base", func(t *testing.T) {
		got := discoverVaultsIn("/nonexistent/path/that/does/not/exist")
		testutil.Nil(t, got)
	})

	t.Run("returns sorted results", func(t *testing.T) {
		base := t.TempDir()
		os.MkdirAll(filepath.Join(base, "Zebra", ".obsidian"), 0o755)
		os.MkdirAll(filepath.Join(base, "Alpha", ".obsidian"), 0o755)
		os.MkdirAll(filepath.Join(base, "Middle", ".obsidian"), 0o755)

		got := discoverVaultsIn(base)
		testutil.DeepEqual(t, got, []string{
			filepath.Join(base, "Alpha"),
			filepath.Join(base, "Middle"),
			filepath.Join(base, "Zebra"),
		})
	})

	t.Run("empty base directory returns nil", func(t *testing.T) {
		base := t.TempDir()

		got := discoverVaultsIn(base)
		testutil.Nil(t, got)
	})

	t.Run("skips files not directories", func(t *testing.T) {
		base := t.TempDir()
		os.WriteFile(filepath.Join(base, "file.md"), []byte("hi"), 0o644)
		os.MkdirAll(filepath.Join(base, "Vault", ".obsidian"), 0o755)

		got := discoverVaultsIn(base)
		testutil.DeepEqual(t, got, []string{filepath.Join(base, "Vault")})
	})
}

func TestDefaultMetisVaultPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("HOME not used on windows")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)

	got := DefaultMetisVaultPath()
	want := filepath.Join(home, "Library/Mobile Documents/iCloud~md~obsidian/Documents", "Metis")
	testutil.Equal(t, got, want)
}

func TestDiscoverICloudVaults(t *testing.T) {
	t.Run("returns nil when iCloud base does not exist", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		got := DiscoverICloudVaults()
		testutil.Nil(t, got)
	})

	t.Run("discovers vaults under the iCloud base", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("HOME not used on windows")
		}
		home := t.TempDir()
		t.Setenv("HOME", home)

		base := filepath.Join(home, "Library/Mobile Documents/iCloud~md~obsidian/Documents")
		if err := os.MkdirAll(filepath.Join(base, "Metis", ".obsidian"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(base, "Argus", ".obsidian"), 0o755); err != nil {
			t.Fatal(err)
		}

		got := DiscoverICloudVaults()
		testutil.DeepEqual(t, got, []string{
			filepath.Join(base, "Argus"),
			filepath.Join(base, "Metis"),
		})
	})
}
