package tui2

import (
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/drn/argus/internal/gitutil"
	"github.com/drn/argus/internal/github"
	"github.com/drn/argus/internal/uxlog"
)

const (
	prListCooldown = 10 * time.Minute
	commentsTTL    = 2 * time.Minute
)

// reviewFocus identifies which panel has focus.
type reviewFocus int

const (
	rfList reviewFocus = iota
	rfDiff
	rfComment
	rfApproveConfirm
)

// ReviewsView is the tcell reviews tab with three panels.
type ReviewsView struct {
	*tview.Box
	mu sync.Mutex

	// PR list state.
	prs           []github.PR
	prCursor      int
	prScrollOff   int
	loading       bool
	loadErr       string
	lastFetchTime time.Time

	// Selected PR / files.
	selectedPR *github.PR
	files      []string
	fileCursor int

	// Diff state.
	fullDiff         string
	diffFetchedAt    time.Time
	rawDiff          string
	parsedDiff       *gitutil.ParsedDiff
	diffRendered     []renderedDiffLine // pre-rendered syntax-highlighted lines
	diffScrollOff    int
	splitMode        bool
	diffFetching     bool

	// Comments state.
	comments         []github.PRComment
	commentsFetchedAt time.Time
	commentCursor    int
	commentsFetching bool

	// Comment compose / review action.
	focus          reviewFocus
	draftBody      string
	draftLine      int
	draftPath      string
	reviewDraftMode bool
	submitErr      string

	// Callback to trigger async fetches from the App.
	onFetch func(fn func())
}

// NewReviewsView creates a new reviews panel.
func NewReviewsView() *ReviewsView {
	return &ReviewsView{
		Box:   tview.NewBox(),
		focus: rfList,
	}
}

// SetOnFetch sets the callback for async operations.
// The callback should call fn in a goroutine and then QueueUpdateDraw.
func (rv *ReviewsView) SetOnFetch(cb func(fn func())) {
	rv.onFetch = cb
}

// --- Data setters ---

func (rv *ReviewsView) StartLoading() {
	rv.loading = true
	rv.loadErr = ""
}

func (rv *ReviewsView) CanFetchPRList() bool {
	if rv.lastFetchTime.IsZero() {
		return true
	}
	return time.Since(rv.lastFetchTime) >= prListCooldown
}

func (rv *ReviewsView) SetPRs(prs []github.PR, err error) {
	rv.loading = false
	if err != nil {
		rv.loadErr = err.Error()
		uxlog.Log("[reviews] fetch error: %v", err)
		return
	}
	uxlog.Log("[reviews] fetched %d PRs", len(prs))

	// Sort: review requests first, then my PRs.
	sort.SliceStable(prs, func(i, j int) bool {
		if prs[i].IsReviewRequest != prs[j].IsReviewRequest {
			return prs[i].IsReviewRequest
		}
		return false
	})

	firstLoad := len(rv.prs) == 0
	rv.prs = prs
	rv.lastFetchTime = time.Now()

	if firstLoad {
		rv.prCursor = 0
		rv.prScrollOff = 0
		rv.selectedPR = nil
		rv.files = nil
		rv.focus = rfList
	} else {
		if rv.prCursor >= len(rv.prs) {
			rv.prCursor = max(0, len(rv.prs)-1)
		}
	}
}

func (rv *ReviewsView) SetFiles(files []string) {
	rv.files = files
	rv.fileCursor = 0
	rv.fullDiff = ""
	rv.rawDiff = ""
	rv.parsedDiff = nil
	rv.diffRendered = nil
	rv.diffScrollOff = 0
	rv.diffFetchedAt = time.Time{}
}

func (rv *ReviewsView) SetFullDiff(diff string) {
	rv.fullDiff = diff
	rv.diffFetchedAt = time.Now()
	rv.diffFetching = false
	rv.applyFileDiff()
}

func (rv *ReviewsView) SetComments(comments []github.PRComment) {
	rv.comments = comments
	rv.commentsFetchedAt = time.Now()
	rv.commentsFetching = false
	rv.commentCursor = 0
	uxlog.Log("[reviews] loaded %d comments", len(comments))
}

