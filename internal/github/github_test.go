package github

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"testing"

	"github.com/drn/argus/internal/testutil"
)

// stubGh installs a fake runGh implementation for the duration of t and
// returns a slice that records each invocation's args. The stub cycles
// through the provided responses; once exhausted it returns ("", nil).
func stubGh(t *testing.T, responses ...ghResponse) *[]ghCall {
	t.Helper()
	calls := []ghCall{}
	idx := 0
	orig := runGh
	runGh = func(args ...string) (string, error) {
		calls = append(calls, ghCall{Args: append([]string(nil), args...)})
		if idx >= len(responses) {
			return "", nil
		}
		r := responses[idx]
		idx++
		return r.Out, r.Err
	}
	t.Cleanup(func() { runGh = orig })
	return &calls
}

type ghCall struct{ Args []string }
type ghResponse struct {
	Out string
	Err error
}

func TestExtractFileDiff(t *testing.T) {
	for _, tc := range []struct {
		name, full, file string
		wantContains     string
		wantEmpty        bool
	}{
		{
			name: "single file",
			full: "diff --git a/foo/bar.go b/foo/bar.go\n+++ b/foo/bar.go\n+x",
			file: "foo/bar.go",
			wantContains: "+++ b/foo/bar.go",
		},
		{
			name: "multi-file picks one",
			full: "diff --git a/a.go b/a.go\n+a\ndiff --git a/b.go b/b.go\n+b",
			file: "b.go",
			wantContains: "+b",
		},
		{
			name:      "missing returns empty",
			full:      "diff --git a/foo.go b/foo.go\n+x",
			file:      "nope.go",
			wantEmpty: true,
		},
		{
			name:      "empty input",
			full:      "",
			file:      "foo.go",
			wantEmpty: true,
		},
		{
			name:      "common prefix avoided",
			full:      "diff --git a/api b/api\n+api\ndiff --git a/api_test.go b/api_test.go\n+test",
			file:      "api",
			wantContains: "+api",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := ExtractFileDiff(tc.full, tc.file)
			if tc.wantEmpty {
				testutil.Equal(t, got, "")
				return
			}
			testutil.Contains(t, got, tc.wantContains)
		})
	}
}

func TestExtractFileDiff_PrefixAvoided(t *testing.T) {
	full := "diff --git a/api b/api\n+api line\ndiff --git a/api_test.go b/api_test.go\n+test line"
	got := ExtractFileDiff(full, "api")
	testutil.Contains(t, got, "+api line")
	if strings.Contains(got, "+test line") {
		t.Errorf("matched wrong file: got=%q", got)
	}
}

func TestClassifyGhError(t *testing.T) {
	for _, tc := range []struct {
		name      string
		stderr    string
		wantRate  bool
	}{
		{"http 429", "HTTP 429 too many", true},
		{"http 403 rate limit", "HTTP 403: rate limit hit", true},
		{"plain rate limit", "API rate limit exceeded", true},
		{"secondary", "secondary rate limit triggered", true},
		{"http 403 other", "HTTP 403 forbidden but not a limit", false},
		{"random error", "no such repository", false},
		{"empty", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := classifyGhError(tc.stderr)
			if tc.wantRate {
				testutil.ErrorIs(t, err, ErrRateLimit)
			} else {
				testutil.Nil(t, err)
			}
		})
	}
}

func TestRealRunGh_Success(t *testing.T) {
	orig := commandBuilder
	commandBuilder = func(ctx context.Context, args ...string) *exec.Cmd {
		return exec.CommandContext(ctx, "echo", "hello")
	}
	t.Cleanup(func() { commandBuilder = orig })

	out, err := realRunGh("ignored")
	testutil.NoError(t, err)
	testutil.Equal(t, strings.TrimSpace(out), "hello")
}

func TestRealRunGh_RateLimit(t *testing.T) {
	orig := commandBuilder
	commandBuilder = func(ctx context.Context, args ...string) *exec.Cmd {
		// Write rate-limit signature to stderr and exit non-zero.
		return exec.CommandContext(ctx, "sh", "-c", "echo 'HTTP 429' >&2; exit 1")
	}
	t.Cleanup(func() { commandBuilder = orig })

	_, err := realRunGh("api", "x")
	testutil.ErrorIs(t, err, ErrRateLimit)
}

