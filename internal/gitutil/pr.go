package gitutil

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/uxlog"
)

// errGhAbsent is a sentinel used by prRunner to signal that gh is not
// installed. Callers map this to PRUnknown and log it at most once.
var errGhAbsent = errors.New("gh not found in PATH")

// ghAbsentOnce guards the single uxlog line emitted when gh is not installed.
// ghUnauthOnce guards the distinct line emitted when gh is installed but not
// authenticated. They are separate so each remediation ("install gh" vs
// "gh auth login") logs exactly once regardless of which condition is seen
// first. Reset via ResetGhLogged in tests.
var (
	ghAbsentOnce sync.Once
	ghUnauthOnce sync.Once
)

// ResetGhLogged resets both once-guards so tests can verify the log-once
// behaviour across multiple FetchPRState calls. Not for production use.
func ResetGhLogged() {
	ghAbsentOnce = sync.Once{}
	ghUnauthOnce = sync.Once{}
}

// prRunner is the test seam for executing gh. The real implementation
// resolves gh via exec.LookPath, runs the command in dir, and returns the
// combined stdout+stderr output, the exit code (0 on success), and any
// exec-level error. Tests swap this variable to inject canned output.
//
// The separation between prRunner (process execution) and mapPRState
// (pure JSON parsing) lets the bulk of coverage run without spawning
// any processes.
var prRunner = func(ctx context.Context, dir string, args ...string) (string, int, error) {
	if _, err := exec.LookPath("gh"); err != nil {
		return "", 0, errGhAbsent
	}
	// Use the literal "gh" (not the LookPath-resolved variable) as the command
	// name so the binary is a constant — exec.CommandContext performs its own
	// PATH resolution. Passing a variable as the command name trips gosec G204;
	// the literal keeps it constant while args remain a fixed call-site list.
	cmd := exec.CommandContext(ctx, "gh", args...)
	cmd.Dir = dir
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	runErr := cmd.Run()
	exitCode := 0
	if cmd.ProcessState != nil {
		exitCode = cmd.ProcessState.ExitCode()
	}
	// Propagate exec errors (e.g., context.DeadlineExceeded) but still
	// return the output so callers can inspect the text.
	return buf.String(), exitCode, runErr
}

// prJSON is the subset of the gh pr view JSON we care about.
type prJSON struct {
	State          string `json:"state"`
	IsDraft        bool   `json:"isDraft"`
	ReviewDecision string `json:"reviewDecision"`
	URL            string `json:"url"`
}

// mapPRState is a pure function that converts raw gh output + exit code into a
// (PRState, url, error) triple. It is the only place that knows the
// gh-JSON→PRState mapping table from the design doc.
//
// Invariant: non-nil error means the result is transient (caller must
// keep-stale). A nil error with PRNone is an unambiguous "no PR" that is safe
// to persist.
func mapPRState(raw string, exitCode int) (model.PRState, string, error) {
	// Non-zero exit: distinguish "no PR" from every other failure.
	if exitCode != 0 {
		if strings.Contains(raw, "no pull requests found") {
			return model.PRNone, "", nil
		}
		// Auth errors and other non-zero exits without the "no PR" text are
		// treated as transient failures that should result in PRUnknown.
		// We return an error so the caller knows NOT to clobber the cache.
		// However, we also need to signal the caller to log this as an auth
		// issue. We return a special error wrapping the output.
		if isAuthError(raw) {
			return model.PRUnknown, "", errGhUnauthenticated(raw)
		}
		return model.PRNone, "", fmt.Errorf("gh exited %d: %s", exitCode, strings.TrimSpace(raw))
	}

	// Zero exit: parse JSON. json.Unmarshal is lenient by design — it ignores
	// any unknown fields, so a future gh release adding keys to the pr-view
	// payload won't break parsing.
	var p prJSON
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return model.PRNone, "", fmt.Errorf("parse gh json: %w", err)
	}

	switch p.State {
	case "MERGED", "CLOSED":
		return model.PRMergedClosed, p.URL, nil
	case "OPEN":
		if p.IsDraft {
			return model.PRDraft, p.URL, nil
		}
		switch p.ReviewDecision {
		case "CHANGES_REQUESTED":
			return model.PRChangesRequested, p.URL, nil
		case "APPROVED":
			return model.PRApproved, p.URL, nil
		default:
			// Covers "" and "REVIEW_REQUIRED" and any future unknown values.
			return model.PRAwaitingReview, p.URL, nil
		}
	default:
		return model.PRNone, "", fmt.Errorf("unexpected gh pr state %q", p.State)
	}
}

// errGhAuth is a sentinel type for unauthenticated gh calls.
type errGhAuth struct{ msg string }

func (e *errGhAuth) Error() string { return "gh unauthenticated: " + e.msg }

func errGhUnauthenticated(raw string) error {
	return &errGhAuth{msg: strings.TrimSpace(raw)}
}

// isAuthError returns true when gh output looks like an authentication failure.
func isAuthError(raw string) bool {
	lower := strings.ToLower(raw)
	return strings.Contains(lower, "auth login") ||
		strings.Contains(lower, "gh_token") ||
		strings.Contains(lower, "authentication") ||
		strings.Contains(lower, "not logged in")
}

// FetchPRState queries the GitHub CLI for the PR state of branch in the
// given worktree directory. It uses prRunner as its execution seam so tests
// can inject fake output without spawning real processes.
//
// Return contract (mirrors design.md keep-stale rule):
//   - (state, url, nil)  — success; persist this value.
//   - (PRNone, "", nil)  — unambiguous "no PR"; safe to persist.
//   - (PRUnknown, "", nil) — gh absent/unauthenticated; log once, keep stale.
//   - (_, _, err)        — transient failure; caller must keep stale.
func FetchPRState(ctx context.Context, worktreeDir, branch string) (model.PRState, string, error) {
	const timeout = 5 * time.Second
	fetchCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	raw, code, runErr := prRunner(fetchCtx, worktreeDir, "pr", "view", branch, "--json", "state,isDraft,reviewDecision,url")

	// gh not installed.
	if errors.Is(runErr, errGhAbsent) {
		ghAbsentOnce.Do(func() {
			uxlog.Log("[pr] gh not found in PATH — install GitHub CLI to enable PR state detection")
		})
		return model.PRUnknown, "", nil
	}

	// exec-level failure with no exit code (process never started, e.g. ENOENT
	// after LookPath, or context cancelled before cmd.Start). These are
	// transient — the caller must keep the cached value.
	if runErr != nil && code == 0 {
		return model.PRNone, "", fmt.Errorf("gh exec error: %w", runErr)
	}

	state, url, err := mapPRState(raw, code)
	if err != nil {
		// Check if this is an auth error (log once).
		var authErr *errGhAuth
		if errors.As(err, &authErr) {
			ghUnauthOnce.Do(func() {
				uxlog.Log("[pr] gh unauthenticated — run `gh auth login` to enable PR state detection: %s", authErr.msg)
			})
			return model.PRUnknown, "", nil
		}
		return model.PRNone, "", err
	}
	return state, url, nil
}