func (rv *ReviewsView) MarkReviewDecision(prNumber int, action github.ReviewAction) {
	decision := ""
	switch action {
	case github.ReviewApprove:
		decision = "APPROVED"
	case github.ReviewRequestChanges:
		decision = "CHANGES_REQUESTED"
	}
	for i := range rv.prs {
		if rv.prs[i].Number == prNumber {
			rv.prs[i].ReviewDecision = decision
		}
	}
	if rv.selectedPR != nil && rv.selectedPR.Number == prNumber {
		rv.selectedPR.ReviewDecision = decision
	}
}

// --- Staleness checks ---

func (rv *ReviewsView) IsDiffStale() bool {
	if rv.selectedPR == nil || rv.fullDiff == "" {
		return false
	}
	return rv.selectedPR.UpdatedAt.After(rv.diffFetchedAt)
}

func (rv *ReviewsView) AreCommentsStale() bool {
	if rv.selectedPR == nil {
		return false
	}
	if rv.commentsFetchedAt.IsZero() {
		return true
	}
	if time.Since(rv.commentsFetchedAt) > commentsTTL {
		return true
	}
	return rv.selectedPR.UpdatedAt.After(rv.commentsFetchedAt)
}

func (rv *ReviewsView) SelectedPR() *github.PR {
	return rv.selectedPR
}

func (rv *ReviewsView) DiffFetching() bool  { return rv.diffFetching }
func (rv *ReviewsView) CommentsFetching() bool { return rv.commentsFetching }

// --- Internal helpers ---

func (rv *ReviewsView) applyFileDiff() {
	if rv.fullDiff == "" || len(rv.files) == 0 {
		return
	}
	filePath := rv.files[rv.fileCursor]
	rv.rawDiff = github.ExtractFileDiff(rv.fullDiff, filePath)
	if rv.rawDiff != "" {
		pd := gitutil.ParseUnifiedDiff(rv.rawDiff)
		rv.parsedDiff = &pd
		rv.diffRendered = buildUnifiedDiffLines(pd, filePath)
	} else {
		rv.parsedDiff = nil
		rv.diffRendered = nil
	}
	rv.diffScrollOff = 0
}

func (rv *ReviewsView) currentDiffLine() (path string, line int) {
	if rv.parsedDiff == nil || len(rv.files) == 0 {
		return "", 0
	}
	path = rv.files[rv.fileCursor]
	// Walk through diff lines to find the line at scrollOff position.
	pos := 0
	for _, hunk := range rv.parsedDiff.Hunks {
		pos++ // hunk header
		for _, dl := range hunk.Lines {
			if pos == rv.diffScrollOff {
				if dl.NewNum > 0 {
					return path, dl.NewNum
				}
				if dl.OldNum > 0 {
					return path, dl.OldNum
				}
			}
			pos++
		}
	}
	return path, 0
}

// --- Key handling ---

func (rv *ReviewsView) HandleKey(ev *tcell.EventKey, app *App) bool {
	switch rv.focus {
	case rfApproveConfirm:
		return rv.handleApproveConfirmKey(ev, app)
	case rfComment:
		return rv.handleCommentKey(ev, app)
	default:
		return rv.handleNormalKey(ev, app)
	}
}

