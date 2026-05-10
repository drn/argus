package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/drn/argus/internal/testutil"
)

func TestParseCounts(t *testing.T) {
	for _, tc := range []struct {
		name             string
		line             string
		wantN, wantC     int
		wantOK           bool
	}{
		{"valid", "x.go:1.2,3.4 5 7\n", 5, 7, true},
		{"zero exec", "x.go:1.2,3.4 4 0\n", 4, 0, true},
		{"too few fields", "mode: set\n", 0, 0, false},
		{"non-numeric", "x.go:a,b c d\n", 0, 0, false},
		{"empty", "", 0, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n, c, ok := parseCounts(tc.line)
			testutil.Equal(t, n, tc.wantN)
			testutil.Equal(t, c, tc.wantC)
			testutil.Equal(t, ok, tc.wantOK)
		})
	}
}

func TestFileOf(t *testing.T) {
	for _, tc := range []struct {
		name, in, want string
	}{
		{"simple", "x/y.go:1.2,3.4 5 7", "x/y.go"},
		{"no colon", "no colon here", ""},
		{"leading colon", ":foo", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			testutil.Equal(t, fileOf(tc.in), tc.want)
		})
	}
}

func TestReadIgnore(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ignore.txt")
	body := "# comment\n\nfoo/bar.go\n  spaced/path.go  \n# another\n"
	testutil.NoError(t, os.WriteFile(path, []byte(body), 0o644))

	got, err := readIgnore(path)
	testutil.NoError(t, err)
	testutil.DeepEqual(t, got, []string{"foo/bar.go", "spaced/path.go"})
}

func TestReadIgnore_Missing(t *testing.T) {
	got, err := readIgnore(filepath.Join(t.TempDir(), "nope.txt"))
	testutil.NoError(t, err)
	testutil.Nil(t, got)
}

func TestReadIgnore_OpenError(t *testing.T) {
	// A directory cannot be opened as a file in a way that reads back content,
	// but os.Open succeeds on macOS/Linux for directories. To force a real
	// open error we pass a path under a non-existent parent that can't be
	// stat'd via permissions: use a path with a NUL byte which the OS
	// rejects with a non-IsNotExist error.
	_, err := readIgnore("bad\x00path")
	testutil.Error(t, err)
}

func TestFilter(t *testing.T) {
	in := strings.NewReader(strings.Join([]string{
		"mode: set",
		"github.com/x/keep/a.go:1.2,3.4 4 1",
		"github.com/x/drop/b.go:1.2,3.4 2 1",
		"github.com/x/keep/a.go:5.6,7.8 3 0",
		"github.com/x/drop/b.go:9.10,11.12 5 1",
		"",
	}, "\n"))
	var out bytes.Buffer
	s, err := filter(in, &out, []string{"x/drop/"})
	testutil.NoError(t, err)
	testutil.Equal(t, s.total, 7)     // 4 + 3
	testutil.Equal(t, s.covered, 4)   // first line covered, second not
	testutil.Equal(t, s.filteredFiles, 1)

	got := out.String()
	testutil.Contains(t, got, "mode: set")
	testutil.Contains(t, got, "x/keep/a.go")
	if strings.Contains(got, "x/drop/") {
		t.Errorf("filtered profile still contains dropped path:\n%s", got)
	}
}

func TestFilter_NoTrailingNewline(t *testing.T) {
	in := strings.NewReader("mode: set\nx/keep.go:1.2,3.4 2 1")
	var out bytes.Buffer
	s, err := filter(in, &out, nil)
	testutil.NoError(t, err)
	testutil.Equal(t, s.total, 2)
	testutil.Equal(t, s.covered, 2)
}

func TestFilter_Empty(t *testing.T) {
	var out bytes.Buffer
	s, err := filter(strings.NewReader(""), &out, nil)
	testutil.NoError(t, err)
	testutil.Equal(t, s.total, 0)
	testutil.Equal(t, s.percent(), 0.0)
}

func TestStatsPercent(t *testing.T) {
	testutil.Equal(t, stats{total: 0}.percent(), 0.0)
	s := stats{total: 200, covered: 50}
	testutil.Equal(t, s.percent(), 25.0)
}

// runCase exercises the run() function end-to-end. Helper writes the inputs
// to t.TempDir() and returns the captured stdout/stderr plus the exit code.
type runCase struct {
	profile string
	ignore  string
	args    []string
}

