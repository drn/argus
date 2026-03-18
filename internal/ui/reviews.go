package ui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/drn/argus/internal/github"
)

// prListCooldown is the minimum time between PR list refreshes.
// 10 minutes balances freshness with GitHub API usage.
const prListCooldown = 10 * time.Minute

// commentsTTL is how long comments are considered fresh before auto-refresh.
// Comments change frequently (others reviewing in parallel), so 2 minutes is
// a reasonable balance between freshness and REST API usage (5k req/hr).
const commentsTTL = 2 * time.Minute

type reviewFocus int

const (
	focusList    reviewFocus = iota
	focusDiff
	focusComment
	focusApproveConfirm
)

// ReviewsView is the three-panel GitHub PR review interface.
// It follows the plain-struct pattern (not tea.Model) — root.go drives it.
type ReviewsView struct {
	theme         Theme
	width, height int

	// PR list panel
	prs          []github.PR
	prCursor     int
	prScrollOff  int
	loading      bool
	loadErr      string
	lastFetchTime time.Time // when the PR list was last successfully fetched

	// File list for selected PR
	selectedPR *github.PR
	files      []string
	fileCursor int

	// Diff panel
	// fullDiff is the complete PR diff fetched once per PR; individual file
	// diffs are extracted from it without additional API calls.
	fullDiff      string     // cached full diff for selectedPR
	diffFetchedAt time.Time  // when fullDiff was fetched; used for staleness check
	rawDiff       string     // extracted diff for the currently viewed file
	parsedDiff    *ParsedDiff
	unifiedLines  []string // cached RenderUnifiedLines output
	diffScrollOff int
	splitMode     bool

	// Comments panel
	// comments is fetched once per PR (not per file) and cached here.
	// commentsFetchedAt drives the 2-minute auto-refresh TTL.
	comments           []github.PRComment
	commentsFetchedAt  time.Time
	commentCursor      int

	// Comment compose
	focus     reviewFocus
	draftBody string
	draftLine int
	draftPath string

	// Review submission state
	submitErr string

	// reviewDraftMode distinguishes between a line comment (false) and a full
	// review REQUEST_CHANGES (true) when focus == focusComment.
	reviewDraftMode bool

	// Fetching guards — prevent the TickMsg auto-refresh from queuing multiple
	// concurrent gh CLI processes when a fetch is already in flight.
	commentsFetching bool
	diffFetching     bool
}

// NewReviewsView creates a new ReviewsView.
func NewReviewsView(theme Theme) *ReviewsView {
	return &ReviewsView{theme: theme}
}

// truncString returns s truncated to maxRunes runes, appending "..." if truncated.
// keepRunes is the number of runes to keep before "...". Uses rune arithmetic
// to avoid panics on non-ASCII characters.
func truncString(s string, maxRunes, keepRunes int) string {
	r := []rune(s)
	if len(r) <= maxRunes {
		return s
	}
	return string(r[:keepRunes]) + "..."
}

// StartLoading marks the PR list as loading and returns the fetch command.
// Use this instead of setting loading=true directly from outside the view.
func (rv *ReviewsView) StartLoading() tea.Cmd {
	rv.loading = true
	rv.loadErr = ""
	return fetchPRListCmd()
}

// SetSize propagates dimensions to internal components.
func (rv *ReviewsView) SetSize(w, h int) {
	rv.width = w
	rv.height = h
}

// canFetchPRList reports whether the cooldown has expired since the last fetch.
func (rv *ReviewsView) canFetchPRList() bool {
	return rv.lastFetchTime.IsZero() || time.Since(rv.lastFetchTime) >= prListCooldown
}