func (rv *ReviewsView) handleNormalKey(ev *tcell.EventKey, app *App) bool {
	switch ev.Key() {
	case tcell.KeyUp:
		rv.cursorUp()
		return true
	case tcell.KeyDown:
		rv.cursorDown()
		return true
	case tcell.KeyEnter:
		rv.handleEnter(app)
		return true
	case tcell.KeyEscape:
		rv.handleEsc()
		return true
	case tcell.KeyTab:
		if rv.selectedPR != nil {
			if rv.focus == rfList {
				rv.focus = rfDiff
			} else {
				rv.focus = rfList
			}
		}
		return true
	case tcell.KeyRune:
		switch ev.Rune() {
		case 'j':
			rv.cursorDown()
			return true
		case 'k':
			rv.cursorUp()
			return true
		case 'o':
			rv.openPRInBrowser()
			return true
		case 'R':
			rv.handleRefresh(app)
			return true
		case 'c':
			if rv.focus == rfDiff && rv.selectedPR != nil {
				path, line := rv.currentDiffLine()
				if line > 0 {
					rv.draftPath = path
					rv.draftLine = line
					rv.draftBody = ""
					rv.reviewDraftMode = false
					rv.focus = rfComment
				}
			}
			return true
		case 'a':
			if rv.focus == rfDiff && rv.selectedPR != nil {
				rv.focus = rfApproveConfirm
			}
			return true
		case 'r':
			if rv.focus == rfDiff && rv.selectedPR != nil {
				rv.draftBody = ""
				rv.reviewDraftMode = true
				rv.focus = rfComment
			}
			return true
		}
	}
	return false
}

func (rv *ReviewsView) handleCommentKey(ev *tcell.EventKey, app *App) bool {
	switch ev.Key() {
	case tcell.KeyEscape:
		rv.focus = rfDiff
		return true
	case tcell.KeyBackspace, tcell.KeyBackspace2:
		if len(rv.draftBody) > 0 {
			_, size := utf8.DecodeLastRuneInString(rv.draftBody)
			rv.draftBody = rv.draftBody[:len(rv.draftBody)-size]
		}
		return true
	case tcell.KeyEnter:
		rv.draftBody += "\n"
		return true
	case tcell.KeyCtrlS:
		rv.submitComment(app)
		return true
	case tcell.KeyRune:
		rv.draftBody += string(ev.Rune())
		return true
	}
	return false
}

func (rv *ReviewsView) handleApproveConfirmKey(ev *tcell.EventKey, app *App) bool {
	switch ev.Key() {
	case tcell.KeyEscape:
		rv.focus = rfDiff
		return true
	case tcell.KeyEnter:
		rv.submitApprove(app)
		return true
	case tcell.KeyRune:
		switch ev.Rune() {
		case 'y':
			rv.submitApprove(app)
			return true
		case 'n':
			rv.focus = rfDiff
			return true
		}
	}
	return false
}

func (rv *ReviewsView) cursorUp() {
	if rv.focus == rfDiff {
		if rv.diffScrollOff > 0 {
			rv.diffScrollOff--
		}
		return
	}
	if rv.selectedPR != nil {
		if rv.fileCursor > 0 {
			rv.fileCursor--
			rv.applyFileDiff()
		}
		return
	}
	if rv.prCursor > 0 {
		rv.prCursor--
		if rv.prCursor < rv.prScrollOff {
			rv.prScrollOff = rv.prCursor
		}
	}
}

func (rv *ReviewsView) cursorDown() {
	if rv.focus == rfDiff {
		rv.diffScrollOff++
		return
	}
	if rv.selectedPR != nil {
		if rv.fileCursor < len(rv.files)-1 {
			rv.fileCursor++
			rv.applyFileDiff()
		}
		return
	}
	if rv.prCursor < len(rv.prs)-1 {
		rv.prCursor++
	}
}

func (rv *ReviewsView) handleEnter(app *App) {
	if rv.selectedPR == nil {
		// Select PR.
		if rv.prCursor >= 0 && rv.prCursor < len(rv.prs) {
			pr := rv.prs[rv.prCursor]
			rv.selectedPR = &pr
			rv.fetchFiles(app)
		}
		return
	}
	// Select file → fetch diff.
	if len(rv.files) > 0 && rv.focus == rfList {
		rv.focus = rfDiff
		rv.fetchDiffAndComments(app)
	}
}

func (rv *ReviewsView) handleEsc() {
	if rv.focus == rfDiff {
		rv.focus = rfList
		return
	}
	if rv.selectedPR != nil {
		rv.selectedPR = nil
		rv.files = nil
		rv.fullDiff = ""
		rv.rawDiff = ""
		rv.parsedDiff = nil
		rv.diffRendered = nil
		rv.comments = nil
		rv.focus = rfList
		return
	}
}

