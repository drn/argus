package github

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// ReviewAction is the type of review to submit.
type ReviewAction string

const (
	ReviewApprove        ReviewAction = "APPROVE"
	ReviewRequestChanges ReviewAction = "REQUEST_CHANGES"
	ReviewComment        ReviewAction = "COMMENT"
)

// PR represents a GitHub pull request.
type PR struct {
	Number          int
	Title           string
	Author          string
	RepoOwner       string
	Repo            string
	ReviewDecision  string // APPROVED / CHANGES_REQUESTED / REVIEW_REQUIRED / ""
	IsReviewRequest bool   // came from review-requested:@me query
	IsDraft         bool
	HeadSHA         string
	UpdatedAt       time.Time
}

// PRComment represents a review comment on a pull request.
type PRComment struct {
	ID        int
	Author    string
	Body      string
	Path      string // file path for line comments; "" for general comments
	Line      int    // diff line number; 0 for general comments
	CreatedAt time.Time
}

// ErrRateLimit is returned when the GitHub API rate limit is exceeded.
// Primary rate limit: 5,000 req/hr for REST, 30 req/min for Search API.
var ErrRateLimit = fmt.Errorf("github rate limit exceeded — try again later")

// runGh runs a gh CLI command and returns stdout.
// Timeout: 10 seconds. Returns ErrRateLimit for 403/429 responses.
func runGh(args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "gh", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		stderrStr := stderr.String()
		// gh CLI surfaces rate limit errors as "HTTP 403" or "HTTP 429" in stderr.
		// Both the primary REST limit (5k/hr) and Search limit (30/min) produce these.
		if strings.Contains(stderrStr, "HTTP 429") ||
			strings.Contains(stderrStr, "HTTP 403") && strings.Contains(stderrStr, "rate limit") ||
			strings.Contains(stderrStr, "API rate limit exceeded") ||
			strings.Contains(stderrStr, "secondary rate limit") {
			return "", ErrRateLimit
		}
		return "", fmt.Errorf("gh %s: %w: %s", strings.Join(args, " "), err, stderrStr)
	}
	return stdout.String(), nil
}

// FetchPRList returns all open PRs + review requests for the authenticated user.
// Uses gh search prs to work across all repos regardless of current directory.
func FetchPRList() ([]PR, error) {
	type ghSearchPR struct {
		Number    int    `json:"number"`
		Title     string `json:"title"`
		Author    struct{ Login string } `json:"author"`
		IsDraft   bool   `json:"isDraft"`
		Repository struct {
			NameWithOwner string `json:"nameWithOwner"`
		} `json:"repository"`
		UpdatedAt      time.Time `json:"updatedAt"`
		ReviewDecision string    `json:"reviewDecision"`
	}

	// Fetch PRs authored by current user
	myOut, myErr := runGh("search", "prs",
		"--author=@me", "--state=open",
		"--json", "number,title,author,isDraft,repository,updatedAt,reviewDecision",
		"--limit", "50")

	// Fetch PRs requesting review from current user
	reviewOut, reviewErr := runGh("search", "prs",
		"--review-requested=@me", "--state=open",
		"--json", "number,title,author,isDraft,repository,updatedAt,reviewDecision",
		"--limit", "50")

	if myErr != nil && reviewErr != nil {
		return nil, fmt.Errorf("gh search failed: %w", myErr)
	}

	var prs []PR
	seen := make(map[string]bool)
	var parseErr error

	parseSearchPRs := func(out string, isReviewReq bool) {
		if out == "" {
			return
		}
		var raw []ghSearchPR
		if err := json.Unmarshal([]byte(out), &raw); err != nil {
			parseErr = fmt.Errorf("parse search results: %w", err)
			return
		}
		for _, p := range raw {
			parts := strings.SplitN(p.Repository.NameWithOwner, "/", 2)
			var owner, repo string
			if len(parts) == 2 {
				owner, repo = parts[0], parts[1]
			} else {
				owner = p.Repository.NameWithOwner
				repo = p.Repository.NameWithOwner
			}
			key := fmt.Sprintf("%s/%s#%d", owner, repo, p.Number)
			if seen[key] {
				continue
			}
			seen[key] = true
			prs = append(prs, PR{
				Number:          p.Number,
				Title:           p.Title,
				Author:          p.Author.Login,
				RepoOwner:       owner,
				Repo:            repo,
				IsDraft:         p.IsDraft,
				IsReviewRequest: isReviewReq,
				ReviewDecision:  p.ReviewDecision,
				UpdatedAt:       p.UpdatedAt,
			})
		}
	}

	if myErr == nil {
		parseSearchPRs(myOut, false)
	}
	if reviewErr == nil {
		parseSearchPRs(reviewOut, true)
	}

	// Surface parse errors only when we got no PRs at all (partial results are
	// better than an error when one query succeeded and the other had bad JSON).
	if parseErr != nil && len(prs) == 0 {
		return nil, parseErr
	}

	return prs, nil
}