// SetPRs replaces the PR list. If this is a background refresh (cached data
// was already displayed), it preserves cursor/selection state. On first load
// (no cached data), it resets everything.
// Sorts review requests first to match the visual render order in renderPRList,
// so cursor navigation (sequential through the flat slice) matches top-to-bottom order.
func (rv *ReviewsView) SetPRs(prs []github.PR) {
	sort.SliceStable(prs, func(i, j int) bool {
		return prs[i].IsReviewRequest && !prs[j].IsReviewRequest
	})
	hadData := len(rv.prs) > 0 || rv.selectedPR != nil
	rv.lastFetchTime = time.Now()
	rv.prs = prs
	rv.loading = false
	rv.loadErr = ""

	if hadData {
		// Background refresh — keep cursor and selection intact.
		// Clamp cursor and scroll offset in case the list shrank.
		if rv.prCursor >= len(prs) {
			rv.prCursor = max(len(prs)-1, 0)
		}
		if rv.prScrollOff > rv.prCursor {
			rv.prScrollOff = rv.prCursor
		}
		return
	}

	// First load — reset everything.
	rv.prCursor = 0
	rv.prScrollOff = 0
	rv.selectedPR = nil
	rv.files = nil
	rv.fileCursor = 0
	rv.rawDiff = ""
	rv.parsedDiff = nil
	rv.unifiedLines = nil
	rv.diffScrollOff = 0
	rv.comments = nil
	rv.focus = focusList
}

// SetFiles sets the changed files for the currently selected PR.
// Clears the full diff and comment caches so fresh fetches are triggered.
func (rv *ReviewsView) SetFiles(files []string) {
	rv.files = files
	rv.fileCursor = 0
	rv.fullDiff = ""
	rv.diffFetchedAt = time.Time{}
	rv.rawDiff = ""
	rv.parsedDiff = nil
	rv.unifiedLines = nil
	rv.diffScrollOff = 0
	rv.comments = nil
	rv.commentsFetchedAt = time.Time{}
}

// SetFullDiff stores the complete PR diff and immediately extracts the view
// for the currently selected file — no further API calls needed for other files.
func (rv *ReviewsView) SetFullDiff(fullDiff string) {
	rv.fullDiff = fullDiff
	rv.diffFetchedAt = time.Now()
	rv.diffFetching = false
	rv.applyFileDiff()
}

// isDiffStale reports whether the cached diff is outdated relative to the PR's
// own UpdatedAt timestamp. When true, re-fetching is warranted.
// This check is free — UpdatedAt comes from the already-cached PR list.
// Returns false when a fetch is already in flight to prevent duplicate requests.
func (rv *ReviewsView) isDiffStale() bool {
	if rv.selectedPR == nil || rv.diffFetchedAt.IsZero() || rv.diffFetching {
		return false
	}
	return rv.selectedPR.UpdatedAt.After(rv.diffFetchedAt)
}

// areCommentsStale reports whether comments should be auto-refreshed.
// Returns true when the TTL has expired or the PR was updated after the last fetch.
// Returns false when a fetch is already in flight to prevent duplicate requests.
func (rv *ReviewsView) areCommentsStale() bool {
	if rv.selectedPR == nil || rv.commentsFetchedAt.IsZero() || rv.commentsFetching {
		return false
	}
	if time.Since(rv.commentsFetchedAt) >= commentsTTL {
		return true
	}
	return rv.selectedPR.UpdatedAt.After(rv.commentsFetchedAt)
}

// applyFileDiff extracts the diff for the currently selected file from the
// cached full diff. Called after SetFullDiff and on every file cursor change.
func (rv *ReviewsView) applyFileDiff() {
	rv.diffScrollOff = 0
	rv.unifiedLines = nil
	file := rv.SelectedFile()
	if file == "" || rv.fullDiff == "" {
		rv.rawDiff = ""
		rv.parsedDiff = nil
		return
	}
	rv.rawDiff = github.ExtractFileDiff(rv.fullDiff, file)
	pd := ParseUnifiedDiff(rv.rawDiff)
	rv.parsedDiff = &pd
}

