package tui

import (
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/tui/hera"
	"github.com/drn/argus/internal/tui/keymap"
	"github.com/drn/argus/internal/tui/widget"
	"github.com/drn/argus/internal/uxlog"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// resolveRepoDir resolves the directory `ctrl+o`/the palette's "open repo"
// action should open for a task: the configured project path, falling back
// to the task's worktree. Extracted so the ActGlobalOpenRepo switch case and
// the command palette's registry entry share one implementation.
func (a *App) resolveRepoDir(t *model.Task) string {
	if p, ok := a.db.Config().Projects[t.Project]; ok && p.Path != "" {
		return p.Path
	}
	return t.Worktree
}

// rowsForContext builds palette rows for a context in contextOrder, keeping
// only actions present in registry (this is where "applicable to the current
// context" is enforced — an action absent from the registry simply isn't
// offered, no special-casing needed) and resolving each row's display key via
// BindingFor (config-override-aware, so a rebind is reflected automatically).
func rowsForContext(km *keymap.Keymap, ctx keymap.Context, registry map[keymap.Action]func()) []paletteRow {
	var rows []paletteRow
	for _, act := range keymap.ContextOrder(ctx) {
		fn, ok := registry[act]
		if !ok {
			continue
		}
		b, ok := km.BindingFor(ctx, act)
		if !ok {
			continue
		}
		rows = append(rows, paletteRow{Label: km.ActionLabel(act), Key: b.String(), invoke: fn})
	}
	return rows
}

// globalActionRegistry mirrors handleGlobalKey's gated CtxGlobal switch — each
// entry is the SAME guard + call the physical key's case already runs
// (ActGlobalOpenRepo shares resolveRepoDir with the switch so the directory
// logic isn't duplicated). ActGlobalPalette is deliberately absent: the
// palette doesn't list "open the command palette" as one of its own rows.
func (a *App) globalActionRegistry() map[keymap.Action]func() {
	return map[keymap.Action]func(){
		keymap.ActGlobalQuit: func() {
			if a.mode == modeTaskList {
				a.tapp.Stop()
			}
		},
		keymap.ActGlobalTabTasks: func() {
			if a.mode != modeAgent {
				a.switchTab(widget.TabTasks)
			}
		},
		keymap.ActGlobalTabHera: func() {
			if a.mode != modeAgent {
				a.switchTab(widget.TabHera)
			}
		},
		keymap.ActGlobalTabSettings: func() {
			if a.mode != modeAgent {
				a.switchTab(widget.TabSettings)
			}
		},
		keymap.ActGlobalHelp: func() {
			if a.mode != modeAgent {
				a.openHelp()
			}
		},
		keymap.ActGlobalRefresh: func() {
			if a.mode != modeAgent && !a.heraPaneFocused() {
				a.screen.Sync()
			}
		},
		keymap.ActGlobalDestroy: func() {
			if a.mode == modeTaskList && a.header.ActiveTab() == widget.TabTasks {
				if t := a.tasklist.SelectedTask(); t != nil {
					a.openConfirmDelete(t)
				}
			}
		},
		keymap.ActGlobalFork: func() {
			if a.mode == modeTaskList && a.header.ActiveTab() == widget.TabTasks {
				if t := a.tasklist.SelectedTask(); t != nil && t.Worktree != "" {
					a.openForkModal(t)
				}
			}
		},
		keymap.ActGlobalOpenRepo: func() {
			if a.mode == modeTaskList && a.header.ActiveTab() == widget.TabTasks {
				if t := a.tasklist.SelectedTask(); t != nil {
					if dir := a.resolveRepoDir(t); dir != "" {
						if err := repoOpener(dir); err != nil {
							uxlog.Log("[tui] open repo failed: %v", err)
						}
					}
				}
			}
		},
		keymap.ActGlobalOpenPR: func() {
			if a.mode == modeTaskList && a.header.ActiveTab() == widget.TabTasks {
				if t := a.tasklist.SelectedTask(); t != nil && t.Worktree != "" {
					if err := prOpener(t.Worktree); err != nil {
						uxlog.Log("[tui] open PR failed: %v", err)
					}
				}
			}
		},
		keymap.ActGlobalPrune: func() {
			if a.mode == modeTaskList && a.header.ActiveTab() == widget.TabTasks {
				a.openConfirmPrune()
			}
		},
	}
}

// agentActionRegistry mirrors handleAgentKey's CtxAgent interception block.
// Scroll actions (Up/Down/PgUp/PgDn/End) are deliberately excluded — they are
// position-sensitive, repeat-oriented navigation rather than a discrete,
// one-shot "run this" action, so they are not palette-worthy (mirrors why
// CtxTaskList's/CtxSettings' pure cursor-nav actions are excluded below).
func (a *App) agentActionRegistry() map[keymap.Action]func() {
	return map[keymap.Action]func(){
		keymap.ActAgentLinks:    func() { a.openAgentLinks() },
		keymap.ActAgentSession:  func() { a.openSessionPicker() },
		keymap.ActAgentSwitcher: func() { a.openTaskSwitcher() },
		keymap.ActAgentOpenPR:   func() { a.openPR() },
		keymap.ActAgentZoom:     func() { a.toggleAgentZen() },
		keymap.ActAgentCopy: func() {
			if !a.copyStagedClipboard() {
				a.flashNotice("Nothing to copy")
			}
		},
		keymap.ActAgentPaneLeft: func() {
			if !a.agentZen && a.agentFocus > focusTerminal {
				a.agentFocus--
				a.updateFocusIndicators()
			}
		},
		keymap.ActAgentPaneRight: func() {
			if !a.agentZen && a.agentFocus < focusFiles {
				a.agentFocus++
				a.updateFocusIndicators()
			}
		},
		keymap.ActAgentTaskPrev: func() { a.navigateAgentTask(-1) },
		keymap.ActAgentTaskNext: func() { a.navigateAgentTask(1) },
	}
}

// heraRailActionRegistry builds registry entries straight from HeraPage's
// already-wired OnXxx callback fields (nil-safe: unwired in remote mode,
// exactly the same safety net handleRailMutation's `fire` gives a physical
// keypress) — acting on the rail's CURRENT selection, the same target
// resolution the existing BUG-010 in-details-mode rail-mutation routing uses.
func (a *App) heraRailActionRegistry() map[keymap.Action]func() {
	if a.heraPage == nil {
		return nil
	}
	sel := a.heraPage.SelectionContext()
	reg := map[keymap.Action]func(){}
	add := func(act keymap.Action, cb func(hera.Selection)) {
		if cb != nil {
			reg[act] = func() { cb(sel) }
		}
	}
	add(keymap.ActHeraSpawn, a.heraPage.OnSpawnWorker)
	add(keymap.ActHeraRename, a.heraPage.OnRename)
	add(keymap.ActHeraArchive, a.heraPage.OnArchiveToggle)
	add(keymap.ActHeraPin, a.heraPage.OnPinToggle)
	add(keymap.ActHeraStatAdv, a.heraPage.OnStatusAdvance)
	add(keymap.ActHeraStatRev, a.heraPage.OnStatusRevert)
	add(keymap.ActHeraDelete, a.heraPage.OnDelete)
	add(keymap.ActHeraAdopt, a.heraPage.OnAdopt)
	add(keymap.ActHeraNewCoord, a.heraPage.OnNewCoordinator)
	add(keymap.ActHeraClear, a.heraPage.OnClearArchive)
	return reg
}

// taskListActionRegistry reuses TaskListView's OWN InputHandler dispatch
// verbatim by synthesizing a tcell.EventKey for each action's bound key — the
// case bodies live in the taskview package as a closed-over switch, not
// separate named methods, so replaying the exact same key through the exact
// same widget is the zero-duplication option (rather than hand-extracting a
// second copy of each case here). ActTaskDown/ActTaskUp (pure cursor nav) are
// excluded, matching agentActionRegistry's scroll-action exclusion. Returns
// nil while the task list is in `/` filter INPUT mode — every key there is
// filter text, so a synthesized action key would silently corrupt the query
// instead of firing the action.
func (a *App) taskListActionRegistry() map[keymap.Action]func() {
	if a.tasklist == nil || a.tasklist.Filtering() {
		return nil
	}
	km := a.activeKeymap()
	reg := map[keymap.Action]func(){}
	for _, act := range keymap.ContextOrder(keymap.CtxTaskList) {
		if act == keymap.ActTaskDown || act == keymap.ActTaskUp {
			continue
		}
		b, ok := km.BindingFor(keymap.CtxTaskList, act)
		if !ok {
			continue
		}
		binding := b
		reg[act] = func() {
			ev := tcell.NewEventKey(binding.Key, binding.Rune, binding.Mods)
			a.tasklist.InputHandler()(ev, func(tview.Primitive) {})
		}
	}
	return reg
}

// settingsActionRegistry mirrors taskListActionRegistry's synthetic-event
// approach for SettingsView.HandleKey. ActSettingsDown/Up are excluded for
// the same pure-cursor-nav reason. Returns nil while a settings field is
// being edited (IsEditing), matching the existing suppressRune guard.
func (a *App) settingsActionRegistry() map[keymap.Action]func() {
	if a.settings == nil || a.settings.IsEditing() {
		return nil
	}
	km := a.activeKeymap()
	reg := map[keymap.Action]func(){}
	for _, act := range keymap.ContextOrder(keymap.CtxSettings) {
		if act == keymap.ActSettingsDown || act == keymap.ActSettingsUp {
			continue
		}
		b, ok := km.BindingFor(keymap.CtxSettings, act)
		if !ok {
			continue
		}
		binding := b
		reg[act] = func() {
			ev := tcell.NewEventKey(binding.Key, binding.Rune, binding.Mods)
			a.settings.HandleKey(ev)
		}
	}
	return reg
}

// heraLiteralRows returns the two enumerated, non-keymap Hera pane-focus
// literal actions (fullscreen toggle; clipboard copy) as fixed palette rows,
// per design.md Decision 2 / the resolved Open Question 1. Fullscreen applies
// to any Hera pane focus (mirrors Ctrl+Z's own "always intercepted, even in
// the Details/plan region" reach); copy only applies to a genuine terminal
// pane (FocusedTerminalTaskID is empty in the coordinator Details/plan
// region, which has nothing to copy).
func (a *App) heraLiteralRows() []paletteRow {
	if a.heraPage == nil || a.heraPage.Machine().State() == hera.FocusRail {
		return nil
	}
	rows := []paletteRow{
		{Label: "toggle fullscreen", Key: "ctrl+z", invoke: func() { a.heraPage.Machine().ToggleFullscreen() }},
	}
	if taskID := a.heraPage.FocusedTerminalTaskID(); taskID != "" {
		rows = append(rows, paletteRow{
			Label: "copy staged clipboard",
			Key:   "ctrl+y",
			invoke: func() {
				if a.heraPage.OnCopyClipboard != nil {
					a.heraPage.OnCopyClipboard(taskID)
				}
			},
		})
	}
	return rows
}

// paletteApplicableActions builds the palette's row list for whatever
// mode/focus region is active — the RESOLVED applicable-action hierarchy
// (design.md Decision 2, confirmed by Aaron 2026-07-18): the focused
// element's own applicable actions, UNION the current tab's own rail/list
// action set (never a DIFFERENT tab's), UNION the global action set —
// uniformly, since a deliberate palette pick isn't the accidental-typing risk
// heraPaneFocused()/BUG-001 guards against. The one exception preserving its
// pre-existing boundary is the classic fullscreen agent view, where the
// global action set stays gated off exactly as it already is for ordinary
// keypresses (CtxAgent only) — that boundary predates this change.
func (a *App) paletteApplicableActions() []paletteRow {
	km := a.activeKeymap()
	switch {
	case a.mode == modeAgent:
		return rowsForContext(km, keymap.CtxAgent, a.agentActionRegistry())
	case a.mode == modeTaskList && a.header.ActiveTab() == widget.TabHera && a.heraPage != nil && !a.heraPage.IsRemote():
		rows := a.heraLiteralRows()
		rows = append(rows, rowsForContext(km, keymap.CtxHeraRail, a.heraRailActionRegistry())...)
		return append(rowsForContext(km, keymap.CtxGlobal, a.globalActionRegistry()), rows...)
	case a.mode == modeTaskList && a.header.ActiveTab() == widget.TabSettings:
		rows := rowsForContext(km, keymap.CtxSettings, a.settingsActionRegistry())
		return append(rowsForContext(km, keymap.CtxGlobal, a.globalActionRegistry()), rows...)
	case a.mode == modeTaskList:
		rows := rowsForContext(km, keymap.CtxTaskList, a.taskListActionRegistry())
		return append(rowsForContext(km, keymap.CtxGlobal, a.globalActionRegistry()), rows...)
	default:
		return nil
	}
}

// openCommandPalette builds the applicable-action row list for whatever
// mode/focus is currently active and opens the palette modal. Dispatched
// unconditionally from handleGlobalKey's ActGlobalPalette case.
func (a *App) openCommandPalette() {
	rows := a.paletteApplicableActions()
	a.commandPaletteModal = NewCommandPaletteModal(rows)
	a.commandPaletteReturnMode = a.mode
	a.mode = modeCommandPalette
	a.pages.AddPage("commandpalette", a.commandPaletteModal, true, true)
	a.tapp.SetFocus(a.commandPaletteModal)
	uxlog.Log("[tui] command palette: %d applicable action(s)", len(rows))
}

// handleCommandPaletteKey processes keys in the command palette modal.
func (a *App) handleCommandPaletteKey(event *tcell.EventKey) {
	handler := a.commandPaletteModal.InputHandler()
	handler(event, func(tview.Primitive) {})

	if a.commandPaletteModal.Canceled() {
		a.closeCommandPaletteModal()
		return
	}
	if a.commandPaletteModal.Selected() {
		m := a.commandPaletteModal
		a.closeCommandPaletteModal()
		// Invoke AFTER closing/refocusing: several registry entries (e.g. Hera
		// rail mutations, new-task modals) open their OWN modal/mode, which
		// must land cleanly rather than fight the palette's own teardown.
		m.Invoke()
	}
}

// closeCommandPaletteModal closes the palette and restores whichever
// mode/focus was active when it opened.
func (a *App) closeCommandPaletteModal() {
	a.mode = a.commandPaletteReturnMode
	a.commandPaletteModal = nil
	a.pages.RemovePage("commandpalette")
	switch {
	case a.mode == modeAgent:
		a.tapp.SetFocus(a.agentPane)
	case a.header.ActiveTab() == widget.TabHera:
		a.tapp.SetFocus(a.heraPage)
	case a.header.ActiveTab() == widget.TabSettings:
		a.tapp.SetFocus(a.settingsPage)
	default:
		a.tapp.SetFocus(a.tasklist)
	}
}