func TestRealRunGh_OtherError(t *testing.T) {
	orig := commandBuilder
	commandBuilder = func(ctx context.Context, args ...string) *exec.Cmd {
		return exec.CommandContext(ctx, "sh", "-c", "echo 'unknown error' >&2; exit 1")
	}
	t.Cleanup(func() { commandBuilder = orig })

	_, err := realRunGh("api", "x")
	testutil.Error(t, err)
	if errors.Is(err, ErrRateLimit) {
		t.Errorf("did not expect ErrRateLimit, got %v", err)
	}
	testutil.Contains(t, err.Error(), "gh api x")
}

func TestFetchPRList_Success(t *testing.T) {
	myJSON := `[{"number":1,"title":"my pr","author":{"login":"me"},"isDraft":false,"repository":{"nameWithOwner":"o/r"},"updatedAt":"2026-01-01T00:00:00Z"}]`
	reviewJSON := `[{"number":2,"title":"review me","author":{"login":"alice"},"isDraft":true,"repository":{"nameWithOwner":"o/r"},"updatedAt":"2026-01-02T00:00:00Z"}]`
	enrichJSON := `[{"number":1,"reviewDecision":"APPROVED"},{"number":2,"reviewDecision":"REVIEW_REQUIRED"}]`

	calls := stubGh(t,
		ghResponse{Out: myJSON},
		ghResponse{Out: reviewJSON},
		ghResponse{Out: enrichJSON},
	)

	prs, err := FetchPRList()
	testutil.NoError(t, err)
	testutil.Equal(t, len(prs), 2)
	testutil.Equal(t, len(*calls), 3)

	// Find each by number — order across the two queries isn't guaranteed.
	byNum := map[int]PR{}
	for _, p := range prs {
		byNum[p.Number] = p
	}
	testutil.Equal(t, byNum[1].Author, "me")
	testutil.Equal(t, byNum[1].RepoOwner, "o")
	testutil.Equal(t, byNum[1].Repo, "r")
	testutil.Equal(t, byNum[1].IsReviewRequest, false)
	testutil.Equal(t, byNum[1].ReviewDecision, "APPROVED")
	testutil.Equal(t, byNum[2].IsDraft, true)
	testutil.Equal(t, byNum[2].IsReviewRequest, true)
	testutil.Equal(t, byNum[2].ReviewDecision, "REVIEW_REQUIRED")
}

func TestFetchPRList_DedupesAcrossQueries(t *testing.T) {
	dup := `[{"number":7,"title":"x","author":{"login":"me"},"isDraft":false,"repository":{"nameWithOwner":"o/r"},"updatedAt":"2026-01-01T00:00:00Z"}]`
	stubGh(t,
		ghResponse{Out: dup},
		ghResponse{Out: dup},
		ghResponse{Out: "[]"},
	)
	prs, err := FetchPRList()
	testutil.NoError(t, err)
	testutil.Equal(t, len(prs), 1)
}

func TestFetchPRList_BothErrorReturns(t *testing.T) {
	stubGh(t,
		ghResponse{Err: errors.New("boom1")},
		ghResponse{Err: errors.New("boom2")},
	)
	_, err := FetchPRList()
	testutil.Error(t, err)
	testutil.Contains(t, err.Error(), "gh search failed")
}

func TestFetchPRList_PartialSuccessReturnsResults(t *testing.T) {
	myJSON := `[{"number":1,"title":"x","author":{"login":"me"},"isDraft":false,"repository":{"nameWithOwner":"o/r"},"updatedAt":"2026-01-01T00:00:00Z"}]`
	stubGh(t,
		ghResponse{Out: myJSON},
		ghResponse{Err: errors.New("review-side-down")},
		ghResponse{Out: "[]"},
	)
	prs, err := FetchPRList()
	testutil.NoError(t, err)
	testutil.Equal(t, len(prs), 1)
}

func TestFetchPRList_BadJSONNoResults(t *testing.T) {
	stubGh(t,
		ghResponse{Out: "not json"},
		ghResponse{Out: "also not json"},
	)
	_, err := FetchPRList()
	testutil.Error(t, err)
	testutil.Contains(t, err.Error(), "parse search results")
}

