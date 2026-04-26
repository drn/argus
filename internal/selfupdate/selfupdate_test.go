package selfupdate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/drn/argus/internal/testutil"
)

func TestRun_EmptyPath(t *testing.T) {
	_, err := Run("")
	testutil.ErrorIs(t, err, ErrSourcePathUnset)
}

func TestRun_NonexistentPath(t *testing.T) {
	_, err := Run(filepath.Join(t.TempDir(), "does-not-exist"))
	if err == nil {
		t.Fatal("expected error for nonexistent path")
	}
	if !strings.Contains(err.Error(), "not a directory") {
		t.Errorf("expected 'not a directory' error, got: %v", err)
	}
}

func TestRun_NotAGoModule(t *testing.T) {
	dir := t.TempDir()
	_, err := Run(dir)
	if err == nil {
		t.Fatal("expected error for non-go-module")
	}
	if !strings.Contains(err.Error(), "not a Go module") {
		t.Errorf("expected 'not a Go module' error, got: %v", err)
	}
}

func TestRun_GoInstallFailure(t *testing.T) {
	if testing.Short() {
		t.Skip("shells out to go")
	}
	dir := t.TempDir()
	// Minimal module with a syntax error.
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/broken\n\ngo 1.21\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc main() { this_does_not_compile }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := Run(dir)
	if err == nil {
		t.Fatal("expected go install to fail")
	}
	if !strings.Contains(out, "go install") {
		t.Errorf("expected output to mention 'go install', got:\n%s", out)
	}
}

func TestRun_GoInstallSuccess(t *testing.T) {
	if testing.Short() {
		t.Skip("shells out to go")
	}
	dir := t.TempDir()
	gobin := t.TempDir()
	t.Setenv("GOBIN", gobin)
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/hello\n\ngo 1.21\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := Run(dir)
	testutil.NoError(t, err)
	if !strings.Contains(out, "Update succeeded") {
		t.Errorf("expected success marker in output, got:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(gobin, "hello")); err != nil {
		t.Errorf("expected installed binary at %s/hello: %v", gobin, err)
	}
}