func (rv *ReviewsView) openPRInBrowser() {
	var pr *github.PR
	if rv.selectedPR != nil {
		pr = rv.selectedPR
	} else if rv.prCursor >= 0 && rv.prCursor < len(rv.prs) {
		pr = &rv.prs[rv.prCursor]
	}
	if pr == nil {
		return
	}
	url := fmt.Sprintf("https://github.com/%s/%s/pull/%d", pr.RepoOwner, pr.Repo, pr.Number)
	exec.Command("open", url).Start() //nolint:errcheck
	uxlog.Log("[reviews] opened PR #%d in browser: %s", pr.Number, url)
}

func (rv *ReviewsView) handleRefresh(app *App) {
	if !rv.CanFetchPRList() {
		remaining := prListCooldown - time.Since(rv.lastFetchTime)
		uxlog.Log("[reviews] refresh blocked, %v remaining", remaining.Round(time.Second))
		return
	}
	rv.StartLoading()
	rv.fetchPRList(app)
}

// --- Async fetch helpers ---

func (rv *ReviewsView) fetchPRList(app *App) {
	if rv.onFetch == nil {
		return
	}
	rv.onFetch(func() {
		prs, err := github.FetchPRList()
		app.tapp.QueueUpdateDraw(func() {
			rv.SetPRs(prs, err)
		})
	})
}

func (rv *ReviewsView) fetchFiles(app *App) {
	pr := rv.selectedPR
	if pr == nil || rv.onFetch == nil {
		return
	}
	rv.onFetch(func() {
		files, err := github.FetchPRFiles(pr.RepoOwner, pr.Repo, pr.Number)
		app.tapp.QueueUpdateDraw(func() {
			if err != nil {
				uxlog.Log("[reviews] fetch files error: %v", err)
				return
			}
			rv.SetFiles(files)
		})
	})
}

func (rv *ReviewsView) fetchDiffAndComments(app *App) {
	pr := rv.selectedPR
	if pr == nil || rv.onFetch == nil {
		return
	}
	if !rv.diffFetching {
		rv.diffFetching = true
		rv.onFetch(func() {
			diff, err := github.FetchPRFullDiff(pr.RepoOwner, pr.Repo, pr.Number)
			app.tapp.QueueUpdateDraw(func() {
				if err != nil {
					uxlog.Log("[reviews] fetch diff error: %v", err)
					rv.diffFetching = false
					return
				}
				rv.SetFullDiff(diff)
			})
		})
	}
	if !rv.commentsFetching {
		rv.commentsFetching = true
		rv.onFetch(func() {
			comments, err := github.FetchPRComments(pr.RepoOwner, pr.Repo, pr.Number)
			app.tapp.QueueUpdateDraw(func() {
				if err != nil {
					uxlog.Log("[reviews] fetch comments error: %v", err)
					rv.commentsFetching = false
					return
				}
				rv.SetComments(comments)
			})
		})
	}
}

func (rv *ReviewsView) submitComment(app *App) {
	pr := rv.selectedPR
	if pr == nil || rv.onFetch == nil || strings.TrimSpace(rv.draftBody) == "" {
		return
	}
	body := rv.draftBody
	if rv.reviewDraftMode {
		action := github.ReviewRequestChanges
		rv.onFetch(func() {
			err := github.SubmitReview(pr.RepoOwner, pr.Repo, pr.Number, action, body)
			app.tapp.QueueUpdateDraw(func() {
				if err != nil {
					rv.submitErr = err.Error()
					uxlog.Log("[reviews] submit review error: %v", err)
				} else {
					rv.MarkReviewDecision(pr.Number, action)
					uxlog.Log("[reviews] submitted REQUEST_CHANGES for #%d", pr.Number)
				}
				rv.focus = rfDiff
				rv.draftBody = ""
			})
		})
	} else {
		path := rv.draftPath
		line := rv.draftLine
		rv.onFetch(func() {
			err := github.PostReviewComment(pr.RepoOwner, pr.Repo, pr.Number, pr.HeadSHA, path, line, body)
			app.tapp.QueueUpdateDraw(func() {
				if err != nil {
					rv.submitErr = err.Error()
					uxlog.Log("[reviews] post comment error: %v", err)
				} else {
					uxlog.Log("[reviews] posted comment on %s:%d", path, line)
					// Refresh comments.
					rv.commentsFetching = false
					rv.fetchDiffAndComments(app)
				}
				rv.focus = rfDiff
				rv.draftBody = ""
			})
		})
	}
}