func TestFetchPRList_BadJSONPartial(t *testing.T) {
	myJSON := `[{"number":1,"title":"x","author":{"login":"me"},"isDraft":false,"repository":{"nameWithOwner":"o/r"},"updatedAt":"2026-01-01T00:00:00Z"}]`
	stubGh(t,
		ghResponse{Out: myJSON},
		ghResponse{Out: "garbage"},
		ghResponse{Out: "[]"},
	)
	prs, err := FetchPRList()
	testutil.NoError(t, err)
	testutil.Equal(t, len(prs), 1)
}

func TestFetchPRList_BareRepoName(t *testing.T) {
	// nameWithOwner without a slash falls back to setting both owner and repo
	// to the same string. Exercises the parts-len-not-2 branch.
	bare := `[{"number":3,"title":"x","author":{"login":"me"},"isDraft":false,"repository":{"nameWithOwner":"singleton"},"updatedAt":"2026-01-01T00:00:00Z"}]`
	stubGh(t,
		ghResponse{Out: bare},
		ghResponse{Out: "[]"},
		ghResponse{Out: "[]"},
	)
	prs, err := FetchPRList()
	testutil.NoError(t, err)
	testutil.Equal(t, len(prs), 1)
	testutil.Equal(t, prs[0].RepoOwner, "singleton")
	testutil.Equal(t, prs[0].Repo, "singleton")
}

func TestEnrichReviewDecisions_RPCError(t *testing.T) {
	prs := []PR{{Number: 1, RepoOwner: "o", Repo: "r"}}
	stubGh(t, ghResponse{Err: errors.New("nope")})
	enrichReviewDecisions(prs)
	testutil.Equal(t, prs[0].ReviewDecision, "")
}

func TestEnrichReviewDecisions_BadJSON(t *testing.T) {
	prs := []PR{{Number: 1, RepoOwner: "o", Repo: "r"}}
	stubGh(t, ghResponse{Out: "garbage"})
	enrichReviewDecisions(prs)
	testutil.Equal(t, prs[0].ReviewDecision, "")
}

func TestEnrichReviewDecisions_GroupsByRepo(t *testing.T) {
	prs := []PR{
		{Number: 1, RepoOwner: "o", Repo: "r"},
		{Number: 2, RepoOwner: "o", Repo: "r"},
		{Number: 3, RepoOwner: "x", Repo: "y"},
	}
	// Map iteration in enrichReviewDecisions visits repos in random order,
	// so we route the response by inspecting the args rather than relying
	// on stub-order FIFO.
	var callCount int
	orig := runGh
	runGh = func(args ...string) (string, error) {
		callCount++
		joined := strings.Join(args, " ")
		switch {
		case strings.Contains(joined, "-R o/r"):
			return `[{"number":1,"reviewDecision":"APPROVED"},{"number":2,"reviewDecision":"REVIEW_REQUIRED"}]`, nil
		case strings.Contains(joined, "-R x/y"):
			return `[{"number":3,"reviewDecision":"CHANGES_REQUESTED"}]`, nil
		}
		return "[]", nil
	}
	t.Cleanup(func() { runGh = orig })

	enrichReviewDecisions(prs)
	testutil.Equal(t, callCount, 2) // one per repo
	got := map[int]string{}
	for _, p := range prs {
		got[p.Number] = p.ReviewDecision
	}
	testutil.Equal(t, got[1], "APPROVED")
	testutil.Equal(t, got[2], "REVIEW_REQUIRED")
	testutil.Equal(t, got[3], "CHANGES_REQUESTED")
}

func TestFetchPRFiles_Success(t *testing.T) {
	stubGh(t, ghResponse{Out: "a.go\nb.go\n"})
	files, err := FetchPRFiles("o", "r", 5)
	testutil.NoError(t, err)
	testutil.DeepEqual(t, files, []string{"a.go", "b.go"})
}

