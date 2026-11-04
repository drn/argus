package tui

import (
	"cmp"
	"slices"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/tui/theme"
	"github.com/drn/argus/internal/tui/widget"
	"github.com/drn/argus/internal/uxlog"
)

// EnableProjectFirst is opt-in at independent creation sites, never the shared
// constructor: worker spawn uses the same form but keeps its existing flow.
func (f *NewTaskForm) EnableProjectFirst(tasks []*model.Task) {
	if len(f.projectNames) < 2 {
		if len(f.projectNames) == 1 {
			f.projInput = []rune(f.projectNames[0])
			f.projCursorPos = len(f.projInput)
			f.onProjectChanged()
		}
		uxlog.Log("[newtask] project-first bypass: %d projects", len(f.projectNames))
		return
	}
	defaultProject := f.resolveProject()
	recent := map[string]time.Time{}
	for _, task := range tasks {
		if task != nil && task.CreatedAt.After(recent[task.Project]) {
			recent[task.Project] = task.CreatedAt
		}
	}
	f.projectStepOrder = slices.Clone(f.projectNames)
	slices.SortFunc(f.projectStepOrder, func(a, b string) int {
		if a == b {
			return 0
		}
		if a == defaultProject {
			return -1
		}
		if b == defaultProject {
			return 1
		}
		if c := recent[b].Compare(recent[a]); c != 0 {
			return c
		}
		return cmp.Compare(a, b)
	})
	f.projectFirst = true
	f.openProjectStep(defaultProject)
}

func (f *NewTaskForm) openProjectStep(current string) {
	f.projectStep = true
	f.focused = ntFieldProject
	f.projInput = nil
	f.projCursorPos = 0
	f.closeAC()
	f.branchACOpen = false
	f.updateProjectAC()
	if idx := slices.Index(f.projACMatches, current); idx >= 0 {
		f.projACIdx = idx
		f.projACScroll = max(0, idx-acMaxVisible+1)
	}
	uxlog.Log("[newtask] project-first opened: current=%s candidates=%d", current, len(f.projACMatches))
}

func (f *NewTaskForm) handleProjectStepKey(event *tcell.EventKey) {
	switch event.Key() {
	case tcell.KeyEscape, tcell.KeyCtrlQ:
		f.canceled = true
		uxlog.Log("[newtask] project-first canceled")
	case tcell.KeyEnter:
		if len(f.projACMatches) == 0 {
			f.errMsg = "Choose a known project"
			return
		}
		// The existing acceptance path owns all project-dependent defaults.
		f.projACAccept()
		f.projectStep = false
		f.focused = ntFieldPrompt
		uxlog.Log("[newtask] project-first accepted: project=%s", f.resolveProject())
	case tcell.KeyDown:
		f.projACMoveDown()
	case tcell.KeyUp:
		f.projACMoveUp()
	case tcell.KeyTab, tcell.KeyBacktab:
		// There is only one field; Enter confirms it.
	default:
		f.handleProjectKey(event)
	}
}

func (f *NewTaskForm) drawProjectStep(screen tcell.Screen, sx, sy, sw, sh int) {
	w := min(60, sw-4)
	rows := min(len(f.projACMatches), acMaxVisible)
	if len(f.projACMatches) > acMaxVisible {
		rows++
	}
	rows = max(rows, 1)
	h := 8 + rows
	if w < 20 || h > sh {
		return
	}
	x, y := sx+(sw-w)/2, sy+(sh-h)/2
	widget.FillArea(screen, x, y, w, h, ' ', tcell.StyleDefault)
	widget.DrawBorder(screen, x, y, w, h, theme.StyleFocusedBorder)
	widget.DrawText(screen, x+2, y, w-4, " Choose project ", theme.StyleTitle)
	f.drawProjectField(screen, x+2, y+2, w-4)
	if len(f.projACMatches) == 0 {
		widget.DrawText(screen, x+2, y+3, w-4, "No matching projects", theme.StyleDimmed)
	} else {
		f.drawProjectAC(screen, x+2, y+3, w-4)
	}
	widget.DrawText(screen, x+2, y+h-3, w-4, "↑/↓ select  Enter continue  Esc cancel", theme.StyleDimmed)
}
