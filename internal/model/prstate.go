package model

import "fmt"

// PRState represents the review state of a GitHub pull request associated
// with a task branch. It is glyph-agnostic; rendering lives in the theme
// and TUI layers.
type PRState int

const (
	// PRNone means no pull request exists for the branch.
	PRNone PRState = iota
	// PRDraft means the PR exists but is still in draft state.
	PRDraft
	// PRAwaitingReview means the PR is open, non-draft, and has not yet
	// received a review decision (or the decision is REVIEW_REQUIRED).
	PRAwaitingReview
	// PRChangesRequested means at least one reviewer requested changes.
	PRChangesRequested
	// PRApproved means the PR has been approved by all required reviewers.
	PRApproved
	// PRMergedClosed means the PR was merged or closed.
	PRMergedClosed
	// PRUnknown is the fallback when gh is unavailable or unauthenticated.
	// It is not an error visible to the user; the indicator cell renders blank.
	PRUnknown
)

var prStateNames = [...]string{
	"none",
	"draft",
	"awaiting-review",
	"changes-requested",
	"approved",
	"merged-closed",
	"unknown",
}

// String returns the stable string representation used for persistence and
// JSON serialization.
func (s PRState) String() string {
	if int(s) >= 0 && int(s) < len(prStateNames) {
		return prStateNames[s]
	}
	return fmt.Sprintf("unknown(%d)", int(s))
}

// IsTerminal reports whether the PR state can never change again. A merged or
// closed PR (both collapse into PRMergedClosed) is terminal; every other state
// — including PRNone (a branch may yet get a PR) and PRUnknown (gh may become
// available) — is non-terminal. The daemon PR poller uses this to permanently
// exclude terminal-state tasks from its eligible set, conserving the GitHub
// API budget. Centralizing the rule here keeps the terminal definition in one
// place instead of scattering string compares across callers.
func (s PRState) IsTerminal() bool {
	return s == PRMergedClosed
}

// IsActionable reports whether the state represents a PR still awaiting human
// attention — the only states a "PR" badge/glyph should render for. This is
// the single source of truth shared by theme.PRGlyph (task list) and the
// native Hera rail/details PR indicators, so all three surfaces hide the
// badge together once a PR is merged, closed, draft, or its state is unknown.
func (s PRState) IsActionable() bool {
	switch s {
	case PRAwaitingReview, PRChangesRequested, PRApproved:
		return true
	default:
		return false
	}
}

// ParsePRState converts a stable string name (e.g. "awaiting-review") into
// a PRState. Returns an error for unrecognized values.
func ParsePRState(str string) (PRState, error) {
	for i, name := range prStateNames {
		if name == str {
			return PRState(i), nil
		}
	}
	return PRNone, fmt.Errorf("unknown pr state: %q", str)
}

// MarshalText implements encoding.TextMarshaler so PRState round-trips
// through JSON, YAML, and any encoding that calls MarshalText.
func (s PRState) MarshalText() ([]byte, error) {
	return []byte(s.String()), nil
}

// UnmarshalText implements encoding.TextUnmarshaler.
func (s *PRState) UnmarshalText(data []byte) error {
	parsed, err := ParsePRState(string(data))
	if err != nil {
		return err
	}
	*s = parsed
	return nil
}
