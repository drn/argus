package keymap

// Context scopes a binding table. The same physical key can map to different
// actions in different contexts, so every Resolve call names its context.
type Context string

const (
	CtxGlobal   Context = "global"
	CtxTaskList Context = "tasklist"
	CtxAgent    Context = "agent"
	CtxFilePnl  Context = "filepanel"
	CtxDiff     Context = "diff"
	CtxSettings Context = "settings"
	CtxHeraRail Context = "hera_rail"
)

// AllContexts is the ordered set of contexts (drives validation + help order).
var AllContexts = []Context{
	CtxGlobal, CtxTaskList, CtxAgent, CtxFilePnl, CtxDiff, CtxSettings, CtxHeraRail,
}

// Action is a stable, context-prefixed identifier for a rebindable command.
// The id after the "<context>." prefix is the key used in the config.toml table.
type Action string

const (
	// Global
	ActGlobalQuit        Action = "global.quit"
	ActGlobalHelp        Action = "global.help"
	ActGlobalTabTasks    Action = "global.tab_tasks"
	ActGlobalTabHera     Action = "global.tab_hera"
	ActGlobalTabSettings Action = "global.tab_settings"
	ActGlobalRefresh     Action = "global.refresh"
	ActGlobalDestroy     Action = "global.destroy"
	ActGlobalFork        Action = "global.fork"
	ActGlobalOpenRepo    Action = "global.open_repo"
	ActGlobalOpenPR      Action = "global.open_pr"
	ActGlobalPrune       Action = "global.prune"

	// Task list
	ActTaskNew       Action = "tasklist.new"
	ActTaskStatusAdv Action = "tasklist.status_advance"
	ActTaskStatusRev Action = "tasklist.status_revert"
	ActTaskArchive   Action = "tasklist.archive"
	ActTaskPin       Action = "tasklist.pin"
	ActTaskRename    Action = "tasklist.rename"
	ActTaskCopy      Action = "tasklist.copy"
	ActTaskFilter    Action = "tasklist.filter"
	ActTaskHera      Action = "tasklist.toggle_hera"
	ActTaskDown      Action = "tasklist.nav_down"
	ActTaskUp        Action = "tasklist.nav_up"

	// Agent view (all bindings must carry a modifier)
	ActAgentLinks      Action = "agent.links"
	ActAgentSession    Action = "agent.session"
	ActAgentSwitcher   Action = "agent.switcher"
	ActAgentOpenPR     Action = "agent.open_pr"
	ActAgentZoom       Action = "agent.zoom"
	ActAgentCopy       Action = "agent.copy"
	ActAgentPaneLeft   Action = "agent.pane_left"
	ActAgentPaneRight  Action = "agent.pane_right"
	ActAgentTaskPrev   Action = "agent.task_prev"
	ActAgentTaskNext   Action = "agent.task_next"
	ActAgentScrollUp   Action = "agent.scroll_up"
	ActAgentScrollDown Action = "agent.scroll_down"
	ActAgentScrollPgUp Action = "agent.scroll_pgup"
	ActAgentScrollPgDn Action = "agent.scroll_pgdn"
	ActAgentScrollEnd  Action = "agent.scroll_end"

	// File panel
	ActFileDown     Action = "filepanel.nav_down"
	ActFileUp       Action = "filepanel.nav_up"
	ActFileFinder   Action = "filepanel.finder"
	ActFileOpen     Action = "filepanel.open"
	ActFileEditor   Action = "filepanel.editor"
	ActFileTerminal Action = "filepanel.terminal"

	// Diff view
	ActDiffSplit      Action = "diff.split"
	ActDiffScrollDown Action = "diff.scroll_down"
	ActDiffScrollUp   Action = "diff.scroll_up"

	// Settings (letter commands; arrows/h/l/enter are structural)
	ActSettingsDown     Action = "settings.nav_down"
	ActSettingsUp       Action = "settings.nav_up"
	ActSettingsDelete   Action = "settings.delete"
	ActSettingsNew      Action = "settings.new"
	ActSettingsEdit     Action = "settings.edit"
	ActSettingsQuickAdd Action = "settings.quick_add"
	ActSettingsApple    Action = "settings.apple_events"
	ActSettingsToggle   Action = "settings.toggle_schedule"
	ActSettingsRun      Action = "settings.run_schedule"
	ActSettingsModel    Action = "settings.edit_model"

	// Hera rail mutations (Enter + nav are structural)
	ActHeraDelete   Action = "hera_rail.delete"
	ActHeraSpawn    Action = "hera_rail.spawn_worker"
	ActHeraRename   Action = "hera_rail.rename"
	ActHeraArchive  Action = "hera_rail.archive"
	ActHeraPin      Action = "hera_rail.pin"
	ActHeraStatAdv  Action = "hera_rail.status_advance"
	ActHeraStatRev  Action = "hera_rail.status_revert"
	ActHeraAdopt    Action = "hera_rail.adopt"
	ActHeraNewCoord Action = "hera_rail.new_coordinator"
	ActHeraClear    Action = "hera_rail.clear_archive"
)