func (rv *ReviewsView) submitApprove(app *App) {
	pr := rv.selectedPR
	if pr == nil || rv.onFetch == nil {
		return
	}
	action := github.ReviewApprove
	rv.onFetch(func() {
		err := github.SubmitReview(pr.RepoOwner, pr.Repo, pr.Number, action, "")
		app.tapp.QueueUpdateDraw(func() {
			if err != nil {
				rv.submitErr = err.Error()
				uxlog.Log("[reviews] approve error: %v", err)
			} else {
				rv.MarkReviewDecision(pr.Number, action)
				uxlog.Log("[reviews] approved #%d", pr.Number)
			}
			rv.focus = rfDiff
		})
	})
}

// --- Draw ---

func (rv *ReviewsView) Draw(screen tcell.Screen) {
	rv.Box.DrawForSubclass(screen, rv)
	x, y, width, height := rv.GetInnerRect()
	if width <= 0 || height <= 0 {
		return
	}

	// Three-panel layout: 20% PR list / 60% diff / 20% comments.
	leftW := width * 20 / 100
	if leftW < 20 {
		leftW = min(20, width)
	}
	rightW := width * 20 / 100
	if rightW < 20 {
		rightW = min(20, width-leftW)
	}
	centerW := width - leftW - rightW
	if centerW < 10 {
		centerW = width - leftW
		rightW = 0
	}

	rv.renderPRList(screen, x, y, leftW, height)
	rv.renderDiff(screen, x+leftW, y, centerW, height)
	if rightW > 0 {
		rv.renderComments(screen, x+leftW+centerW, y, rightW, height)
	}
}

// --- Render: PR list ---

func (rv *ReviewsView) renderPRList(screen tcell.Screen, x, y, w, h int) {
	borderStyle := StyleBorder
	if rv.focus == rfList {
		borderStyle = StyleFocusedBorder
	}
	inner := drawBorderedPanel(screen, x, y, w, h, "", borderStyle)
	if inner.W <= 0 || inner.H <= 0 {
		return
	}
	innerX, innerY, innerW, innerH := inner.X, inner.Y, inner.W, inner.H

	if rv.loading && len(rv.prs) == 0 {
		drawText(screen, innerX, innerY, innerW, "Loading PRs...", StyleDimmed)
		return
	}
	if rv.loadErr != "" && len(rv.prs) == 0 {
		drawText(screen, innerX, innerY, innerW, "Error: "+rv.loadErr, tcell.StyleDefault.Foreground(ColorError))
		return
	}
	if len(rv.prs) == 0 {
		drawText(screen, innerX, innerY, innerW, "No open PRs", StyleDimmed)
		return
	}

	// If a PR is selected, show files instead.
	if rv.selectedPR != nil {
		rv.renderFileList(screen, innerX, innerY, innerW, innerH)
		return
	}

	row := 0
	inReviewRequests := true
	drewReviewHeader := false

	for i, pr := range rv.prs {
		if row >= innerH {
			break
		}

		// Section headers.
		if pr.IsReviewRequest && !drewReviewHeader {
			drawText(screen, innerX, innerY+row, innerW, "Review Requests", tcell.StyleDefault.Foreground(ColorTitle).Bold(true))
			row++
			drewReviewHeader = true
		}
		if !pr.IsReviewRequest && inReviewRequests {
			inReviewRequests = false
			if row > 0 && row < innerH {
				row++ // spacer
			}
			if row < innerH {
				drawText(screen, innerX, innerY+row, innerW, "My Open PRs", tcell.StyleDefault.Foreground(ColorTitle).Bold(true))
				row++
			}
		}

		if row >= innerH {
			break
		}

		// PR row.
		style := tcell.StyleDefault
		if i == rv.prCursor {
			style = style.Background(ColorHighlight)
		}

		badge := rv.reviewBadge(pr)
		title := truncString(pr.Title, innerW-len(badge)-2)
		line := fmt.Sprintf("%s %s", badge, title)
		drawText(screen, innerX, innerY+row, innerW, line, style)
		row++

		// Subtitle row.
		if row < innerH {
			sub := fmt.Sprintf("  #%d %s/%s", pr.Number, pr.RepoOwner, pr.Repo)
			if len(sub) > innerW {
				sub = sub[:innerW]
			}
			drawText(screen, innerX, innerY+row, innerW, sub, StyleDimmed)
			row++
		}
	}
}

