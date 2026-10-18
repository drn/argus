package terminal

import (
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/drn/argus/internal/app/agentview"
	"github.com/drn/argus/internal/tui/theme"
	"github.com/drn/argus/internal/tui/widget"
	"github.com/drn/argus/internal/uxlog"
)

// launchSplashTimeout caps how long one bound session may keep the launch
// splash up. A launching agent normally emits its first byte within seconds;
// the cap keeps a genuinely silent (or ring-less re-attached) session from
// hiding its pane forever.
const launchSplashTimeout = 45 * time.Second

// launchNow is the clock the splash cap reads; tests swap it.
var launchNow = time.Now

// EnableLaunchSplash opts this pane into the derived launch splash (the Tasks
// view's LAUNCH banner, driven by task/session state instead of SetPending's
// in-process callback). Used by the Hera panes, whose launches are usually
// triggered by MCP tools in the daemon rather than by this TUI.
func (tp *TerminalPane) EnableLaunchSplash(v bool) {
	tp.mu.Lock()
	tp.launchSplash = v
	tp.mu.Unlock()
}

// SetLaunchTaskStatus records the bound task's workflow status ("pending",
// "in_progress", …) so Draw can tell a task that has not started yet from one
// whose session simply has no session.
func (tp *TerminalPane) SetLaunchTaskStatus(status string) {
	tp.mu.Lock()
	tp.launchTaskStatus = status
	tp.mu.Unlock()
}

// resetLaunchSplashLocked forgets the cap clock and last logged stage. Called
// whenever the bound session changes. Caller holds tp.mu.
func (tp *TerminalPane) resetLaunchSplashLocked() {
	tp.launchSince = time.Time{}
	tp.launchStage = ""
}

// launchSubtitle returns the splash status line for the current Draw, or ""
// when the pane should render normally. Precedence: closed-out banner (decided
// by the caller, earlier) > "Re-rendering…" (a live session being repaired is
// never a launch) > launch splash > everything else. Splash shows only while
// nothing has ever been written: a pending task with no session, or a live
// session whose ring is still empty.
func (tp *TerminalPane) launchSubtitle(sess agentview.TerminalAdapter) string {
	tp.mu.Lock()
	defer tp.mu.Unlock()
	if !tp.launchSplash || tp.rerendering || tp.scrollOffset > 0 {
		return ""
	}
	stage := ""
	switch {
	case sess == nil && tp.launchTaskStatus == "pending":
		stage = widget.LaunchSubtitlePreparing
	case sess != nil && sess.Alive() && sess.TotalWritten() == 0:
		stage = widget.LaunchSubtitleStarting
	}
	if stage == "" {
		if tp.launchStage != "" {
			uxlog.Log("[terminalpane] launch splash end task=%s", tp.taskID)
			tp.launchStage = ""
		}
		return ""
	}
	now := launchNow()
	if tp.launchSince.IsZero() {
		tp.launchSince = now
	}
	if now.Sub(tp.launchSince) > launchSplashTimeout {
		if tp.launchStage != "timeout" {
			uxlog.Log("[terminalpane] launch splash timeout task=%s", tp.taskID)
			tp.launchStage = "timeout"
		}
		return ""
	}
	if tp.launchStage != stage {
		uxlog.Log("[terminalpane] launch splash task=%s stage=%q", tp.taskID, stage)
		tp.launchStage = stage
	}
	return stage
}

// drawLaunchSplash paints the shared LAUNCH banner with the given status line,
// strictly inside the rect. A rect too short for the full banner falls back to
// just the centered status line.
func drawLaunchSplash(screen tcell.Screen, x, y, width, height int, subtitle string) {
	widget.FillArea(screen, x, y, width, height, ' ', tcell.StyleDefault)
	bannerH := widget.PendingBannerHeight()
	if height >= bannerH {
		widget.DrawLaunchBanner(screen, x, y+(height-bannerH)/2, width, subtitle)
		return
	}
	pad := max((width-len(subtitle))/2, 0)
	widget.DrawText(screen, x+pad, y+height/2, width-pad, subtitle, tcell.StyleDefault.Foreground(theme.ColorDimmed))
}
