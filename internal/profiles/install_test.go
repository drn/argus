package profiles

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/testutil"
)

func TestInstallDefaults_EmptyDirInstallsAll(t *testing.T) {
	dir := t.TempDir()
	installed, skipped, err := InstallDefaults(dir)
	testutil.NoError(t, err)
	testutil.DeepEqual(t, installed, SeedNames)
	testutil.Equal(t, len(skipped), 0)

	for _, name := range SeedNames {
		if !fileExists(filepath.Join(dir, name+".toml")) {
			t.Errorf("expected %s.toml to be written", name)
		}
	}
}

func TestInstallDefaults_SkipsExistingWithoutOverwriting(t *testing.T) {
	dir := t.TempDir()
	custom := "# customized by the operator\n[archetype.code_slice]\nmodel = \"opus\"\n"
	writeProfile(t, dir, "default", custom)

	installed, skipped, err := InstallDefaults(dir)
	testutil.NoError(t, err)
	testutil.DeepEqual(t, skipped, []string{"default"})
	testutil.DeepEqual(t, installed, []string{"lean", "customer_grade"})

	got, err := os.ReadFile(filepath.Join(dir, "default.toml"))
	testutil.NoError(t, err)
	testutil.Equal(t, string(got), custom)
}

func TestInstallDefaults_CreatesMissingDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "profiles")
	installed, _, err := InstallDefaults(dir)
	testutil.NoError(t, err)
	testutil.DeepEqual(t, installed, SeedNames)
}

func TestInstallDefaults_SecondRunSkipsEverything(t *testing.T) {
	dir := t.TempDir()
	_, _, err := InstallDefaults(dir)
	testutil.NoError(t, err)

	installed, skipped, err := InstallDefaults(dir)
	testutil.NoError(t, err)
	testutil.Equal(t, len(installed), 0)
	testutil.DeepEqual(t, skipped, SeedNames)
}

// TestEmbeddedSeeds_EachValidates proves the bytes the embed directive
// actually captured (not merely the on-disk files) resolve and validate —
// extracting through seedFS (the same embed.FS InstallDefaults reads from)
// rather than reading internal/profiles/seeds/*.toml directly, so a future
// edit that breaks a seed's validity fails this test, not a silent runtime
// surprise.
func TestEmbeddedSeeds_EachValidates(t *testing.T) {
	dir := t.TempDir()
	for _, name := range SeedNames {
		data, err := seedFS.ReadFile("seeds/" + name + ".toml")
		testutil.NoError(t, err)
		testutil.NoError(t, os.WriteFile(filepath.Join(dir, name+".toml"), data, 0o644))
	}

	l := &Loader{LibraryDir: dir}
	for _, name := range SeedNames {
		t.Run(name, func(t *testing.T) {
			p, errs := l.ValidateName(name, config.Config{}, testKnownModels, nil)
			testutil.NotNil(t, p)
			if len(errs) != 0 {
				t.Fatalf("embedded seed %q failed validation: %s", name, errorsText(errs))
			}
		})
	}
}