// FetchPRFiles returns the list of changed files for a given PR.
func FetchPRFiles(owner, repo string, number int) ([]string, error) {
	out, err := runGh("api",
		fmt.Sprintf("repos/%s/%s/pulls/%d/files", owner, repo, number),
		"--jq", ".[].filename")
	if err != nil {
		return nil, err
	}
	var files []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line != "" {
			files = append(files, line)
		}
	}
	return files, nil
}

// FetchPRFullDiff returns the complete unified diff for a PR.
// Callers should cache this and use ExtractFileDiff to slice per-file,
// avoiding repeated API calls when the user browses different files.
func FetchPRFullDiff(owner, repo string, number int) (string, error) {
	return runGh("pr", "diff", fmt.Sprintf("%d", number),
		"-R", fmt.Sprintf("%s/%s", owner, repo))
}

// ExtractFileDiff extracts the diff for a single file from a full unified diff.
// This is exported so ReviewsView can re-slice the cached full diff on file
// cursor changes without issuing additional API calls.
func ExtractFileDiff(fullDiff, filePath string) string {
	lines := strings.Split(fullDiff, "\n")
	var result []string
	inFile := false

	// Match the exact "diff --git a/<path> b/<path>" header to avoid false
	// matches on files whose names share a common prefix (e.g., "api" matching
	// "api_test.go"). We check both the suffix (" b/"+path) and full format.
	wantSuffix := " b/" + filePath

	for _, line := range lines {
		if strings.HasPrefix(line, "diff --git ") {
			inFile = strings.HasSuffix(line, wantSuffix)
		}
		if inFile {
			result = append(result, line)
		}
	}

	return strings.Join(result, "\n")
}

// FetchPRComments returns review comments for a PR.
func FetchPRComments(owner, repo string, number int) ([]PRComment, error) {
	out, err := runGh("api",
		fmt.Sprintf("repos/%s/%s/pulls/%d/comments", owner, repo, number))
	if err != nil {
		return nil, err
	}

	type ghComment struct {
		ID        int    `json:"id"`
		User      struct{ Login string } `json:"user"`
		Body      string `json:"body"`
		Path      string `json:"path"`
		Line      int    `json:"line"`
		CreatedAt time.Time `json:"created_at"`
	}

	var raw []ghComment
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		return nil, fmt.Errorf("parse comments: %w", err)
	}

	comments := make([]PRComment, len(raw))
	for i, c := range raw {
		comments[i] = PRComment{
			ID:        c.ID,
			Author:    c.User.Login,
			Body:      c.Body,
			Path:      c.Path,
			Line:      c.Line,
			CreatedAt: c.CreatedAt,
		}
	}
	return comments, nil
}

// PostReviewComment posts a single line comment on a PR diff.
func PostReviewComment(owner, repo string, number int, commitSHA, path string, line int, body string) error {
	_, err := runGh("api",
		fmt.Sprintf("repos/%s/%s/pulls/%d/comments", owner, repo, number),
		"-f", "body="+body,
		"-f", "path="+path,
		"-f", "commit_id="+commitSHA,
		"-F", fmt.Sprintf("line=%d", line),
	)
	return err
}

// SubmitReview submits a PR review (APPROVE, REQUEST_CHANGES, COMMENT).
func SubmitReview(owner, repo string, number int, action ReviewAction, body string) error {
	_, err := runGh("api",
		fmt.Sprintf("repos/%s/%s/pulls/%d/reviews", owner, repo, number),
		"-f", "body="+body,
		"-f", "event="+string(action),
	)
	return err
}