// defaultSpecs is THE source of truth for argus's built-in bindings, mirroring
// the historical hardcoded literals. config.Keybindings carries overrides only.
var defaultSpecs = map[Context]map[Action]string{
	CtxGlobal: {
		ActGlobalQuit: "q", ActGlobalHelp: "?",
		ActGlobalTabTasks: "1", ActGlobalTabHera: "2", ActGlobalTabSettings: "3",
		ActGlobalRefresh: "ctrl+l", ActGlobalDestroy: "ctrl+d", ActGlobalFork: "ctrl+f",
		ActGlobalOpenRepo: "ctrl+o", ActGlobalOpenPR: "ctrl+p", ActGlobalPrune: "ctrl+r",
	},
	CtxTaskList: {
		ActTaskNew: "n", ActTaskStatusAdv: "s", ActTaskStatusRev: "S",
		ActTaskArchive: "a", ActTaskPin: "P", ActTaskRename: "r", ActTaskCopy: "c",
		ActTaskFilter: "/", ActTaskHera: "H", ActTaskDown: "j", ActTaskUp: "k",
	},
	CtxAgent: {
		ActAgentLinks: "ctrl+l", ActAgentSession: "ctrl+r", ActAgentSwitcher: "ctrl+k",
		ActAgentOpenPR: "ctrl+p", ActAgentZoom: "ctrl+z", ActAgentCopy: "ctrl+y",
		ActAgentPaneLeft: "cmd+left", ActAgentPaneRight: "cmd+right",
		ActAgentTaskPrev: "cmd+up", ActAgentTaskNext: "cmd+down",
		ActAgentScrollUp: "shift+up", ActAgentScrollDown: "shift+down",
		ActAgentScrollPgUp: "shift+pgup", ActAgentScrollPgDn: "shift+pgdn",
		ActAgentScrollEnd: "shift+end",
	},
	CtxFilePnl: {
		ActFileDown: "j", ActFileUp: "k", ActFileFinder: "f",
		ActFileOpen: "o", ActFileEditor: "e", ActFileTerminal: "t",
	},
	CtxDiff: {
		ActDiffSplit: "s", ActDiffScrollDown: "j", ActDiffScrollUp: "k",
	},
	CtxSettings: {
		ActSettingsDown: "j", ActSettingsUp: "k", ActSettingsDelete: "d",
		ActSettingsNew: "n", ActSettingsEdit: "e", ActSettingsQuickAdd: "i",
		ActSettingsApple: "a", ActSettingsToggle: "t", ActSettingsRun: "r",
		ActSettingsModel: "m",
	},
	CtxHeraRail: {
		ActHeraDelete: "ctrl+d", ActHeraSpawn: "w", ActHeraRename: "r",
		ActHeraArchive: "a", ActHeraPin: "P", ActHeraStatAdv: "s", ActHeraStatRev: "S",
		ActHeraAdopt: "J", ActHeraNewCoord: "n", ActHeraClear: "C",
	},
}

