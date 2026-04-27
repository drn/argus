package selfupdate

import (
	"os"
	"os/exec"
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

// gitInit is a small helper for the git-aware tests below.
func gitInit(t *testing.T, dir string, args ...[]string) {
	t.Helper()
	for _, a := range args {
		cmd := exec.Command("git", a...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", a, err, out)
		}
	}
}

func TestRun_GitResetsHardToOriginMaster(t *testing.T) {
	if testing.Short() {
		t.Skip("shells out to git and go")
	}
	gobin := t.TempDir()
	t.Setenv("GOBIN", gobin)

	// Build a bare "origin" repo with a master branch carrying the real
	// hello-world source, then a working clone whose master has diverged
	// (different file contents and an extra local commit). After Run, the
	// clone's working tree must match origin/master's contents — proving the
	// reset --hard fired — and `go install` must succeed.
	originDir := t.TempDir()
	gitInit(t, originDir, []string{"init", "-q", "--bare", "-b", "master"})

	seedDir := t.TempDir()
	gitInit(t, seedDir,
		[]string{"init", "-q", "-b", "master"},
		[]string{"-c", "user.email=t@t", "-c", "user.name=t", "remote", "add", "origin", originDir},
	)
	if err := os.WriteFile(filepath.Join(seedDir, "go.mod"), []byte("module example.com/hello\n\ngo 1.21\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(seedDir, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitInit(t, seedDir,
		[]string{"-c", "user.email=t@t", "-c", "user.name=t", "add", "."},
		[]string{"-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "-m", "origin master"},
		[]string{"push", "-q", "origin", "master"},
	)

	cloneDir := t.TempDir()
	gitInit(t, cloneDir,
		[]string{"clone", "-q", originDir, cloneDir},
	)
	// Diverge: replace main.go with a broken version on a non-master branch
	// without an upstream — this reproduces the "no tracking information"
	// scenario the user originally hit, and the broken source proves the
	// reset took effect (otherwise `go install` would fail).
	gitInit(t, cloneDir, []string{"checkout", "-q", "-b", "feature/no-upstream"})
	if err := os.WriteFile(filepath.Join(cloneDir, "main.go"), []byte("package main\n\nfunc main() { broken }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitInit(t, cloneDir,
		[]string{"-c", "user.email=t@t", "-c", "user.name=t", "add", "."},
		[]string{"-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "-m", "broken local"},
	)

	out, err := Run(cloneDir)
	testutil.NoError(t, err)
	if !strings.Contains(out, "git reset --hard origin/master") {
		t.Errorf("expected log to mention reset --hard, got:\n%s", out)
	}
	if strings.Contains(out, "no tracking information") {
		t.Errorf("expected the no-tracking-branch error to no longer surface, got:\n%s", out)
	}
	if !strings.Contains(out, "Update succeeded") {
		t.Errorf("expected update to succeed, got:\n%s", out)
	}
	// Working tree matches origin/master, not the diverged feature branch.
	got, err := os.ReadFile(filepath.Join(cloneDir, "main.go"))
	testutil.NoError(t, err)
	if !strings.Contains(string(got), "func main() {}") {
		t.Errorf("expected reset to restore origin/master main.go, got:\n%s", got)
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
