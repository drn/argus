package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/drn/argus/internal/profiles"
	"github.com/drn/argus/internal/testutil"
)

func TestRunProfilesInstallDefaults_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	var b strings.Builder
	code := runProfilesInstallDefaults(&b, dir)
	testutil.Equal(t, code, 0)
	out := b.String()
	testutil.Contains(t, out, "installed:")
	for _, name := range profiles.SeedNames {
		testutil.Contains(t, out, name)
	}
}

func TestRunProfilesInstallDefaults_AllAlreadyPresent(t *testing.T) {
	dir := t.TempDir()
	_, _, err := profiles.InstallDefaults(dir)
	testutil.NoError(t, err)

	var b strings.Builder
	code := runProfilesInstallDefaults(&b, dir)
	testutil.Equal(t, code, 0)
	testutil.Contains(t, b.String(), "already present")
	testutil.Contains(t, b.String(), "nothing to do")
}

func TestRunProfilesInstallDefaults_PartialInstallReportsBoth(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "default.toml"), []byte("# custom\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var b strings.Builder
	code := runProfilesInstallDefaults(&b, dir)
	testutil.Equal(t, code, 0)
	out := b.String()
	testutil.Contains(t, out, "installed:")
	testutil.Contains(t, out, "already present (left untouched):")
	testutil.Contains(t, out, "default")
}
