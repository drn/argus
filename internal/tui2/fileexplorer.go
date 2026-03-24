package tui2

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/drn/argus/internal/gitutil"
)

// FilePanel is a file explorer panel for the tcell agent view.
// It displays changed files with status icons, directory expansion,
// and cursor navigation.
type FilePanel struct {
	*tview.Box
	files       []gitutil.ChangedFile
	rows        []fpRow
	expanded    map[string]bool
	dirChildren map[string][]gitutil.ChangedFile
	cursor      int
	offset      int
	focused     bool

	// OnClick is called when the user clicks on the file panel.
	// The app wires this to switch agentFocus so keyboard events route here.
	OnClick func()
}

type fpRow struct {
	gitutil.ChangedFile
	indent int
}

// NewFilePanel creates a file explorer panel.
func NewFilePanel() *FilePanel {
	return &FilePanel{
		Box:         tview.NewBox(),
		expanded:    make(map[string]bool),
		dirChildren: make(map[string][]gitutil.ChangedFile),
	}
}

// SetFiles updates the file list and rebuilds rows.
func (fp *FilePanel) SetFiles(files []gitutil.ChangedFile) {
	fp.files = files
	// Prune stale expansion state
	dirs := make(map[string]bool)
	for _, f := range files {
		if f.IsDir {
			dirs[f.Path] = true
		}
	}
	for path := range fp.expanded {
		if !dirs[path] {
			delete(fp.expanded, path)
			delete(fp.dirChildren, path)
		}
	}
	fp.buildRows()
	fp.clampCursor()
}

// SetDirChildren caches directory children and rebuilds.
func (fp *FilePanel) SetDirChildren(dir string, children []gitutil.ChangedFile) {
	fp.dirChildren[dir] = children
	fp.buildRows()
	fp.clampCursor()
}

// SetFocused updates the focus state.
func (fp *FilePanel) SetFocused(f bool) {
	fp.focused = f
}

// SelectedFile returns the file at the cursor, or nil.
func (fp *FilePanel) SelectedFile() *gitutil.ChangedFile {
	if fp.cursor < 0 || fp.cursor >= len(fp.rows) {
		return nil
	}
	return &fp.rows[fp.cursor].ChangedFile
}

// FileCount returns the number of top-level files.
func (fp *FilePanel) FileCount() int {
	return len(fp.files)
}

// CursorUp moves cursor up, skipping directories. Returns dir needing fetch.
func (fp *FilePanel) CursorUp() string {
	if fp.cursor > 0 {
		fp.cursor--
		if fp.cursor < fp.offset {
			fp.offset = fp.cursor
		}
	}
	fetch := fp.autoExpand()
	fp.skipToFile(-1)
	fp.ensureVisible()
	return fetch
}

// CursorDown moves cursor down, skipping directories. Returns dir needing fetch.
func (fp *FilePanel) CursorDown() string {
	_, _, _, h := fp.GetInnerRect()
	visible := max(h-3, 1) // reserve for border + header
	if fp.cursor < len(fp.rows)-1 {
		fp.cursor++
		if fp.cursor >= fp.offset+visible {
			fp.offset = fp.cursor - visible + 1
		}
	}
	fetch := fp.autoExpand()
	fp.skipToFile(1)
	fp.ensureVisible()
	return fetch
}

// skipToFile advances the cursor past directory rows in the given direction.
// If no file rows exist, the cursor stays on the current row (directory).
func (fp *FilePanel) skipToFile(dir int) {
	start := fp.cursor
	for fp.cursor >= 0 && fp.cursor < len(fp.rows) {
		if !fp.rows[fp.cursor].IsDir {
			return
		}
		fp.cursor += dir
	}
	// Went past bounds — search the other direction.
	fp.cursor = max(0, min(fp.cursor, len(fp.rows)-1))
	for fp.cursor >= 0 && fp.cursor < len(fp.rows) {
		if !fp.rows[fp.cursor].IsDir {
			return
		}
		fp.cursor -= dir
	}
	// No file rows at all — stay put.
	fp.cursor = start
}

// ensureVisible adjusts offset so cursor is within the viewport.
func (fp *FilePanel) ensureVisible() {
	_, _, _, h := fp.GetInnerRect()
	visible := max(h-3, 1)
	if fp.cursor < fp.offset {
		fp.offset = fp.cursor
	} else if fp.cursor >= fp.offset+visible {
		fp.offset = fp.cursor - visible + 1
	}
}

// CursorIndex returns the current cursor index.
func (fp *FilePanel) CursorIndex() int {
	return fp.cursor
}

func (fp *FilePanel) buildRows() {
	fp.rows = nil
	for _, f := range fp.files {
		fp.rows = append(fp.rows, fpRow{ChangedFile: f, indent: 0})
		if f.IsDir && fp.expanded[f.Path] {
			if children, ok := fp.dirChildren[f.Path]; ok {
				for _, child := range children {
					fp.rows = append(fp.rows, fpRow{ChangedFile: child, indent: 1})
				}
			}
		}
	}
}

func (fp *FilePanel) clampCursor() {
	if fp.cursor >= len(fp.rows) {
		fp.cursor = max(0, len(fp.rows)-1)
	}
}