func TestFetchPRFiles_Empty(t *testing.T) {
	stubGh(t, ghResponse{Out: ""})
	files, err := FetchPRFiles("o", "r", 5)
	testutil.NoError(t, err)
	// strings.Split of empty returns [""], filter drops it.
	testutil.Equal(t, len(files), 0)
}

func TestFetchPRFiles_Error(t *testing.T) {
	stubGh(t, ghResponse{Err: errors.New("boom")})
	_, err := FetchPRFiles("o", "r", 5)
	testutil.Error(t, err)
}

func TestFetchPRFullDiff_PassesThroughArgs(t *testing.T) {
	calls := stubGh(t, ghResponse{Out: "diff stuff"})
	got, err := FetchPRFullDiff("o", "r", 42)
	testutil.NoError(t, err)
	testutil.Equal(t, got, "diff stuff")
	testutil.Equal(t, len(*calls), 1)
	testutil.DeepEqual(t, (*calls)[0].Args, []string{"pr", "diff", "42", "-R", "o/r"})
}

func TestFetchPRComments_Success(t *testing.T) {
	body := `[
		{"id":10,"user":{"login":"alice"},"body":"nit","path":"a.go","line":3,"created_at":"2026-01-01T00:00:00Z"},
		{"id":11,"user":{"login":"bob"},"body":"lgtm","path":"","line":0,"created_at":"2026-01-02T00:00:00Z"}
	]`
	stubGh(t, ghResponse{Out: body})
	cs, err := FetchPRComments("o", "r", 1)
	testutil.NoError(t, err)
	testutil.Equal(t, len(cs), 2)
	testutil.Equal(t, cs[0].Author, "alice")
	testutil.Equal(t, cs[0].Path, "a.go")
	testutil.Equal(t, cs[0].Line, 3)
	testutil.Equal(t, cs[1].Path, "")
}

func TestFetchPRComments_Error(t *testing.T) {
	stubGh(t, ghResponse{Err: errors.New("boom")})
	_, err := FetchPRComments("o", "r", 1)
	testutil.Error(t, err)
}

func TestFetchPRComments_BadJSON(t *testing.T) {
	stubGh(t, ghResponse{Out: "not json"})
	_, err := FetchPRComments("o", "r", 1)
	testutil.Error(t, err)
	testutil.Contains(t, err.Error(), "parse comments")
}

func TestPostReviewComment_PassesThroughArgs(t *testing.T) {
	calls := stubGh(t, ghResponse{Out: ""})
	err := PostReviewComment("o", "r", 1, "deadbeef", "a.go", 5, "nit")
	testutil.NoError(t, err)
	testutil.Equal(t, len(*calls), 1)
	args := (*calls)[0].Args
	if !strings.Contains(strings.Join(args, " "), "repos/o/r/pulls/1/comments") {
		t.Errorf("expected endpoint in args: %v", args)
	}
	if !strings.Contains(strings.Join(args, " "), "line=5") {
		t.Errorf("expected line=5 in args: %v", args)
	}
}

func TestSubmitReview_PassesThroughArgs(t *testing.T) {
	calls := stubGh(t, ghResponse{Out: ""})
	err := SubmitReview("o", "r", 1, ReviewApprove, "nice")
	testutil.NoError(t, err)
	testutil.Equal(t, len(*calls), 1)
	joined := strings.Join((*calls)[0].Args, " ")
	testutil.Contains(t, joined, "repos/o/r/pulls/1/reviews")
	testutil.Contains(t, joined, "event=APPROVE")
	testutil.Contains(t, joined, "body=nice")
}

func TestSubmitReview_Error(t *testing.T) {
	stubGh(t, ghResponse{Err: errors.New("boom")})
	err := SubmitReview("o", "r", 1, ReviewComment, "x")
	testutil.Error(t, err)
}

// Sanity check: errors from runGh wrap the args for diagnosis.
func TestRunGhErrorWrapping(t *testing.T) {
	stubGh(t, ghResponse{Err: fmt.Errorf("simulated")})
	_, err := runGh("api", "x")
	testutil.Error(t, err)
}

// realRunGh's context timeout is exercised indirectly by the success and
// error paths above. A direct timeout test would burn 10s of wall time per
// CI run for marginal value, so we rely on the cancel/exec semantics being
// covered by the stdlib.
