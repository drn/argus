package gitutil

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
	"github.com/drn/argus/internal/uxlog"
)

// initTestUxlog redirects uxlog to a temp file and returns a function that
// reads the log contents. Cleanup is registered automatically.
func initTestUxlog(t *testing.T) func() string {
	t.Helper()
	logPath := filepath.Join(t.TempDir(), "ux.log")
	if err := uxlog.Init(logPath); err != nil {
		t.Fatalf("uxlog.Init: %v", err)
	}
	t.Cleanup(uxlog.Close)
	return func() string {
		b, err := os.ReadFile(logPath)
		if err != nil {
			t.Fatalf("read log: %v", err)
		}
		return string(b)
	}
}

// --- mapPRState pure-function tests (no process spawning) ---

func TestMapPRState_AllMappings(t *testing.T) {
	for _, tc := range []struct {
		name     string
		raw      string
		exitCode int
		want     model.PRState
		wantErr  bool
	}{
		{
			name: "open draft",
			raw:  `{"state":"OPEN","isDraft":true,"reviewDecision":"","url":"https://github.com/x/y/pull/1"}`,
			want: model.PRDraft,
		},
		{
			name: "open awaiting review (empty decision)",
			raw:  `{"state":"OPEN","isDraft":false,"reviewDecision":"","url":"https://github.com/x/y/pull/1"}`,
			want: model.PRAwaitingReview,
		},
		{
			name: "open review required decision",
			raw:  `{"state":"OPEN","isDraft":false,"reviewDecision":"REVIEW_REQUIRED","url":"https://github.com/x/y/pull/1"}`,
			want: model.PRAwaitingReview,
		},
		{
			name: "open changes requested",
			raw:  `{"state":"OPEN","isDraft":false,"reviewDecision":"CHANGES_REQUESTED","url":"https://github.com/x/y/pull/2"}`,
			want: model.PRChangesRequested,
		},
		{
			name: "open approved",
			raw:  `{"state":"OPEN","isDraft":false,"reviewDecision":"APPROVED","url":"https://github.com/x/y/pull/3"}`,
			want: model.PRApproved,
		},
		{
			name: "merged",
			raw:  `{"state":"MERGED","isDraft":false,"reviewDecision":"APPROVED","url":"https://github.com/x/y/pull/4"}`,
			want: model.PRMergedClosed,
		},
		{
			name: "closed",
			raw:  `{"state":"CLOSED","isDraft":false,"reviewDecision":"","url":"https://github.com/x/y/pull/5"}`,
			want: model.PRMergedClosed,
		},
		{
			name:     "non-zero exit no-pr message",
			exitCode: 1,
			raw:      "no pull requests found for branch 'argus/foo'",
			want:     model.PRNone,
		},
		{
			name: "draft takes priority over review decision",
			raw:  `{"state":"OPEN","isDraft":true,"reviewDecision":"APPROVED","url":""}`,
			want: model.PRDraft,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, _, err := mapPRState(tc.raw, tc.exitCode)
			testutil.NoError(t, err)
			testutil.Equal(t, got, tc.want)
		})
	}
}

func TestMapPRState_MalformedJSON(t *testing.T) {
	_, _, err := mapPRState(`{not valid json`, 0)
	testutil.Error(t, err)
}

func TestMapPRState_EmptyOutput_NonZeroExit(t *testing.T) {
	// Non-zero exit without the specific "no pull requests found" text is a
	// transient error (network, auth) — caller must not clobber cached value.
	_, _, err := mapPRState("", 1)
	testutil.Error(t, err)
}

func TestMapPRState_UnrecognizedOpenDecision(t *testing.T) {
	// Unknown reviewDecision values from future GitHub API versions should
	// fall through to AwaitingReview to stay conservative.
	got, _, err := mapPRState(`{"state":"OPEN","isDraft":false,"reviewDecision":"FUTURE_VALUE","url":""}`, 0)
	testutil.NoError(t, err)
	testutil.Equal(t, got, model.PRAwaitingReview)
}

// --- FetchPRState integration using the injected seam ---

func TestFetchPRState_InjectsSeam(t *testing.T) {
	// Verify that swapping prRunner makes FetchPRState use the fake.
	orig := prRunner
	t.Cleanup(func() { prRunner = orig })

	prRunner = func(_ context.Context, _ string, _ ...string) (string, int, error) {
		return `{"state":"OPEN","isDraft":false,"reviewDecision":"APPROVED","url":"u"}`, 0, nil
	}

	state, url, err := FetchPRState(context.Background(), "/any/dir", "my-branch")
	testutil.NoError(t, err)
	testutil.Equal(t, state, model.PRApproved)
	testutil.Equal(t, url, "u")
}

