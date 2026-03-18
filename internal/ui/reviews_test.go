package ui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/drn/argus/internal/github"
)

func TestReviewsView_Empty(t *testing.T) {
	rv := NewReviewsView(DefaultTheme())
	rv.SetSize(120, 40)
	got := rv.View()
	if got == "" {
		t.Error("View() returned empty string with no PRs")
	}
	if !strings.Contains(got, "No open PRs") && !strings.Contains(got, "R") {
		t.Errorf("expected 'No open PRs' message, got: %q", got)
	}
}

func TestReviewsView_SetPRs(t *testing.T) {
	rv := NewReviewsView(DefaultTheme())
	rv.SetSize(120, 40)

	prs := []github.PR{
		{Number: 42, Title: "Fix critical bug", Author: "alice", RepoOwner: "org", Repo: "repo"},
		{Number: 43, Title: "Review request PR", Author: "bob", RepoOwner: "org", Repo: "repo", IsReviewRequest: true},
	}
	rv.SetPRs(prs)

	got := rv.View()
	if !strings.Contains(got, "Fix critical bug") {
		t.Errorf("expected PR title in View(), got: %q", got)
	}
	if !strings.Contains(got, "Review request PR") {
		t.Errorf("expected review request PR title in View(), got: %q", got)
	}
	if !strings.Contains(got, "Review Requests") {
		t.Errorf("expected 'Review Requests' section header, got: %q", got)
	}
	if !strings.Contains(got, "My Open PRs") {
		t.Errorf("expected 'My Open PRs' section header, got: %q", got)
	}
}

func TestReviewsView_Navigation(t *testing.T) {
	rv := NewReviewsView(DefaultTheme())
	rv.SetSize(120, 40)

	prs := []github.PR{
		{Number: 1, Title: "PR One", Author: "alice", RepoOwner: "org", Repo: "repo"},
		{Number: 2, Title: "PR Two", Author: "alice", RepoOwner: "org", Repo: "repo"},
		{Number: 3, Title: "PR Three", Author: "alice", RepoOwner: "org", Repo: "repo"},
	}
	rv.SetPRs(prs)

	// Initial position
	if rv.prCursor != 0 {
		t.Errorf("expected prCursor=0, got %d", rv.prCursor)
	}

	// Move down
	rv.HandleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	if rv.prCursor != 1 {
		t.Errorf("expected prCursor=1 after j, got %d", rv.prCursor)
	}

	// Move down again
	rv.HandleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	if rv.prCursor != 2 {
		t.Errorf("expected prCursor=2 after j, got %d", rv.prCursor)
	}

	// Can't go past end
	rv.HandleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	if rv.prCursor != 2 {
		t.Errorf("expected prCursor=2 (clamped), got %d", rv.prCursor)
	}

	// Move up
	rv.HandleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("k")})
	if rv.prCursor != 1 {
		t.Errorf("expected prCursor=1 after k, got %d", rv.prCursor)
	}
}

func TestReviewsView_ZeroDimensions(t *testing.T) {
	// Verify no panic with zero dimensions
	rv := NewReviewsView(DefaultTheme())
	// Don't call SetSize — width/height remain 0

	defer func() {
		if r := recover(); r != nil {
			t.Errorf("View() panicked with zero dimensions: %v", r)
		}
	}()

	_ = rv.View()
	_ = rv.RenderDiff(0, 0)
	_ = rv.RenderComments(0, 0)
}

func TestReviewsView_HandleKey_Refresh(t *testing.T) {
	rv := NewReviewsView(DefaultTheme())
	rv.SetSize(120, 40)

	// First refresh always succeeds (no cooldown yet)
	cmd := rv.HandleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("R")})
	if cmd == nil {
		t.Error("expected non-nil cmd from R key on first press")
	}
}