func (rv *ReviewsView) renderFileList(screen tcell.Screen, x, y, w, h int) {
	pr := rv.selectedPR
	if pr == nil {
		return
	}

	// Header.
	header := fmt.Sprintf("#%d %s", pr.Number, truncString(pr.Title, w-8))
	drawText(screen, x, y, w, header, tcell.StyleDefault.Foreground(ColorTitle).Bold(true))

	if len(rv.files) == 0 {
		drawText(screen, x, y+2, w, "Loading files...", StyleDimmed)
		return
	}

	for i, f := range rv.files {
		row := i + 2
		if row >= h {
			break
		}
		style := tcell.StyleDefault
		if i == rv.fileCursor {
			style = style.Background(ColorHighlight)
		}
		name := f
		if len(name) > w {
			name = "…" + name[len(name)-w+1:]
		}
		drawText(screen, x, y+row, w, name, style)
	}
}

// --- Render: Diff ---

func (rv *ReviewsView) renderDiff(screen tcell.Screen, x, y, w, h int) {
	borderStyle := StyleBorder
	if rv.focus == rfDiff {
		borderStyle = StyleFocusedBorder
	}
	inner := drawBorderedPanel(screen, x, y, w, h, "", borderStyle)
	if inner.W <= 0 || inner.H <= 0 {
		return
	}
	innerX, innerY, innerW, innerH := inner.X, inner.Y, inner.W, inner.H

	if rv.focus == rfApproveConfirm {
		rv.renderApproveConfirm(screen, innerX, innerY, innerW, innerH)
		return
	}

	if rv.parsedDiff == nil || len(rv.diffRendered) == 0 {
		msg := "Select a file to view diff"
		if rv.selectedPR == nil {
			msg = "Select a PR to view diff"
		} else if rv.diffFetching {
			msg = "Loading diff..."
		}
		drawText(screen, innerX+(innerW-len(msg))/2, innerY+innerH/2, innerW, msg, StyleDimmed)
		return
	}

	// Render syntax-highlighted diff lines.
	lines := rv.diffRendered
	maxScroll := len(lines) - innerH
	if maxScroll < 0 {
		maxScroll = 0
	}
	if rv.diffScrollOff > maxScroll {
		rv.diffScrollOff = maxScroll
	}

	for i := range innerH {
		lineIdx := rv.diffScrollOff + i
		if lineIdx >= len(lines) {
			break
		}
		drawStyledLine(screen, innerX, innerY+i, innerW, lines[lineIdx].cells)
	}
}


func (rv *ReviewsView) renderApproveConfirm(screen tcell.Screen, x, y, w, h int) {
	midY := y + h/2 - 1
	title := "Approve this PR?"
	drawText(screen, x+(w-len(title))/2, midY, w, title, tcell.StyleDefault.Bold(true))
	if rv.selectedPR != nil {
		prTitle := truncString(rv.selectedPR.Title, w-4)
		drawText(screen, x+(w-len(prTitle))/2, midY+1, w, prTitle, StyleDimmed)
	}
	hint := "[y] yes  [n] no  [esc] cancel"
	drawText(screen, x+(w-len(hint))/2, midY+3, w, hint, StyleDimmed)
}