// SetDiff sets the raw diff for the selected file and parses it.
// Used as a fallback when a full diff is not available.
func (rv *ReviewsView) SetDiff(diff string) {
	rv.rawDiff = diff
	rv.diffScrollOff = 0
	pd := ParseUnifiedDiff(diff)
	rv.parsedDiff = &pd
	rv.unifiedLines = nil
}

// SetComments replaces the comments for the selected PR and records fetch time.
func (rv *ReviewsView) SetComments(comments []github.PRComment) {
	rv.comments = comments
	rv.commentsFetchedAt = time.Now()
	rv.commentCursor = 0
	rv.commentsFetching = false
}

// MarkReviewDecision updates the in-memory ReviewDecision for the given PR number
// so the badge updates immediately without waiting for a full PR list refresh.
func (rv *ReviewsView) MarkReviewDecision(prNumber int, action github.ReviewAction) {
	var decision string
	switch action {
	case github.ReviewApprove:
		decision = "APPROVED"
	case github.ReviewRequestChanges:
		decision = "CHANGES_REQUESTED"
	default:
		return
	}
	for i := range rv.prs {
		if rv.prs[i].Number == prNumber {
			rv.prs[i].ReviewDecision = decision
			break
		}
	}
	if rv.selectedPR != nil && rv.selectedPR.Number == prNumber {
		rv.selectedPR.ReviewDecision = decision
	}
}

// SetLoadError records a load error.
func (rv *ReviewsView) SetLoadError(err string) {
	rv.loadErr = err
	rv.loading = false
}

// SelectedPR returns the currently selected PR, or nil.
func (rv *ReviewsView) SelectedPR() *github.PR {
	return rv.selectedPR
}

// SelectedFile returns the currently selected file path.
func (rv *ReviewsView) SelectedFile() string {
	if len(rv.files) == 0 || rv.fileCursor >= len(rv.files) {
		return ""
	}
	return rv.files[rv.fileCursor]
}

// DraftComment returns the current in-progress comment (path, line, body).
func (rv *ReviewsView) DraftComment() (path string, line int, body string) {
	return rv.draftPath, rv.draftLine, rv.draftBody
}

// HandleKey processes keyboard input and returns a tea.Cmd (may be nil).
func (rv *ReviewsView) HandleKey(msg tea.KeyMsg) tea.Cmd {
	if rv.focus == focusComment {
		return rv.handleCommentKey(msg)
	}
	if rv.focus == focusApproveConfirm {
		return rv.handleApproveConfirmKey(msg)
	}

	switch msg.String() {
	case "up", "k":
		rv.moveCursorUp()
	case "down", "j":
		rv.moveCursorDown()
	case "R":
		if !rv.canFetchPRList() {
			// Still in cooldown — show how long until next refresh is allowed
			remaining := prListCooldown - time.Since(rv.lastFetchTime)
			rv.loadErr = fmt.Sprintf("rate limit cooldown: wait %ds before refreshing", int(remaining.Seconds())+1)
			return nil
		}
		rv.loading = true
		rv.loadErr = ""
		return fetchPRListCmd()
	case "s":
		rv.splitMode = !rv.splitMode
		rv.unifiedLines = nil
	case "tab":
		if rv.selectedPR != nil {
			if rv.focus == focusList {
				rv.focus = focusDiff
			} else {
				rv.focus = focusList
			}
		}
	case "enter":
		return rv.handleEnter()
	case "esc":
		if rv.focus == focusDiff {
			rv.focus = focusList
			rv.rawDiff = ""
			rv.parsedDiff = nil
			rv.unifiedLines = nil
		} else if rv.selectedPR != nil {
			rv.selectedPR = nil
			rv.files = nil
			rv.focus = focusList
		}
	case "c":
		if rv.focus == focusDiff && rv.parsedDiff != nil {
			line := rv.currentDiffLine()
			if line == 0 {
				break // no valid diff line at current scroll position
			}
			rv.focus = focusComment
			rv.reviewDraftMode = false
			rv.draftBody = ""
			rv.draftPath = rv.SelectedFile()
			rv.draftLine = line
		}
	case "a":
		if rv.selectedPR != nil {
			rv.focus = focusApproveConfirm
		}
	case "r":
		// REQUEST_CHANGES requires a non-empty body per GitHub API.
		// Open the compose box in review-draft mode so the user can type one.
		if rv.selectedPR != nil {
			rv.focus = focusComment
			rv.reviewDraftMode = true
			rv.draftBody = ""
			rv.draftPath = ""
			rv.draftLine = 0
		}
	}
	return nil
}