// actionLabels are the human-readable descriptions used in the help overlay.
var actionLabels = map[Action]string{
	ActGlobalQuit: "quit", ActGlobalHelp: "help", ActGlobalTabTasks: "switch to Tasks tab",
	ActGlobalTabHera: "switch to Hera tab", ActGlobalTabSettings: "switch to Settings tab",
	ActGlobalRefresh: "refresh screen", ActGlobalDestroy: "destroy task", ActGlobalFork: "fork task",
	ActGlobalOpenRepo: "open repo", ActGlobalOpenPR: "open PR", ActGlobalPrune: "prune completed",

	ActTaskNew: "new task", ActTaskStatusAdv: "advance status", ActTaskStatusRev: "revert status",
	ActTaskArchive: "toggle archive", ActTaskPin: "toggle pin", ActTaskRename: "rename",
	ActTaskCopy: "copy name / prompt", ActTaskFilter: "filter", ActTaskHera: "show/hide hera-managed (workers+coords)",
	ActTaskDown: "navigate down", ActTaskUp: "navigate up",

	ActAgentLinks: "link picker", ActAgentSession: "switch Claude session", ActAgentSwitcher: "task switcher",
	ActAgentOpenPR: "open PR", ActAgentZoom: "toggle single-pane (zoom)", ActAgentCopy: "copy staged text",
	ActAgentPaneLeft: "focus pane left", ActAgentPaneRight: "focus pane right",
	ActAgentTaskPrev: "previous task", ActAgentTaskNext: "next task",
	ActAgentScrollUp: "scroll up", ActAgentScrollDown: "scroll down",
	ActAgentScrollPgUp: "scroll page up", ActAgentScrollPgDn: "scroll page down", ActAgentScrollEnd: "scroll to bottom",

	ActFileDown: "navigate down", ActFileUp: "navigate up", ActFileFinder: "reveal in Finder",
	ActFileOpen: "open file", ActFileEditor: "open in editor", ActFileTerminal: "open terminal",

	ActDiffSplit: "toggle split/unified", ActDiffScrollDown: "scroll down", ActDiffScrollUp: "scroll up",

	ActSettingsDown: "navigate down", ActSettingsUp: "navigate up", ActSettingsDelete: "delete / set default",
	ActSettingsNew: "new", ActSettingsEdit: "edit", ActSettingsQuickAdd: "quick add projects",
	ActSettingsApple: "AppleEvents", ActSettingsToggle: "toggle schedule", ActSettingsRun: "run schedule now",
	ActSettingsModel: "edit model",

	ActHeraDelete: "nuke role/orchestrator (whole sub-team if nested)", ActHeraSpawn: "spawn worker under coordinator (new-task modal)",
	ActHeraRename: "rename role/orchestrator", ActHeraArchive: "hide worker in coord's archive (reversible)",
	ActHeraPin: "toggle pin", ActHeraStatAdv: "advance status", ActHeraStatRev: "revert status",
	ActHeraAdopt: "adopt freelancer / reparent coordinator", ActHeraNewCoord: "new coordinator (new-task modal)",
	ActHeraClear: "clear coord's archive (nuke hidden agents)",
}

// HelpRow is one rendered help line: the key chord and its action label.
type HelpRow struct {
	Key   string
	Label string
}

// HelpRows returns the rebindable bindings for a context as ordered help rows
// (resolved key string + label), following contextOrder.
func (k *Keymap) HelpRows(ctx Context) []HelpRow {
	var rows []HelpRow
	for _, act := range contextOrder[ctx] {
		if b, ok := k.fwd[ctx][act]; ok {
			rows = append(rows, HelpRow{Key: b.String(), Label: actionLabels[act]})
		}
	}
	return rows
}

// ActionLabel returns the human-readable description for an action, or the
// action id when no label is registered.
func (k *Keymap) ActionLabel(a Action) string {
	if s, ok := actionLabels[a]; ok {
		return s
	}
	return string(a)
}

// contextOrder is the per-context order actions appear in help (defaultSpecs
// maps are unordered). Only actions listed here are rendered; an action absent
// from this list still resolves but won't show in help.
var contextOrder = map[Context][]Action{
	CtxGlobal: {ActGlobalQuit, ActGlobalHelp, ActGlobalTabTasks, ActGlobalTabHera, ActGlobalTabSettings,
		ActGlobalRefresh, ActGlobalDestroy, ActGlobalFork, ActGlobalOpenRepo, ActGlobalOpenPR, ActGlobalPrune},
	CtxTaskList: {ActTaskNew, ActTaskDown, ActTaskUp, ActTaskStatusAdv, ActTaskStatusRev, ActTaskArchive,
		ActTaskPin, ActTaskRename, ActTaskCopy, ActTaskFilter, ActTaskHera},
	CtxAgent: {ActAgentLinks, ActAgentSession, ActAgentSwitcher, ActAgentOpenPR, ActAgentZoom, ActAgentCopy,
		ActAgentPaneLeft, ActAgentPaneRight, ActAgentTaskPrev, ActAgentTaskNext,
		ActAgentScrollUp, ActAgentScrollDown, ActAgentScrollPgUp, ActAgentScrollPgDn, ActAgentScrollEnd},
	CtxFilePnl: {ActFileDown, ActFileUp, ActFileFinder, ActFileOpen, ActFileEditor, ActFileTerminal},
	CtxDiff:    {ActDiffSplit, ActDiffScrollDown, ActDiffScrollUp},
	CtxSettings: {ActSettingsDown, ActSettingsUp, ActSettingsNew, ActSettingsEdit, ActSettingsDelete,
		ActSettingsQuickAdd, ActSettingsApple, ActSettingsToggle, ActSettingsRun, ActSettingsModel},
	CtxHeraRail: {ActHeraSpawn, ActHeraNewCoord, ActHeraRename, ActHeraArchive, ActHeraPin,
		ActHeraStatAdv, ActHeraStatRev, ActHeraAdopt, ActHeraClear, ActHeraDelete},
}