func runFor(t *testing.T, c runCase) (int, string, string) {
	t.Helper()
	dir := t.TempDir()
	profPath := filepath.Join(dir, "coverage.out")
	testutil.NoError(t, os.WriteFile(profPath, []byte(c.profile), 0o644))

	ignPath := filepath.Join(dir, "ignore.txt")
	if c.ignore != "" {
		testutil.NoError(t, os.WriteFile(ignPath, []byte(c.ignore), 0o644))
	}

	args := append([]string{"-in", profPath, "-ignore", ignPath}, c.args...)

	var stdout, stderr bytes.Buffer
	code := run(args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func TestRun_Success(t *testing.T) {
	prof := "mode: set\nx/keep.go:1.2,3.4 4 1\nx/keep.go:5.6,7.8 1 0\n"
	code, stdout, stderr := runFor(t, runCase{profile: prof})
	testutil.Equal(t, code, 0)
	testutil.Contains(t, stdout, "80.0")
	testutil.Contains(t, stderr, "coverage:")
}

func TestRun_GateFails(t *testing.T) {
	prof := "mode: set\nx/keep.go:1.2,3.4 4 0\n"
	code, _, stderr := runFor(t, runCase{
		profile: prof,
		args:    []string{"-min", "95"},
	})
	testutil.Equal(t, code, 2)
	testutil.Contains(t, stderr, "coverage gate FAILED")
}

func TestRun_GatePasses(t *testing.T) {
	prof := "mode: set\nx/keep.go:1.2,3.4 4 1\n"
	code, _, _ := runFor(t, runCase{
		profile: prof,
		args:    []string{"-min", "95"},
	})
	testutil.Equal(t, code, 0)
}

func TestRun_QuietSuppressesStderr(t *testing.T) {
	prof := "mode: set\nx/keep.go:1.2,3.4 2 1\n"
	code, _, stderr := runFor(t, runCase{
		profile: prof,
		args:    []string{"-quiet"},
	})
	testutil.Equal(t, code, 0)
	if strings.Contains(stderr, "coverage:") {
		t.Errorf("expected -quiet to suppress stderr summary, got: %q", stderr)
	}
}

func TestRun_StdoutOutput(t *testing.T) {
	prof := "mode: set\nx/keep.go:1.2,3.4 2 1\n"
	dir := t.TempDir()
	profPath := filepath.Join(dir, "coverage.out")
	testutil.NoError(t, os.WriteFile(profPath, []byte(prof), 0o644))
	var stdout, stderr bytes.Buffer
	code := run([]string{"-in", profPath, "-out", "-", "-quiet"}, &stdout, &stderr)
	testutil.Equal(t, code, 0)
	// With -out=-, stdout receives the profile, NOT a percentage line.
	testutil.Contains(t, stdout.String(), "mode: set")
	if strings.Contains(stdout.String(), "100.0\n") && !strings.Contains(stdout.String(), "mode: set\n") {
		t.Error("stdout should be the profile when -out=-")
	}
}

func TestRun_FileOutput(t *testing.T) {
	prof := "mode: set\nx/keep.go:1.2,3.4 2 1\n"
	dir := t.TempDir()
	profPath := filepath.Join(dir, "coverage.out")
	outPath := filepath.Join(dir, "filtered.out")
	testutil.NoError(t, os.WriteFile(profPath, []byte(prof), 0o644))
	var stdout, stderr bytes.Buffer
	code := run([]string{"-in", profPath, "-out", outPath, "-quiet"}, &stdout, &stderr)
	testutil.Equal(t, code, 0)

	got, err := os.ReadFile(outPath)
	testutil.NoError(t, err)
	testutil.Contains(t, string(got), "mode: set")
}

func TestRun_MissingInput(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"-in", "/nonexistent/path/coverage.out"}, &stdout, &stderr)
	testutil.Equal(t, code, 1)
	testutil.Contains(t, stderr.String(), "open input")
}

func TestRun_BadFlag(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"-bogus"}, &stdout, &stderr)
	testutil.Equal(t, code, 1)
}

func TestRun_BadIgnore(t *testing.T) {
	dir := t.TempDir()
	prof := filepath.Join(dir, "p.out")
	testutil.NoError(t, os.WriteFile(prof, []byte("mode: set\n"), 0o644))
	var stdout, stderr bytes.Buffer
	code := run([]string{"-in", prof, "-ignore", "bad\x00path"}, &stdout, &stderr)
	testutil.Equal(t, code, 1)
	testutil.Contains(t, stderr.String(), "read ignore")
}

func TestRun_CreateOutputFails(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("running as root")
	}
	dir := t.TempDir()
	prof := filepath.Join(dir, "p.out")
	testutil.NoError(t, os.WriteFile(prof, []byte("mode: set\n"), 0o644))
	// Make dir read-only so create fails.
	testutil.NoError(t, os.Chmod(dir, 0o500))
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	var stdout, stderr bytes.Buffer
	code := run([]string{"-in", prof, "-out", filepath.Join(dir, "x.out")}, &stdout, &stderr)
	testutil.Equal(t, code, 1)
	testutil.Contains(t, stderr.String(), "create output")
}

func TestRun_FilterError(t *testing.T) {
	// Truncate input mid-line so bufio.Reader returns ErrUnexpectedEOF —
	// actually io.EOF is returned with partial data. Crafting a real read
	// error requires a custom reader; the easier path is to point -in at a
	// directory, which triggers a read error on the first ReadString call.
	dir := t.TempDir()
	var stdout, stderr bytes.Buffer
	code := run([]string{"-in", dir}, &stdout, &stderr)
	// Either open succeeds but read fails (1), or open itself fails (1).
	testutil.Equal(t, code, 1)
}

// Compile-time check that bytes is used.
var _ bytes.Buffer