func (rv *ReviewsView) handleCommentKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		rv.focus = focusDiff
		rv.draftBody = ""
		rv.reviewDraftMode = false
	case "ctrl+s":
		if rv.draftBody != "" && rv.selectedPR != nil {
			pr := rv.selectedPR
			path := rv.draftPath
			line := rv.draftLine
			body := rv.draftBody
			isDraftReview := rv.reviewDraftMode
			rv.focus = focusDiff
			rv.draftBody = ""
			rv.reviewDraftMode = false
			if isDraftReview {
				return func() tea.Msg {
					err := github.SubmitReview(pr.RepoOwner, pr.Repo, pr.Number, github.ReviewRequestChanges, body)
					return SubmitReviewMsg{Err: err, Action: github.ReviewRequestChanges, PRNumber: pr.Number}
				}
			}
			return postCommentCmd(pr, path, line, body)
		}
	case "enter":
		// Allow multi-line comments by inserting a newline.
		rv.draftBody += "\n"
	case "backspace":
		if len(rv.draftBody) > 0 {
			// Remove last rune (handles multi-byte UTF-8 correctly).
			runes := []rune(rv.draftBody)
			rv.draftBody = string(runes[:len(runes)-1])
		}
	default:
		// Accept printable characters
		if len(msg.Runes) > 0 {
			rv.draftBody += string(msg.Runes)
		}
	}
	return nil
}

func (rv *ReviewsView) handleApproveConfirmKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "y", "enter":
		pr := rv.selectedPR
		rv.focus = focusList
		return func() tea.Msg {
			err := github.SubmitReview(pr.RepoOwner, pr.Repo, pr.Number, github.ReviewApprove, "")
			return SubmitReviewMsg{Err: err, Action: github.ReviewApprove, PRNumber: pr.Number}
		}
	case "n", "esc":
		rv.focus = focusList
	}
	return nil
}

func (rv *ReviewsView) handleEnter() tea.Cmd {
	if rv.focus == focusList {
		if rv.selectedPR == nil {
			// Select a PR from the list
			if len(rv.prs) == 0 || rv.prCursor >= len(rv.prs) {
				return nil
			}
			pr := rv.prs[rv.prCursor]
			rv.selectedPR = &pr
			rv.files = nil
			rv.fileCursor = 0
			rv.rawDiff = ""
			rv.parsedDiff = nil
			rv.unifiedLines = nil
			return fetchPRFilesCmd(pr.RepoOwner, pr.Repo, pr.Number)
		}
		// Select a file from the file list
		if len(rv.files) > 0 && rv.fileCursor < len(rv.files) {
			pr := rv.selectedPR
			rv.focus = focusDiff
			// If we already have the full diff cached, extract immediately —
			// no API call needed. Otherwise fetch the full diff once.
			if rv.fullDiff != "" {
				rv.applyFileDiff()
				// Comments are already cached from the first file selection.
				return nil
			}
			rv.rawDiff = ""
			rv.parsedDiff = nil
			rv.unifiedLines = nil
			rv.diffScrollOff = 0
			rv.diffFetching = true
			rv.commentsFetching = true
			// Fetch full diff + comments in parallel (once per PR).
			return tea.Batch(
				fetchPRFullDiffCmd(pr.RepoOwner, pr.Repo, pr.Number),
				fetchPRCommentsCmd(pr.RepoOwner, pr.Repo, pr.Number),
			)
		}
	}
	return nil
}

