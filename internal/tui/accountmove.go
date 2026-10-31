package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/apiclient"
	"github.com/drn/argus/internal/backendkind"
	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/tui/modal"
	"github.com/drn/argus/internal/usagebudget"
	"github.com/drn/argus/internal/uxlog"
)

// remoteAccountMover is satisfied by *apistore.Store: in --remote mode the
// daemon performs the move and the restart.
type remoteAccountMover interface {
	MoveTaskAccount(ctx context.Context, id, account string, force bool) (*apiclient.MoveAccountResp, error)
}

// kickRunner is the runner surface agent.MoveTaskAccount restarts a live
// session through; both the daemon client and the in-process runner have it.
type kickRunner interface {
	Get(taskID string) agent.SessionHandle
	KickRerender(task *model.Task, cfg config.Config, rows, cols uint16) error
	HasPendingRestart(taskID string) bool
}

// openAccountPicker starts the `>` flow for taskID. Everything that can block
// (the task read, which is HTTP in remote mode; the session-status and usage
// reads, which are daemon RPCs) runs off the UI thread; the picker, or a
// notice when the task cannot move, is shown back on it.
func (a *App) openAccountPicker(taskID string) {
	if a.accountPicker != nil || taskID == "" {
		return
	}
	go func() {
		t, err := a.db.Get(taskID)
		if err != nil || t == nil {
			a.tapp.QueueUpdateDraw(func() { a.flashNotice("Move account: task not found") })
			return
		}
		busy := false
		if h := a.runner.Get(taskID); h != nil && h.Alive() && !h.IsIdle() {
			busy = true
		}
		choices, notice := a.accountMoveChoices(t)
		a.tapp.QueueUpdateDraw(func() {
			if notice != "" {
				uxlog.Log("[account] picker task=%s not shown: %s", t.ID, notice)
				a.flashNotice(notice)
				return
			}
			a.showAccountPicker(t, choices, busy)
		})
	}()
}

// accountMoveChoices lists "auto" plus every other account that can run the
// task, labelled with usage. A non-empty notice means the task cannot move.
func (a *App) accountMoveChoices(t *model.Task) ([]modal.AccountChoice, string) {
	cfg := a.db.Config()
	current := agent.NormalizeAccount(t.Account)
	if backend, err := agent.ResolveBackend(t, cfg); err == nil && backendkind.Of(backend.Command) != backendkind.Claude {
		return nil, "Only Claude tasks can move accounts"
	}
	if _, local := a.db.(*db.DB); !local {
		if rl, ok := a.db.(remoteAccountLister); ok {
			return remoteAccountMoveChoices(rl, current)
		}
	}
	names := cfg.AccountNamesFor("claude")
	usage := agent.AccountUsage(cfg)
	now := time.Now()
	var others []modal.AccountChoice
	for _, n := range names {
		if n == current {
			continue
		}
		five, week := usagebudget.LiveWindows(usage[n], now)
		others = append(others, modal.AccountChoice{Name: n, Detail: usageDetail(windowPct(five), windowPct(week))})
	}
	if len(others) == 0 {
		return nil, "No other account can run this task"
	}
	auto := modal.AccountChoice{Name: agent.AutoMoveTarget, Detail: autoDetail}
	if picked, _, ok := agent.PickMoveTarget(cfg, usage, "claude", current); ok {
		auto.Detail = autoDetail + " (now " + picked + ")"
	}
	return append([]modal.AccountChoice{auto}, others...), ""
}

// remoteAccountMoveChoices builds the choices from the remote daemon's
// GET /api/accounts, which carries the usage readings.
func remoteAccountMoveChoices(rl remoteAccountLister, current string) ([]modal.AccountChoice, string) {
	list, err := rl.Accounts(context.Background())
	if err != nil {
		return nil, "Move account: could not list accounts: " + err.Error()
	}
	var others []modal.AccountChoice
	for _, ac := range list {
		if !ac.Supports.Claude || ac.Name == current {
			continue
		}
		detail := usageDetail(nil, nil)
		if u := ac.Usage; u != nil {
			detail = usageDetail(u.FiveHourPct, u.WeeklyPct)
		}
		others = append(others, modal.AccountChoice{Name: ac.Name, Detail: detail})
	}
	if len(others) == 0 {
		return nil, "No other account can run this task"
	}
	return append([]modal.AccountChoice{{Name: agent.AutoMoveTarget, Detail: autoDetail}}, others...), ""
}

// autoDetail describes the "auto" choice in the picker.
const autoDetail = "lowest usage"

func windowPct(w *usagebudget.Window) *float64 {
	if w == nil {
		return nil
	}
	p := w.Percentage
	return &p
}

// usageDetail renders "5h 100% · wk 75%", with "—" for an unknown window.
func usageDetail(fiveHour, weekly *float64) string {
	f := func(p *float64) string {
		if p == nil {
			return "—"
		}
		return fmt.Sprintf("%.0f%%", *p)
	}
	return "5h " + f(fiveHour) + " · wk " + f(weekly)
}