func TestReviewsView_HandleKey_RefreshCooldown(t *testing.T) {
	rv := NewReviewsView(DefaultTheme())
	rv.SetSize(120, 40)

	// Simulate that we just fetched
	rv.lastFetchTime = time.Now()

	// Refresh should be blocked by cooldown
	cmd := rv.HandleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("R")})
	if cmd != nil {
		t.Error("expected nil cmd from R key during cooldown")
	}
	if !strings.Contains(rv.loadErr, "cooldown") {
		t.Errorf("expected cooldown error message, got: %q", rv.loadErr)
	}
}

func TestReviewsView_SelectPR(t *testing.T) {
	rv := NewReviewsView(DefaultTheme())
	rv.SetSize(120, 40)

	prs := []github.PR{
		{Number: 42, Title: "Fix bug", Author: "alice", RepoOwner: "org", Repo: "repo"},
	}
	rv.SetPRs(prs)

	// Press Enter to select the PR
	cmd := rv.HandleKey(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Error("expected non-nil cmd after selecting PR")
	}
	if rv.selectedPR == nil {
		t.Error("expected selectedPR to be set after Enter")
	}
	if rv.selectedPR.Number != 42 {
		t.Errorf("expected PR #42 selected, got #%d", rv.selectedPR.Number)
	}
}

func TestReviewsView_EscBack(t *testing.T) {
	rv := NewReviewsView(DefaultTheme())
	rv.SetSize(120, 40)

	prs := []github.PR{
		{Number: 1, Title: "PR", Author: "a", RepoOwner: "o", Repo: "r"},
	}
	rv.SetPRs(prs)
	rv.HandleKey(tea.KeyMsg{Type: tea.KeyEnter}) // select PR

	if rv.selectedPR == nil {
		t.Fatal("setup: expected PR to be selected")
	}

	// Esc clears selection
	rv.HandleKey(tea.KeyMsg{Type: tea.KeyEsc})
	if rv.selectedPR != nil {
		t.Error("expected selectedPR to be cleared after Esc")
	}
}

func TestReviewsView_DiffRender(t *testing.T) {
	rv := NewReviewsView(DefaultTheme())
	rv.SetSize(120, 40)

	pr := github.PR{Number: 1, Title: "PR", Author: "a", RepoOwner: "o", Repo: "r", HeadSHA: "abc"}
	rv.SetPRs([]github.PR{pr})
	rv.HandleKey(tea.KeyMsg{Type: tea.KeyEnter}) // select PR
	rv.SetFiles([]string{"main.go"})

	rawDiff := `diff --git a/main.go b/main.go
--- a/main.go
+++ b/main.go
@@ -1,3 +1,4 @@
 package main
+// added line
 func main() {}
`
	rv.SetDiff(rawDiff)
	rv.focus = focusDiff

	got := rv.RenderDiff(60, 20)
	if got == "" {
		t.Error("RenderDiff() returned empty string with valid diff")
	}
}

func TestReviewsView_ReviewDecisionBadges(t *testing.T) {
	rv := NewReviewsView(DefaultTheme())
	rv.SetSize(120, 40)

	prs := []github.PR{
		{Number: 1, Title: "Approved PR", Author: "a", RepoOwner: "o", Repo: "r", ReviewDecision: "APPROVED"},
		{Number: 2, Title: "Changes PR", Author: "a", RepoOwner: "o", Repo: "r", ReviewDecision: "CHANGES_REQUESTED"},
		{Number: 3, Title: "Review PR", Author: "a", RepoOwner: "o", Repo: "r", ReviewDecision: "REVIEW_REQUIRED"},
		{Number: 4, Title: "No Decision PR", Author: "a", RepoOwner: "o", Repo: "r", ReviewDecision: ""},
	}
	rv.SetPRs(prs)

	got := rv.View()
	if !strings.Contains(got, "✓") {
		t.Error("expected ✓ badge for APPROVED PR")
	}
	if !strings.Contains(got, "✗") {
		t.Error("expected ✗ badge for CHANGES_REQUESTED PR")
	}
	if !strings.Contains(got, "?") {
		t.Error("expected ? badge for REVIEW_REQUIRED PR")
	}
}