func (rv *ReviewsView) moveCursorUp() {
	if rv.focus == focusDiff {
		if rv.diffScrollOff > 0 {
			rv.diffScrollOff--
		}
		return
	}
	// focusList
	if rv.selectedPR != nil {
		if rv.fileCursor > 0 {
			rv.fileCursor--
			// Re-slice from cached full diff immediately — no API call.
			if rv.fullDiff != "" {
				rv.applyFileDiff()
			}
		}
	} else {
		if rv.prCursor > 0 {
			rv.prCursor--
		}
	}
}

func (rv *ReviewsView) moveCursorDown() {
	if rv.focus == focusDiff {
		if rv.diffScrollOff < rv.maxDiffScroll() {
			rv.diffScrollOff++
		}
		return
	}
	// focusList
	if rv.selectedPR != nil {
		if rv.fileCursor < len(rv.files)-1 {
			rv.fileCursor++
			// Re-slice from cached full diff immediately — no API call.
			if rv.fullDiff != "" {
				rv.applyFileDiff()
			}
		}
	} else {
		if rv.prCursor < len(rv.prs)-1 {
			rv.prCursor++
		}
	}
}

func (rv *ReviewsView) maxDiffScroll() int {
	lines := rv.cachedDiffLines()
	h := rv.diffHeight()
	if len(lines) <= h {
		return 0
	}
	return len(lines) - h
}

func (rv *ReviewsView) diffHeight() int {
	h := rv.height - 4
	if h < 5 {
		h = 5
	}
	return h
}

func (rv *ReviewsView) cachedDiffLines() []string {
	if rv.parsedDiff == nil {
		return nil
	}
	if rv.unifiedLines == nil {
		rv.unifiedLines = RenderUnifiedLines(*rv.parsedDiff, rv.SelectedFile())
	}
	return rv.unifiedLines
}

// currentDiffLine returns the diff line number at the current scroll position.
// Returns 0 when the scroll position is out of range (e.g. after a diff reload
// with fewer lines). Callers must treat 0 as "no line selected" and guard
// before passing it to the GitHub API (line=0 is rejected by GitHub).
func (rv *ReviewsView) currentDiffLine() int {
	if rv.parsedDiff == nil || len(rv.parsedDiff.Hunks) == 0 {
		return 0
	}
	lineCount := 0
	for _, hunk := range rv.parsedDiff.Hunks {
		for _, dl := range hunk.Lines {
			if lineCount == rv.diffScrollOff {
				if dl.NewNum > 0 {
					return dl.NewNum
				}
				if dl.OldNum > 0 {
					return dl.OldNum
				}
				return 0
			}
			lineCount++
		}
	}
	return 0
}

// HandleMouse handles mouse wheel events for scrolling.
func (rv *ReviewsView) HandleMouse(msg tea.MouseMsg) {
	switch msg.Button {
	case tea.MouseButtonWheelUp:
		rv.scrollUp()
	case tea.MouseButtonWheelDown:
		rv.scrollDown()
	}
}

func (rv *ReviewsView) scrollUp() {
	switch rv.focus {
	case focusDiff:
		if rv.diffScrollOff > 0 {
			rv.diffScrollOff--
		}
	case focusList:
		if rv.selectedPR != nil {
			if rv.fileCursor > 0 {
				rv.fileCursor--
				if rv.fullDiff != "" {
					rv.applyFileDiff()
				}
			}
		} else {
			if rv.prCursor > 0 {
				rv.prCursor--
			}
		}
	case focusComment:
		if rv.commentCursor > 0 {
			rv.commentCursor--
		}
	}
}