func TestFetchPRState_NoPR(t *testing.T) {
	orig := prRunner
	t.Cleanup(func() { prRunner = orig })

	prRunner = func(_ context.Context, _ string, _ ...string) (string, int, error) {
		return "no pull requests found for branch 'x'", 1, nil
	}

	state, _, err := FetchPRState(context.Background(), "/any/dir", "x")
	testutil.NoError(t, err)
	testutil.Equal(t, state, model.PRNone)
}

func TestFetchPRState_TransientError(t *testing.T) {
	orig := prRunner
	t.Cleanup(func() { prRunner = orig })

	prRunner = func(_ context.Context, _ string, _ ...string) (string, int, error) {
		return "", 1, errors.New("network timeout")
	}

	_, _, err := FetchPRState(context.Background(), "/any/dir", "x")
	testutil.Error(t, err)
}

func TestFetchPRState_GhAbsent_ReturnsUnknown(t *testing.T) {
	orig := prRunner
	t.Cleanup(func() { prRunner = orig })
	ResetGhAbsentLogged()

	prRunner = func(_ context.Context, _ string, _ ...string) (string, int, error) {
		return "", 0, errGhAbsent
	}

	state, _, err := FetchPRState(context.Background(), "/any/dir", "x")
	testutil.NoError(t, err)
	testutil.Equal(t, state, model.PRUnknown)
}

func TestFetchPRState_GhAbsent_LogsOnce(t *testing.T) {
	orig := prRunner
	t.Cleanup(func() { prRunner = orig })
	ResetGhAbsentLogged()
	readLog := initTestUxlog(t)

	prRunner = func(_ context.Context, _ string, _ ...string) (string, int, error) {
		return "", 0, errGhAbsent
	}

	// Three calls — log should appear exactly once.
	for range 3 {
		state, _, err := FetchPRState(context.Background(), "/any/dir", "x")
		testutil.NoError(t, err)
		testutil.Equal(t, state, model.PRUnknown)
	}

	content := readLog()
	count := strings.Count(content, "[pr]")
	testutil.Equal(t, count, 1)
}

func TestFetchPRState_GhUnauthenticated_LogsOnce(t *testing.T) {
	orig := prRunner
	t.Cleanup(func() { prRunner = orig })
	ResetGhAbsentLogged()
	readLog := initTestUxlog(t)

	prRunner = func(_ context.Context, _ string, _ ...string) (string, int, error) {
		// gh exits non-zero with auth-error text on the first line.
		return "To get started with GitHub CLI, please run: gh auth login\nalternatively, populate the GH_TOKEN environment variable with a personal access token\n", 4, nil
	}

	state, _, err := FetchPRState(context.Background(), "/any/dir", "x")
	testutil.NoError(t, err)
	testutil.Equal(t, state, model.PRUnknown)

	content := readLog()
	testutil.Contains(t, content, "[pr]")
}

func TestMapPRState_UnexpectedState(t *testing.T) {
	_, _, err := mapPRState(`{"state":"UNKNOWN_STATE","isDraft":false,"reviewDecision":"","url":""}`, 0)
	testutil.Error(t, err)
	testutil.Contains(t, err.Error(), "unexpected gh pr state")
}

func TestFetchPRState_ExecLevelError_Code0(t *testing.T) {
	// Simulates a process-launch failure (e.g. ENOENT after LookPath succeeds
	// on a race, or EPERM). Code is 0 because ProcessState is nil.
	orig := prRunner
	t.Cleanup(func() { prRunner = orig })

	prRunner = func(_ context.Context, _ string, _ ...string) (string, int, error) {
		return "", 0, errors.New("fork/exec: permission denied")
	}

	_, _, err := FetchPRState(context.Background(), "/any/dir", "x")
	testutil.Error(t, err)
	testutil.Contains(t, err.Error(), "gh exec error")
}

func TestErrGhAuth_ErrorMethod(t *testing.T) {
	e := &errGhAuth{msg: "please run gh auth login"}
	testutil.Contains(t, e.Error(), "gh unauthenticated")
	testutil.Contains(t, e.Error(), "please run gh auth login")
}

func TestFetchPRState_ContextTimeout(t *testing.T) {
	orig := prRunner
	t.Cleanup(func() { prRunner = orig })

	prRunner = func(ctx context.Context, _ string, _ ...string) (string, int, error) {
		// Simulate the context having already been cancelled (as happens on
		// a 5s timeout).
		return "", 1, context.DeadlineExceeded
	}

	_, _, err := FetchPRState(context.Background(), "/any/dir", "x")
	testutil.Error(t, err)
}