func TestTruncString(t *testing.T) {
	tests := []struct {
		s        string
		maxRunes int
		keepRunes int
		want     string
	}{
		{"short", 10, 7, "short"},
		{"a longer string here", 10, 7, "a longe..."},
		{"", 10, 7, ""},
		{"exactly10!", 10, 7, "exactly10!"},
		{"exactly11!!", 10, 7, "exactly..."},
	}
	for _, tt := range tests {
		got := truncString(tt.s, tt.maxRunes, tt.keepRunes)
		if got != tt.want {
			t.Errorf("truncString(%q, %d, %d) = %q, want %q", tt.s, tt.maxRunes, tt.keepRunes, got, tt.want)
		}
	}
}

func TestReviewsView_SetPRs_SortOrder(t *testing.T) {
	rv := NewReviewsView(DefaultTheme())
	rv.SetSize(120, 40)

	// Pass review requests after my PRs — SetPRs should sort review requests first.
	prs := []github.PR{
		{Number: 1, Title: "My PR 1", Author: "me", RepoOwner: "o", Repo: "r", IsReviewRequest: false},
		{Number: 2, Title: "My PR 2", Author: "me", RepoOwner: "o", Repo: "r", IsReviewRequest: false},
		{Number: 3, Title: "Review Req 1", Author: "bob", RepoOwner: "o", Repo: "r", IsReviewRequest: true},
		{Number: 4, Title: "Review Req 2", Author: "alice", RepoOwner: "o", Repo: "r", IsReviewRequest: true},
	}
	rv.SetPRs(prs)

	// After SetPRs, review requests should be at the front of the slice
	// so cursor navigation matches the visual render order.
	if len(rv.prs) != 4 {
		t.Fatalf("expected 4 PRs, got %d", len(rv.prs))
	}
	if !rv.prs[0].IsReviewRequest || !rv.prs[1].IsReviewRequest {
		t.Errorf("expected first 2 PRs to be review requests, got IsReviewRequest=%v,%v",
			rv.prs[0].IsReviewRequest, rv.prs[1].IsReviewRequest)
	}
	if rv.prs[2].IsReviewRequest || rv.prs[3].IsReviewRequest {
		t.Errorf("expected last 2 PRs to be my PRs, got IsReviewRequest=%v,%v",
			rv.prs[2].IsReviewRequest, rv.prs[3].IsReviewRequest)
	}

	// Cursor at 0 should select the first review request
	if rv.prCursor != 0 {
		t.Errorf("expected prCursor=0, got %d", rv.prCursor)
	}
	if rv.prs[rv.prCursor].Title != "Review Req 1" {
		t.Errorf("expected cursor on 'Review Req 1', got %q", rv.prs[rv.prCursor].Title)
	}

	// Navigate down twice to reach "My PR 1"
	rv.HandleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	rv.HandleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	if rv.prs[rv.prCursor].Title != "My PR 1" {
		t.Errorf("expected cursor on 'My PR 1' after 2 downs, got %q", rv.prs[rv.prCursor].Title)
	}
}

func TestReviewsView_CanFetchPRList(t *testing.T) {
	rv := NewReviewsView(DefaultTheme())

	// Should be allowed initially (no previous fetch)
	if !rv.canFetchPRList() {
		t.Error("expected canFetchPRList()=true on first call")
	}

	// After SetPRs, lastFetchTime is set — should be blocked
	rv.SetPRs(nil)
	if rv.canFetchPRList() {
		t.Error("expected canFetchPRList()=false immediately after fetch")
	}

	// After cooldown expires, should be allowed again
	rv.lastFetchTime = time.Now().Add(-prListCooldown - time.Second)
	if !rv.canFetchPRList() {
		t.Error("expected canFetchPRList()=true after cooldown expires")
	}
}
