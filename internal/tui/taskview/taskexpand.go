package taskview

import "github.com/drn/argus/internal/uxlog"

// ExpandAll reports the remembered project expansion preference.
func (tl *TaskListView) ExpandAll() bool { return tl.expandAll }

func (tl *TaskListView) SetExpandAll(enabled bool) {
	if tl.expandAll == enabled {
		return
	}
	var prev taskRow
	hasPrev := tl.cursor >= 0 && tl.cursor < len(tl.rows)
	sec := sectionActive
	if hasPrev {
		prev = tl.rows[tl.cursor]
		sec = tl.sectionAt(tl.cursor)
	}
	tl.expandAll = enabled
	if hasPrev && prev.project != "" {
		if sec == sectionActive {
			tl.expanded = prev.project
		}
		if sec == sectionArchive {
			tl.archiveProject = prev.project
		}
	}
	tl.buildRows()
	if hasPrev {
		tl.restoreCursor(prev, sec)
	} else {
		tl.clampCursor()
	}
	uxlog.Log("[tasks-rail] expand all=%v", enabled)
}

func (tl *TaskListView) focusRailProject() {
	if tl.cursor < 0 || tl.cursor >= len(tl.rows) {
		return
	}
	row := tl.rows[tl.cursor]
	if row.project == "" {
		return
	}
	sec := tl.sectionAt(tl.cursor)
	changed := false
	if sec == sectionActive && tl.expanded != row.project {
		tl.expanded = row.project
		changed = true
	}
	if sec == sectionArchive && tl.archiveProject != row.project {
		tl.archiveProject = row.project
		changed = true
	}
	if changed && !tl.expandAll && tl.filter == "" {
		tl.buildRows()
		tl.restoreCursor(row, sec)
	}
}