func (rv *ReviewsView) scrollDown() {
	switch rv.focus {
	case focusDiff:
		if rv.diffScrollOff < rv.maxDiffScroll() {
			rv.diffScrollOff++
		}
	case focusList:
		if rv.selectedPR != nil {
			if rv.fileCursor < len(rv.files)-1 {
				rv.fileCursor++
				if rv.fullDiff != "" {
					rv.applyFileDiff()
				}
			}
		} else {
			if rv.prCursor < len(rv.prs)-1 {
				rv.prCursor++
			}
		}
	case focusComment:
		if rv.commentCursor < len(rv.comments)-1 {
			rv.commentCursor++
		}
	}
}

// View renders the left (PR list) panel content.
func (rv *ReviewsView) View() string {
	if rv.width == 0 || rv.height == 0 {
		return ""
	}
	if rv.loading && len(rv.prs) == 0 && rv.selectedPR == nil {
		return rv.theme.Dimmed.Render("Loading PRs...")
	}
	if rv.loadErr != "" && len(rv.prs) == 0 && rv.selectedPR == nil {
		return rv.theme.Error.Render("Error: " + rv.loadErr)
	}

	// Show file list if a PR is selected
	if rv.selectedPR != nil {
		return rv.renderFileList()
	}

	list := rv.renderPRList()

	// Append a background status indicator when cached data is visible.
	if rv.loading {
		list += "\n" + rv.theme.Dimmed.Render("  refreshing…")
	} else if rv.loadErr != "" {
		list += "\n" + rv.theme.Dimmed.Render("  refresh failed: "+rv.loadErr)
	}

	return list
}

func (rv *ReviewsView) renderPRList() string {
	if len(rv.prs) == 0 {
		return rv.theme.Dimmed.Render("No open PRs\n\nPress R to refresh")
	}

	// Separate into review requests and my PRs
	var reviewReqs, myPRs []github.PR
	var reviewReqIdxs, myPRIdxs []int
	for i, pr := range rv.prs {
		if pr.IsReviewRequest {
			reviewReqIdxs = append(reviewReqIdxs, i)
			reviewReqs = append(reviewReqs, pr)
		} else {
			myPRIdxs = append(myPRIdxs, i)
			myPRs = append(myPRs, pr)
		}
	}

	var b strings.Builder

	if len(reviewReqs) > 0 {
		b.WriteString(rv.theme.Section.Render("Review Requests"))
		b.WriteString("\n")
		for j, pr := range reviewReqs {
			globalIdx := reviewReqIdxs[j]
			rv.writePRRow(&b, pr, globalIdx == rv.prCursor)
		}
	}

	if len(myPRs) > 0 {
		if len(reviewReqs) > 0 {
			b.WriteString("\n")
		}
		b.WriteString(rv.theme.Section.Render("My Open PRs"))
		b.WriteString("\n")
		for j, pr := range myPRs {
			globalIdx := myPRIdxs[j]
			rv.writePRRow(&b, pr, globalIdx == rv.prCursor)
		}
	}

	b.WriteString("\n")
	// Show last fetch time + cooldown warning
	if !rv.lastFetchTime.IsZero() {
		age := time.Since(rv.lastFetchTime).Round(time.Second)
		b.WriteString(rv.theme.Dimmed.Render(fmt.Sprintf("updated %s ago", age)))
		b.WriteString("\n")
	}
	b.WriteString(rv.theme.Help.Render("[↑↓] navigate  [RET] select  [R] refresh"))

	return b.String()
}