func (fp *FilePanel) autoExpand() string {
	if fp.cursor < 0 || fp.cursor >= len(fp.rows) {
		return ""
	}
	row := fp.rows[fp.cursor]
	cursorPath := row.Path

	var targetDir string
	if row.IsDir {
		targetDir = row.Path
	} else if row.indent > 0 {
		for i := fp.cursor - 1; i >= 0; i-- {
			if fp.rows[i].IsDir && fp.rows[i].indent == 0 {
				targetDir = fp.rows[i].Path
				break
			}
		}
	}

	for dir := range fp.expanded {
		if dir != targetDir {
			fp.expanded[dir] = false
		}
	}

	var needsFetch string
	if targetDir != "" && !fp.expanded[targetDir] {
		fp.expanded[targetDir] = true
		if _, ok := fp.dirChildren[targetDir]; !ok {
			needsFetch = targetDir
		}
	}

	fp.buildRows()
	for i, r := range fp.rows {
		if r.Path == cursorPath {
			fp.cursor = i
			break
		}
	}
	fp.clampCursor()
	return needsFetch
}

// Draw renders the file explorer panel.
func (fp *FilePanel) Draw(screen tcell.Screen) {
	fp.Box.DrawForSubclass(screen, fp)
	x, y, width, height := fp.GetInnerRect()
	if width <= 0 || height <= 0 {
		return
	}

	// Draw border
	borderStyle := StyleBorder
	if fp.focused {
		borderStyle = StyleFocusedBorder
	}
	title := " Files "
	if len(fp.files) > 0 {
		title = fmt.Sprintf(" Files (%d) ", len(fp.files))
	}
	inner := drawBorderedPanel(screen, x, y, width, height, title, borderStyle)
	x, y, width, height = inner.X, inner.Y, inner.W, inner.H
	if width <= 0 || height <= 0 {
		return
	}

	if len(fp.rows) == 0 {
		drawText(screen, x, y, width, "No changes", StyleDimmed)
		return
	}

	visible := height
	for i := 0; i < visible; i++ {
		idx := fp.offset + i
		if idx >= len(fp.rows) {
			break
		}
		row := fp.rows[idx]
		isCursor := fp.focused && idx == fp.cursor

		nameStyle := StyleNormal
		if isCursor {
			nameStyle = StyleSelected
		}

		col := x + 1
		if isCursor {
			screen.SetContent(col, y+i, '▸', nil, StyleSelected)
		}
		col += 2

		// Status icon
		icon, iconStyle := fp.statusIcon(row.Status)
		screen.SetContent(col, y+i, icon, nil, iconStyle)
		col += 2

		// Indent for children
		if row.indent > 0 {
			col += 2
		}

		// Name
		name := row.Path
		if row.indent > 0 {
			name = filepath.Base(row.Path)
		}
		if row.IsDir {
			arrow := '▶'
			if fp.expanded[row.Path] {
				arrow = '▼'
			}
			screen.SetContent(col, y+i, arrow, nil, nameStyle)
			col += 2
			name = strings.TrimSuffix(name, "/") + "/"
		}

		maxNameW := x + width - col - 1
		if len(name) > maxNameW && maxNameW > 3 {
			name = "…" + name[len(name)-maxNameW+1:]
		}
		drawText(screen, col, y+i, maxNameW, name, nameStyle)
	}
}

// MouseHandler handles mouse events — clicks focus the panel and position the cursor.
func (fp *FilePanel) MouseHandler() func(action tview.MouseAction, event *tcell.EventMouse, setFocus func(p tview.Primitive)) (consumed bool, capture tview.Primitive) {
	return fp.WrapMouseHandler(func(action tview.MouseAction, event *tcell.EventMouse, setFocus func(p tview.Primitive)) (consumed bool, capture tview.Primitive) {
		if !fp.InRect(event.Position()) {
			return false, nil
		}
		if action == tview.MouseLeftDown || action == tview.MouseLeftClick {
			setFocus(fp)
			consumed = true

			// Notify the app to switch agentFocus.
			if fp.OnClick != nil {
				fp.OnClick()
			}

			// Position cursor on the clicked row (content starts 1 cell inside border).
			_, ey, _, _ := fp.GetInnerRect()
			_, my := event.Position()
			clickedRow := fp.offset + (my - ey - 1)
			if clickedRow >= 0 && clickedRow < len(fp.rows) {
				fp.cursor = clickedRow
			}
		}

		if action == tview.MouseScrollUp {
			fp.CursorUp()
			consumed = true
		}
		if action == tview.MouseScrollDown {
			fp.CursorDown()
			consumed = true
		}

		return
	})
}

func (fp *FilePanel) statusIcon(status string) (rune, tcell.Style) {
	switch status {
	case "M", "MM":
		return 'M', tcell.StyleDefault.Foreground(ColorInReview)
	case "A":
		return 'A', tcell.StyleDefault.Foreground(ColorComplete)
	case "D":
		return 'D', tcell.StyleDefault.Foreground(ColorError)
	case "??":
		return '?', tcell.StyleDefault.Foreground(ColorComplete)
	case "R":
		return 'R', tcell.StyleDefault.Foreground(ColorInReview)
	default:
		return '·', StyleDimmed
	}
}