// --- Render: Comments ---

func (rv *ReviewsView) renderComments(screen tcell.Screen, x, y, w, h int) {
	borderStyle := StyleBorder
	if rv.focus == rfComment {
		borderStyle = StyleFocusedBorder
	}
	inner := drawBorderedPanel(screen, x, y, w, h, "", borderStyle)
	if inner.W <= 0 || inner.H <= 0 {
		return
	}
	innerX, innerY, innerW, innerH := inner.X, inner.Y, inner.W, inner.H

	// Comment compose mode.
	if rv.focus == rfComment {
		rv.renderCompose(screen, innerX, innerY, innerW, innerH)
		return
	}

	if len(rv.comments) == 0 {
		drawText(screen, innerX, innerY, innerW, "No comments", StyleDimmed)
		if rv.selectedPR != nil {
			drawText(screen, innerX, innerY+2, innerW, "[c] comment", StyleDimmed)
			drawText(screen, innerX, innerY+3, innerW, "[a] approve", StyleDimmed)
			drawText(screen, innerX, innerY+4, innerW, "[r] request changes", StyleDimmed)
		}
		return
	}

	row := 0
	for _, c := range rv.comments {
		if row >= innerH {
			break
		}
		// Author + location.
		loc := c.Author
		if c.Path != "" {
			loc += fmt.Sprintf(" (%s:%d)", truncString(c.Path, 20), c.Line)
		}
		if len(loc) > innerW {
			loc = loc[:innerW]
		}
		drawText(screen, innerX, innerY+row, innerW, loc, tcell.StyleDefault.Foreground(ColorTitle))
		row++

		// Body (truncated to 2 lines).
		bodyLines := strings.SplitN(c.Body, "\n", 3)
		for li := 0; li < 2 && li < len(bodyLines) && row < innerH; li++ {
			text := bodyLines[li]
			if len(text) > innerW {
				text = text[:innerW]
			}
			drawText(screen, innerX, innerY+row, innerW, text, tcell.StyleDefault)
			row++
		}
		row++ // spacer
	}
}

func (rv *ReviewsView) renderCompose(screen tcell.Screen, x, y, w, h int) {
	title := "Comment"
	if rv.reviewDraftMode {
		title = "Request Changes"
	}
	drawText(screen, x, y, w, title, tcell.StyleDefault.Foreground(ColorTitle).Bold(true))

	if !rv.reviewDraftMode {
		loc := fmt.Sprintf("%s:%d", rv.draftPath, rv.draftLine)
		if len(loc) > w {
			loc = loc[:w]
		}
		drawText(screen, x, y+1, w, loc, StyleDimmed)
	}

	// Draft body with cursor.
	bodyY := y + 3
	body := rv.draftBody + "█"
	lines := strings.Split(body, "\n")
	for i, line := range lines {
		if bodyY+i >= y+h-1 {
			break
		}
		if len(line) > w {
			line = line[:w]
		}
		drawText(screen, x, bodyY+i, w, line, tcell.StyleDefault)
	}

	// Hints.
	hint := "[ctrl+s] submit  [esc] cancel"
	if y+h-1 > bodyY {
		drawText(screen, x, y+h-1, w, hint, StyleDimmed)
	}

	if rv.submitErr != "" {
		errMsg := "Error: " + rv.submitErr
		if len(errMsg) > w {
			errMsg = errMsg[:w]
		}
		drawText(screen, x, y+h-2, w, errMsg, tcell.StyleDefault.Foreground(ColorError))
	}
}

// --- Helpers ---

func (rv *ReviewsView) reviewBadge(pr github.PR) string {
	if pr.IsDraft {
		return "[draft]"
	}
	switch pr.ReviewDecision {
	case "APPROVED":
		return "✓"
	case "CHANGES_REQUESTED":
		return "✗"
	case "REVIEW_REQUIRED":
		return "?"
	default:
		return "·"
	}
}

func truncString(s string, maxLen int) string {
	if maxLen <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return string(runes[:maxLen])
	}
	return string(runes[:maxLen-1]) + "…"
}
