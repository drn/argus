package claudesession

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/drn/argus/internal/testutil"
)

func TestEncodeProjectDir(t *testing.T) {
	tests := []struct {
		name string
		path string
		want string
	}{
		{"argus worktree", "/Users/aaron/.argus/worktrees/ARGUS/brainstorm-am-wondering-don", "-Users-aaron--argus-worktrees-ARGUS-brainstorm-am-wondering-don"},
		{"dot becomes dash", "/a/.b", "-a--b"},
		{"underscores and spaces", "/x/a_b c", "-x-a-b-c"},
		{"already kebab", "/p/foo-bar", "-p-foo-bar"},
		// Edge cases consolidated from agent.claudeEncodeCwd (now removed):
		{"single slash", "/", "-"},
		{"empty", "", ""},
		{"relative path", "relative/path", "relative-path"},
		{"hyphens survive; dots and underscores map", "/a/b-c/d_e.f", "-a-b-c-d-e-f"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			testutil.Equal(t, EncodeProjectDir(tc.path), tc.want)
		})
	}
}

func TestProjectDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	got, err := ProjectDir("/w/t")
	testutil.NoError(t, err)
	testutil.Equal(t, got, filepath.Join(home, ".claude", "projects", "-w-t"))
}

// writeSession creates a JSONL session file with the given lines inside the
// project dir for worktree, returning the session ID used.
func writeSession(t *testing.T, home, worktree, id string, lines []string, mod time.Time) {
	t.Helper()
	dir := filepath.Join(home, ".claude", "projects", EncodeProjectDir(worktree))
	testutil.NoError(t, os.MkdirAll(dir, 0o755))
	path := filepath.Join(dir, id+".jsonl")
	var content string
	for _, l := range lines {
		content += l + "\n"
	}
	testutil.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	if !mod.IsZero() {
		testutil.NoError(t, os.Chtimes(path, mod, mod))
	}
}

func TestList_EmptyWorktreeErrors(t *testing.T) {
	_, err := List("")
	if err == nil {
		t.Fatal("expected error for empty worktree path")
	}
}

func TestList_MissingDirIsEmpty(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	got, err := List("/never/created")
	testutil.NoError(t, err)
	testutil.Equal(t, len(got), 0)
}

func TestList_ParsesMetadata(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	wt := "/w/proj/task"
	id := "5e7ca4b6-2b5a-43b4-8de2-1e339c46d686"
	writeSession(t, home, wt, id, []string{
		`{"type":"user","timestamp":"2026-06-04T20:17:26.617Z","gitBranch":"argus/task","slug":"do-the-thing","sessionId":"` + id + `"}`,
		`{"type":"assistant","timestamp":"2026-06-04T20:17:56.270Z","gitBranch":"argus/task","sessionId":"` + id + `"}`,
		`{"type":"ai-title","aiTitle":"Implement the thing","sessionId":"` + id + `"}`,
		`{"type":"pr-link","prNumber":635,"prRepository":"drn/argus","timestamp":"2026-05-30T23:12:04.190Z","sessionId":"` + id + `"}`,
	}, time.Time{})

	got, err := List(wt)
	testutil.NoError(t, err)
	testutil.Equal(t, len(got), 1)
	s := got[0]
	testutil.Equal(t, s.ID, id)
	testutil.Equal(t, s.Title, "Implement the thing")
	testutil.Equal(t, s.Branch, "argus/task")
	testutil.Equal(t, s.PRRef, "drn/argus#635")
	// ModTime is the max of the entry timestamps (the assistant line).
	testutil.Equal(t, s.ModTime.UTC(), time.Date(2026, 6, 4, 20, 17, 56, 270000000, time.UTC))
	if s.SizeBytes <= 0 {
		t.Fatalf("expected positive size, got %d", s.SizeBytes)
	}
}

func TestList_TitleFallsBackToSlug(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	wt := "/w/slug"
	id := "11111111-1111-4111-8111-111111111111"
	writeSession(t, home, wt, id, []string{
		`{"type":"user","timestamp":"2026-06-04T10:00:00.000Z","slug":"fix-the-bug-now","sessionId":"` + id + `"}`,
	}, time.Time{})

	got, err := List(wt)
	testutil.NoError(t, err)
	testutil.Equal(t, len(got), 1)
	testutil.Equal(t, got[0].Title, "fix the bug now")
}

func TestList_TitleFallsBackToUntitled(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	wt := "/w/untitled"
	id := "22222222-2222-4222-8222-222222222222"
	writeSession(t, home, wt, id, []string{
		`{"type":"mode","mode":"normal","sessionId":"` + id + `"}`,
	}, time.Time{})

	got, err := List(wt)
	testutil.NoError(t, err)
	testutil.Equal(t, len(got), 1)
	testutil.Equal(t, got[0].Title, "(untitled session)")
}

