package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/profiles"
	"github.com/drn/argus/internal/testutil"
)

func writeProfileFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".toml"), []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func TestRunValidate_Valid(t *testing.T) {
	lib := t.TempDir()
	writeProfileFile(t, lib, "good", `
[archetype.code_slice]
model = "sonnet"
`)
	var b strings.Builder
	code := runValidate(&b, &profiles.Loader{LibraryDir: lib}, config.Config{}, "good")
	testutil.Equal(t, code, 0)
	testutil.Contains(t, b.String(), "valid")
	testutil.Contains(t, b.String(), "library")
}

func TestRunValidate_InvalidReportsAllAndExitsNonZero(t *testing.T) {
	lib := t.TempDir()
	writeProfileFile(t, lib, "bad", `
[archetype.planner]
model = "opus"

[archetype.code_slice]
model  = "no-such-model"
effort = "extreme"
`)
	var b strings.Builder
	code := runValidate(&b, &profiles.Loader{LibraryDir: lib}, config.Config{}, "bad")
	testutil.Equal(t, code, 1)
	out := b.String()
	testutil.Contains(t, out, "planner")
	testutil.Contains(t, out, "no-such-model")
	testutil.Contains(t, out, "extreme")
}

func TestRunValidate_NotFound(t *testing.T) {
	var b strings.Builder
	code := runValidate(&b, &profiles.Loader{LibraryDir: t.TempDir()}, config.Config{}, "ghost")
	testutil.Equal(t, code, 1)
	testutil.Contains(t, b.String(), "ghost")
}

func TestRunValidate_CycleReported(t *testing.T) {
	lib := t.TempDir()
	writeProfileFile(t, lib, "a", `extends = "b"`)
	writeProfileFile(t, lib, "b", `extends = "a"`)
	var b strings.Builder
	code := runValidate(&b, &profiles.Loader{LibraryDir: lib}, config.Config{}, "a")
	testutil.Equal(t, code, 1)
	testutil.Contains(t, b.String(), "cycle")
}

func TestRunValidate_InRepoSourceReported(t *testing.T) {
	repo := t.TempDir()
	writeProfileFile(t, repo, "p", `
[archetype.docs]
model = "haiku"
`)
	var b strings.Builder
	code := runValidate(&b, &profiles.Loader{RepoDir: repo, LibraryDir: t.TempDir()}, config.Config{}, "p")
	testutil.Equal(t, code, 0)
	testutil.Contains(t, b.String(), "in-repo")
}
