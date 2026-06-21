package gitutil

import (
	"context"
	"errors"
	"testing"

	"github.com/drn/argus/internal/testutil"
)

// installRepoRunner swaps the prRepoRunner seam for the duration of a test.
func installRepoRunner(t *testing.T, f func(ctx context.Context, dir string, args ...string) (string, int, error)) {
	t.Helper()
	orig := prRepoRunner
	t.Cleanup(func() { prRepoRunner = orig })
	prRepoRunner = f
}

func TestResolveDefaultRepo(t *testing.T) {
	for _, tc := range []struct {
		name     string
		worktree string
		out      string
		code     int
		err      error
		wantRepo string
		wantOK   bool
	}{
		{name: "valid owner/name", worktree: "/wt", out: "drn/argus\n", code: 0, wantRepo: "drn/argus", wantOK: true},
		{name: "trims whitespace", worktree: "/wt", out: "  anutron/gmail-mcp  ", code: 0, wantRepo: "anutron/gmail-mcp", wantOK: true},
		{name: "empty worktree short-circuits", worktree: "", out: "drn/argus", code: 0, wantOK: false},
		{name: "non-zero exit drops", worktree: "/wt", out: "no default repo", code: 1, wantOK: false},
		{name: "exec error drops", worktree: "/wt", out: "", code: 0, err: errors.New("gh exec"), wantOK: false},
		{name: "malformed output drops", worktree: "/wt", out: "not-a-repo", code: 0, wantOK: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var called bool
			installRepoRunner(t, func(_ context.Context, _ string, _ ...string) (string, int, error) {
				called = true
				return tc.out, tc.code, tc.err
			})
			repo, ok := ResolveDefaultRepo(context.Background(), tc.worktree)
			testutil.Equal(t, ok, tc.wantOK)
			testutil.Equal(t, repo, tc.wantRepo)
			// Empty worktree must short-circuit before shelling out.
			if tc.worktree == "" {
				testutil.Equal(t, called, false)
			}
		})
	}
}