func (a *App) showAccountPicker(t *model.Task, choices []modal.AccountChoice, busy bool) {
	if a.accountPicker != nil {
		return
	}
	warning := ""
	if busy {
		warning = "The agent is working; moving will interrupt it."
	}
	a.accountPickerPrevPage, _ = a.pages.GetFrontPage()
	a.accountPickerTask = t
	a.accountPickerForce = busy
	a.accountPicker = modal.NewAccountPickerModal(fmt.Sprintf("Move %s (on %s) to account", t.Name, agent.NormalizeAccount(t.Account)), warning, choices)
	a.mode = modeAccountPicker
	a.pages.AddPage("accountpicker", a.accountPicker, true, true)
	a.pages.SwitchToPage("accountpicker")
	a.tapp.SetFocus(a.accountPicker)
	uxlog.Log("[account] picker open task=%s choices=%d busy=%t", t.ID, len(choices), busy)
}

func (a *App) handleAccountPickerKey(event *tcell.EventKey) {
	a.accountPicker.InputHandler()(event, func(tview.Primitive) {})
	if a.accountPicker.Canceled() {
		uxlog.Log("[account] picker canceled task=%s", a.accountPickerTask.ID)
		a.closeAccountPicker()
		return
	}
	if target := a.accountPicker.Selected(); target != "" {
		t, force := a.accountPickerTask, a.accountPickerForce
		a.closeAccountPicker()
		a.runAccountMove(t, target, force)
	}
}

func (a *App) closeAccountPicker() {
	a.mode = modeTaskList
	a.accountPicker = nil
	a.accountPickerTask = nil
	a.accountPickerForce = false
	a.pages.RemovePage("accountpicker")
	prev := a.accountPickerPrevPage
	a.accountPickerPrevPage = ""
	if prev == "accountpicker" {
		prev = ""
	}
	a.restorePageFocus(prev)
}

// runAccountMove performs the move off the UI thread. Locally a live session
// restarts in place through the runner; an exited one is started here through
// startSession. Remotely the daemon does both.
func (a *App) runAccountMove(t *model.Task, target string, force bool) {
	a.statusbar.SetInfo(fmt.Sprintf("Moving %s to %s…", t.Name, target))
	uxlog.Log("[account] move requested task=%s target=%s force=%t", t.ID, target, force)
	if _, local := a.db.(*db.DB); !local {
		rm, ok := a.db.(remoteAccountMover)
		if !ok {
			a.statusbar.SetError("Move account is not available in this mode")
			return
		}
		go func() {
			resp, err := rm.MoveTaskAccount(context.Background(), t.ID, target, force)
			a.tapp.QueueUpdateDraw(func() {
				if err != nil {
					a.accountMoveFailed(t, err)
					return
				}
				a.accountMoveDone(t, agent.AccountMoveResult{From: resp.From, To: resp.To, Resumed: resp.Resumed, Started: resp.Started})
			})
		}()
		return
	}
	d := a.db.(*db.DB)
	rows, cols := a.spawnPTYSize(t.ID)
	// A runner without in-place restart passes nil: the move then reports
	// Started=false and the session is started below.
	runner, _ := a.runner.(kickRunner)
	go func() {
		_, res, err := agent.MoveTaskAccount(d, runner, agent.AccountMoveInput{TaskID: t.ID, Account: target, Force: force, Rows: rows, Cols: cols})
		a.tapp.QueueUpdateDraw(func() {
			a.invalidateTasksChangeGate()
			if err != nil {
				a.accountMoveFailed(t, err)
				return
			}
			if !res.Started {
				fresh, gerr := a.db.Get(t.ID)
				if gerr != nil || fresh == nil {
					a.accountMoveFailed(t, fmt.Errorf("reload task: %w", gerr))
					return
				}
				a.startSession(fresh)
			}
			a.accountMoveDone(t, res)
		})
	}()
}

func (a *App) accountMoveFailed(t *model.Task, err error) {
	uxlog.Log("[account] move failed task=%s: %v", t.ID, err)
	msg := err.Error()
	if i := strings.Index(msg, ": "); strings.HasPrefix(msg, agent.ErrAccount.Error()+": ") && i >= 0 {
		msg = msg[i+2:]
	}
	a.statusbar.SetError("Move account failed: " + msg)
}

func (a *App) accountMoveDone(t *model.Task, res agent.AccountMoveResult) {
	note := fmt.Sprintf("Moved %s: %s → %s", t.Name, res.From, res.To)
	if !res.Resumed {
		note += " (no transcript; started fresh)"
	}
	uxlog.Log("[account] move done task=%s %s→%s resumed=%t", t.ID, res.From, res.To, res.Resumed)
	a.statusbar.ClearInfo()
	a.flashNotice(note)
	a.refreshTasks()
}