func TestList_ModTimeFallsBackToFileMtime(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	wt := "/w/notime"
	id := "33333333-3333-4333-8333-333333333333"
	mtime := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	writeSession(t, home, wt, id, []string{
		`{"type":"ai-title","aiTitle":"No timestamps here","sessionId":"` + id + `"}`,
	}, mtime)

	got, err := List(wt)
	testutil.NoError(t, err)
	testutil.Equal(t, len(got), 1)
	testutil.Equal(t, got[0].ModTime.UTC().Truncate(time.Second), mtime)
}

func TestList_SkipsMalformedAndStrayFiles(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	wt := "/w/mixed"
	good := "44444444-4444-4444-8444-444444444444"
	writeSession(t, home, wt, good, []string{
		`{"type":"ai-title","aiTitle":"Good one","sessionId":"` + good + `"}`,
		`this is not json and must be skipped`,
		`{"type":"user","timestamp":"2026-06-04T10:00:00.000Z","sessionId":"` + good + `"}`,
	}, time.Time{})

	dir := filepath.Join(home, ".claude", "projects", EncodeProjectDir(wt))
	// Stray non-UUID jsonl file: must be ignored.
	testutil.NoError(t, os.WriteFile(filepath.Join(dir, "notes.jsonl"), []byte("{}\n"), 0o644))
	// A subdirectory (Claude's tool-results cache): must be ignored.
	testutil.NoError(t, os.MkdirAll(filepath.Join(dir, good), 0o755))

	got, err := List(wt)
	testutil.NoError(t, err)
	testutil.Equal(t, len(got), 1)
	testutil.Equal(t, got[0].Title, "Good one")
}

func TestList_SortsNewestFirst(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	wt := "/w/sorted"
	older := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	newer := "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	writeSession(t, home, wt, older, []string{
		`{"type":"user","timestamp":"2026-06-01T10:00:00.000Z","sessionId":"` + older + `"}`,
	}, time.Time{})
	writeSession(t, home, wt, newer, []string{
		`{"type":"user","timestamp":"2026-06-04T10:00:00.000Z","sessionId":"` + newer + `"}`,
	}, time.Time{})

	got, err := List(wt)
	testutil.NoError(t, err)
	testutil.Equal(t, len(got), 2)
	testutil.Equal(t, got[0].ID, newer)
	testutil.Equal(t, got[1].ID, older)
}

func TestProjectDir_NoHomeErrors(t *testing.T) {
	t.Setenv("HOME", "")
	if _, err := ProjectDir("/w/t"); err == nil {
		t.Fatal("expected error when HOME is unset")
	}
	if _, err := List("/w/t"); err == nil {
		t.Fatal("expected List to surface the home-dir error")
	}
}

func TestList_ReadDirErrorSurfaces(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	wt := "/w/isfile"
	// Create a regular file exactly where the project directory would be,
	// so os.ReadDir fails with a non-NotExist error.
	projects := filepath.Join(home, ".claude", "projects")
	testutil.NoError(t, os.MkdirAll(projects, 0o755))
	testutil.NoError(t, os.WriteFile(filepath.Join(projects, EncodeProjectDir(wt)), []byte("x"), 0o644))
	if _, err := List(wt); err == nil {
		t.Fatal("expected error when project path is not a directory")
	}
}

func TestParseSession_UnreadableFileIsZero(t *testing.T) {
	got := parseSession(filepath.Join(t.TempDir(), "does-not-exist.jsonl"))
	testutil.Equal(t, got.Title, "")
	testutil.Equal(t, got.ID, "")
}

func TestRelativeTime_UsesNow(t *testing.T) {
	testutil.Equal(t, RelativeTime(time.Now().Add(-2*time.Minute)), "2 minutes ago")
}

func TestRelativeTimeSince(t *testing.T) {
	now := time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		t    time.Time
		want string
	}{
		{"zero", time.Time{}, "unknown"},
		{"future clamps to just now", now.Add(time.Hour), "just now"},
		{"seconds", now.Add(-30 * time.Second), "just now"},
		{"one minute", now.Add(-time.Minute), "1 minute ago"},
		{"minutes", now.Add(-5 * time.Minute), "5 minutes ago"},
		{"one hour", now.Add(-time.Hour), "1 hour ago"},
		{"hours", now.Add(-3 * time.Hour), "3 hours ago"},
		{"one day", now.Add(-24 * time.Hour), "1 day ago"},
		{"days", now.Add(-72 * time.Hour), "3 days ago"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			testutil.Equal(t, relativeTimeSince(now, tc.t), tc.want)
		})
	}
}

func TestHumanSize(t *testing.T) {
	tests := []struct {
		n    int64
		want string
	}{
		{0, "0B"},
		{512, "512B"},
		{1024, "1.0KB"},
		{303206, "296.1KB"},
		{12163481, "11.6MB"},
		{5 * 1024 * 1024 * 1024, "5.0GB"},
	}
	for _, tc := range tests {
		t.Run(tc.want, func(t *testing.T) {
			testutil.Equal(t, HumanSize(tc.n), tc.want)
		})
	}
}
