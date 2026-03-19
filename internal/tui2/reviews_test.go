package tui2

import (
	"testing"
	"time"

	"github.com/drn/argus/internal/github"
)

func TestReviewsView_Empty(t *testing.T) {
	rv := NewReviewsView()
	if len(rv.prs) != 0 {
		t.Error("initial PRs should be empty")
	}
	if rv.focus != rfList {
		t.Error("initial focus should be list")
	}
}

func TestReviewsView_SetPRs(t *testing.T) {
	rv := NewReviewsView()
	prs := []github.PR{
		{Number: 1, Title: "My PR", Author: "me", IsReviewRequest: false},
		{Number: 2, Title: "Review me", Author: "them", IsReviewRequest: true},
	}
	rv.SetPRs(prs, nil)
	if len(rv.prs) != 2 {
		t.Fatalf("expected 2 PRs, got %d", len(rv.prs))
	}
	// Review requests should be sorted first.
	if !rv.prs[0].IsReviewRequest {
		t.Error("review requests should be sorted first")
	}
}

func TestReviewsView_SetPRs_Error(t *testing.T) {
	rv := NewReviewsView()
	rv.StartLoading()
	rv.SetPRs(nil, github.ErrRateLimit)
	if rv.loading {
		t.Error("loading should be false after error")
	}
	if rv.loadErr == "" {
		t.Error("loadErr should be set")
	}
}

func TestReviewsView_CanFetchPRList(t *testing.T) {
	rv := NewReviewsView()
	if !rv.CanFetchPRList() {
		t.Error("should be fetchable initially")
	}
	rv.lastFetchTime = time.Now()
	if rv.CanFetchPRList() {
		t.Error("should be blocked by cooldown")
	}
	rv.lastFetchTime = time.Now().Add(-prListCooldown - time.Second)
	if !rv.CanFetchPRList() {
		t.Error("should be fetchable after cooldown")
	}
}

func TestReviewsView_Navigation(t *testing.T) {
	rv := NewReviewsView()
	prs := []github.PR{
		{Number: 1, Title: "PR 1"},
		{Number: 2, Title: "PR 2"},
		{Number: 3, Title: "PR 3"},
	}
	rv.SetPRs(prs, nil)

	rv.cursorDown()
	if rv.prCursor != 1 {
		t.Errorf("cursor = %d, want 1", rv.prCursor)
	}
	rv.cursorDown()
	if rv.prCursor != 2 {
		t.Errorf("cursor = %d, want 2", rv.prCursor)
	}
	// Can't go past end.
	rv.cursorDown()
	if rv.prCursor != 2 {
		t.Errorf("cursor = %d, want 2 (clamped)", rv.prCursor)
	}
	rv.cursorUp()
	if rv.prCursor != 1 {
		t.Errorf("cursor = %d, want 1", rv.prCursor)
	}
}

func TestReviewsView_DiffScrolling(t *testing.T) {
	rv := NewReviewsView()
	rv.focus = rfDiff
	rv.cursorDown()
	if rv.diffScrollOff != 1 {
		t.Errorf("diffScrollOff = %d, want 1", rv.diffScrollOff)
	}
	rv.cursorUp()
	if rv.diffScrollOff != 0 {
		t.Errorf("diffScrollOff = %d, want 0", rv.diffScrollOff)
	}
}

func TestReviewsView_EscBack(t *testing.T) {
	rv := NewReviewsView()
	pr := github.PR{Number: 1, Title: "Test"}
	rv.selectedPR = &pr
	rv.files = []string{"a.go"}
	rv.focus = rfDiff

	// Esc from diff → list.
	rv.handleEsc()
	if rv.focus != rfList {
		t.Error("should return to list focus")
	}

	// Esc from list with selected PR → deselect.
	rv.handleEsc()
	if rv.selectedPR != nil {
		t.Error("should deselect PR")
	}
}

func TestReviewsView_MarkReviewDecision(t *testing.T) {
	rv := NewReviewsView()
	prs := []github.PR{
		{Number: 1, Title: "PR 1"},
		{Number: 2, Title: "PR 2"},
	}
	rv.SetPRs(prs, nil)
	pr := rv.prs[0]
	rv.selectedPR = &pr

	rv.MarkReviewDecision(1, github.ReviewApprove)
	if rv.prs[0].ReviewDecision != "APPROVED" {
		t.Errorf("decision = %q, want APPROVED", rv.prs[0].ReviewDecision)
	}
	if rv.selectedPR.ReviewDecision != "APPROVED" {
		t.Error("selectedPR should also be updated")
	}
}

func TestReviewsView_ReviewBadge(t *testing.T) {
	rv := NewReviewsView()
	tests := []struct {
		pr   github.PR
		want string
	}{
		{github.PR{IsDraft: true}, "[draft]"},
		{github.PR{ReviewDecision: "APPROVED"}, "✓"},
		{github.PR{ReviewDecision: "CHANGES_REQUESTED"}, "✗"},
		{github.PR{ReviewDecision: "REVIEW_REQUIRED"}, "?"},
		{github.PR{}, "·"},
	}
	for _, tt := range tests {
		got := rv.reviewBadge(tt.pr)
		if got != tt.want {
			t.Errorf("reviewBadge(%+v) = %q, want %q", tt.pr, got, tt.want)
		}
	}
}

func TestTruncString(t *testing.T) {
	if got := truncString("hello", 10); got != "hello" {
		t.Errorf("truncString short = %q", got)
	}
	if got := truncString("hello world", 5); got != "hell…" {
		t.Errorf("truncString long = %q", got)
	}
	if got := truncString("hi", 0); got != "" {
		t.Errorf("truncString zero = %q", got)
	}
}

func TestReviewsView_SetFiles(t *testing.T) {
	rv := NewReviewsView()
	rv.fullDiff = "old diff"
	rv.rawDiff = "old raw"
	rv.diffScrollOff = 5

	rv.SetFiles([]string{"a.go", "b.go"})

	if rv.fullDiff != "" {
		t.Error("fullDiff should be cleared")
	}
	if rv.diffScrollOff != 0 {
		t.Error("diffScrollOff should be reset")
	}
	if len(rv.files) != 2 {
		t.Errorf("files = %d, want 2", len(rv.files))
	}
}

func TestReviewsView_SetComments(t *testing.T) {
	rv := NewReviewsView()
	comments := []github.PRComment{
		{ID: 1, Author: "user", Body: "looks good"},
	}
	rv.SetComments(comments)
	if len(rv.comments) != 1 {
		t.Fatalf("expected 1 comment, got %d", len(rv.comments))
	}
	if rv.commentsFetchedAt.IsZero() {
		t.Error("commentsFetchedAt should be set")
	}
}

func TestReviewsView_BackgroundRefresh(t *testing.T) {
	rv := NewReviewsView()
	prs := []github.PR{
		{Number: 1, Title: "PR 1"},
		{Number: 2, Title: "PR 2"},
	}
	rv.SetPRs(prs, nil)
	rv.prCursor = 1

	// Background refresh with more PRs.
	newPRs := []github.PR{
		{Number: 1, Title: "PR 1"},
		{Number: 2, Title: "PR 2"},
		{Number: 3, Title: "PR 3"},
	}
	rv.SetPRs(newPRs, nil)

	// Cursor should be preserved.
	if rv.prCursor != 1 {
		t.Errorf("cursor = %d, want 1 (preserved)", rv.prCursor)
	}
}