func (rv *ReviewsView) renderFileList() string {
	var b strings.Builder
	pr := rv.selectedPR

	title := truncString(pr.Title, 35, 32)
	b.WriteString(rv.theme.Title.Render(title))
	b.WriteString("\n")
	b.WriteString(rv.theme.Dimmed.Render(fmt.Sprintf("#%d · %s/%s", pr.Number, pr.RepoOwner, pr.Repo)))
	b.WriteString("\n")
	switch pr.ReviewDecision {
	case "APPROVED":
		b.WriteString(rv.theme.Complete.Render("✓ Approved"))
	case "CHANGES_REQUESTED":
		b.WriteString(rv.theme.Error.Render("✗ Changes requested"))
	case "REVIEW_REQUIRED":
		b.WriteString(rv.theme.Dimmed.Render("? Review required"))
	}
	b.WriteString("\n")

	if len(rv.files) == 0 {
		b.WriteString(rv.theme.Dimmed.Render("Loading files..."))
		return b.String()
	}

	b.WriteString(rv.theme.Section.Render("Changed Files"))
	b.WriteString("\n")
	for i, f := range rv.files {
		name := f
		// Truncate long paths using rune-safe arithmetic to avoid panics on
		// non-ASCII characters (e.g., repos with Unicode in file paths).
		runes := []rune(name)
		if len(runes) > 30 {
			name = "…" + string(runes[len(runes)-29:])
		}
		if i == rv.fileCursor {
			b.WriteString(rv.theme.Selected.Render("▶ " + name))
		} else {
			b.WriteString(rv.theme.Normal.Render("  " + name))
		}
		if i < len(rv.files)-1 {
			b.WriteString("\n")
		}
	}

	b.WriteString("\n\n")
	b.WriteString(rv.theme.Help.Render("[RET] view diff  [esc] back"))

	return b.String()
}

func (rv *ReviewsView) writePRRow(b *strings.Builder, pr github.PR, selected bool) {
	title := truncString(pr.Title, 35, 32)

	var badge string
	switch pr.ReviewDecision {
	case "APPROVED":
		badge = " ✓"
	case "CHANGES_REQUESTED":
		badge = " ✗"
	case "REVIEW_REQUIRED":
		badge = " ?"
	}
	if pr.IsDraft {
		badge += " [draft]"
	}

	meta := fmt.Sprintf("#%d %s/%s", pr.Number, pr.RepoOwner, pr.Repo)

	if selected {
		b.WriteString(rv.theme.Selected.Render("▶ " + title + badge))
		b.WriteString("\n")
		b.WriteString(rv.theme.Dimmed.Render("  " + meta))
		b.WriteString("\n")
	} else {
		b.WriteString(rv.theme.Normal.Render("  " + title + badge))
		b.WriteString("\n")
		b.WriteString(rv.theme.Dimmed.Render("  " + meta))
		b.WriteString("\n")
	}
}

// RenderDiff renders the center (diff) panel content.
func (rv *ReviewsView) RenderDiff(w, h int) string {
	if rv.width == 0 || rv.height == 0 {
		return ""
	}
	if rv.parsedDiff == nil {
		if rv.selectedPR != nil && rv.focus == focusDiff {
			return rv.theme.Dimmed.Render("Loading diff...")
		}
		if rv.selectedPR != nil {
			return rv.theme.Dimmed.Render("Select a file to view diff\n\n[Tab] switch focus  [↑↓] navigate files")
		}
		return rv.theme.Dimmed.Render("Select a PR to view its diff")
	}

	lines := rv.cachedDiffLines()
	if len(lines) == 0 {
		return rv.theme.Dimmed.Render("(no changes in this file)")
	}

	file := rv.SelectedFile()
	visibleH := h - 4
	if visibleH < 1 {
		visibleH = 1
	}

	content := RenderUnified(lines, visibleH, rv.diffScrollOff)

	total := len(lines)
	scrollPct := 0
	if total > visibleH {
		scrollPct = rv.diffScrollOff * 100 / (total - visibleH)
	}
	footer := rv.theme.Dimmed.Render(fmt.Sprintf("  %s  [%d%%]  [↑↓] scroll  [c] comment  [Tab] focus list", file, scrollPct))

	return content + "\n" + footer
}

