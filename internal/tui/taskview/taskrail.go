package taskview

import (
	"github.com/drn/argus/internal/tui/theme"
	"github.com/drn/argus/internal/uxlog"
	"github.com/gdamore/tcell/v2"
)

// SetRailMode opts into selectable project headers and a persistent Archive fold.
func (tl *TaskListView) SetRailMode(enabled bool) {
	if tl.railMode == enabled {
		return
	}
	id := ""
	if task := tl.SelectedTask(); task != nil {
		id = task.ID
	}
	tl.railMode = enabled
	tl.buildRows()
	if id != "" {
		tl.selectByID(id)
	} else {
		tl.clampCursor()
	}
	uxlog.Log("[tasks-rail] list mode enabled=%v", enabled)
}

func (tl *TaskListView) selectable(idx int) bool {
	if idx < 0 || idx >= len(tl.rows) {
		return false
	}
	row := tl.rows[idx]
	return row.kind == rowTask || (tl.railMode && (row.kind == rowProject || row.kind == rowArchiveHeader))
}

func (tl *TaskListView) moveRailCursor(dir int) {
	for i := tl.cursor + dir; i >= 0 && i < len(tl.rows); i += dir {
		if tl.selectable(i) {
			tl.cursor = i
			tl.focusRailProject()
			tl.notifyCursorChange()
			return
		}
	}
}

func (tl *TaskListView) toggleRailFolder() bool {
	if !tl.railMode || tl.cursor < 0 || tl.cursor >= len(tl.rows) {
		return false
	}
	row := tl.rows[tl.cursor]
	sec := tl.sectionAt(tl.cursor)
	switch row.kind {
	case rowProject:
		tl.focusRailProject()
	case rowArchiveHeader:
		tl.railArchiveOpen = !tl.railArchiveOpen
	default:
		return false
	}
	tl.buildRows()
	tl.restoreCursor(row, sec)
	tl.notifyCursorChange()
	uxlog.Log("[tasks-rail] fold project=%s section=%d archive=%v", row.project, sec, tl.railArchiveOpen)
	return true
}

func (tl *TaskListView) drawRailCursor(screen tcell.Screen, x, y int) {
	style := theme.StyleSelected
	if tl.cursorDimmed {
		style = theme.StyleDimmed
	}
	screen.SetContent(x, y, '›', nil, style)
}