// RenderComments renders the right (comments + compose) panel content.
func (rv *ReviewsView) RenderComments(w, h int) string {
	if rv.width == 0 || rv.height == 0 {
		return ""
	}

	if rv.focus == focusComment {
		return rv.renderCommentCompose()
	}

	if rv.focus == focusApproveConfirm {
		return rv.renderApproveConfirm()
	}

	var b strings.Builder

	if len(rv.comments) == 0 {
		b.WriteString(rv.theme.Dimmed.Render("No comments"))
		if rv.selectedPR != nil {
			b.WriteString("\n\n")
			b.WriteString(rv.theme.Help.Render("[c] add comment"))
			b.WriteString("\n")
			b.WriteString(rv.theme.Help.Render("[a] approve"))
			b.WriteString("\n")
			b.WriteString(rv.theme.Help.Render("[r] request changes"))
		}
		return b.String()
	}

	b.WriteString(rv.theme.Title.Render(fmt.Sprintf("Comments (%d)", len(rv.comments))))
	b.WriteString("\n\n")

	maxVisible := h - 5
	if maxVisible < 1 {
		maxVisible = 1
	}
	start := 0
	if rv.commentCursor >= maxVisible {
		start = rv.commentCursor - maxVisible + 1
	}
	end := start + maxVisible
	if end > len(rv.comments) {
		end = len(rv.comments)
	}

	for i := start; i < end; i++ {
		c := rv.comments[i]
		header := rv.theme.Dimmed.Render(c.Author)
		if c.Path != "" {
			header += rv.theme.Dimmed.Render(fmt.Sprintf(" [%s:%d]", c.Path, c.Line))
		}
		body := truncString(c.Body, 60, 57)
		if i == rv.commentCursor {
			b.WriteString(rv.theme.Selected.Render("▶ ") + header + "\n")
			b.WriteString(rv.theme.Normal.Render("  "+body) + "\n\n")
		} else {
			b.WriteString("  " + header + "\n")
			b.WriteString(rv.theme.Dimmed.Render("  "+body) + "\n\n")
		}
	}

	return b.String()
}

func (rv *ReviewsView) renderApproveConfirm() string {
	pr := rv.selectedPR
	if pr == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString(rv.theme.Title.Render("Approve PR?"))
	b.WriteString("\n\n")
	title := truncString(pr.Title, 40, 37)
	b.WriteString(rv.theme.Normal.Render(title))
	b.WriteString("\n")
	b.WriteString(rv.theme.Dimmed.Render(fmt.Sprintf("#%d · %s/%s", pr.Number, pr.RepoOwner, pr.Repo)))
	b.WriteString("\n\n")
	b.WriteString(rv.theme.Help.Render("[y/enter] confirm  [n/esc] cancel"))
	return b.String()
}

func (rv *ReviewsView) renderCommentCompose() string {
	var b strings.Builder
	if rv.reviewDraftMode {
		b.WriteString(rv.theme.Title.Render("Request Changes"))
		b.WriteString("\n\n")
		b.WriteString(rv.theme.Dimmed.Render("Body (required by GitHub)"))
		b.WriteString("\n")
	} else {
		b.WriteString(rv.theme.Title.Render("New Comment"))
		b.WriteString("\n\n")
		if rv.draftPath != "" {
			b.WriteString(rv.theme.Dimmed.Render("File: " + rv.draftPath))
			b.WriteString("\n")
		}
		if rv.draftLine > 0 {
			b.WriteString(rv.theme.Dimmed.Render(fmt.Sprintf("Line: %d", rv.draftLine)))
			b.WriteString("\n")
		}
	}
	b.WriteString("\n")
	b.WriteString(rv.theme.Normal.Render(rv.draftBody))
	b.WriteString("█\n\n")
	b.WriteString(rv.theme.Help.Render("[enter] newline  [ctrl+s] submit  [esc] cancel"))
	return b.String()
}
