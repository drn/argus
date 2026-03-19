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
// and cursor navigation — mirroring the Bubble Tea FileExplorer.
type FilePanel struct {
	*tview.Box
	files       []gitutil.ChangedFile
	rows        []fpRow
	expanded    map[string]bool
	dirChildren map[string][]gitutil.ChangedFile
	cursor      int
	offset      int
	focused     bool
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

// CursorUp moves cursor up, auto-expands directories. Returns dir needing fetch.
func (fp *FilePanel) CursorUp() string {
	if fp.cursor > 0 {
		fp.cursor--
		if fp.cursor < fp.offset {
			fp.offset = fp.cursor
		}
	}
	return fp.autoExpand()
}

// CursorDown moves cursor down, auto-expands directories. Returns dir needing fetch.
func (fp *FilePanel) CursorDown() string {
	_, _, _, h := fp.GetInnerRect()
	visible := max(h-3, 1) // reserve for border + header
	if fp.cursor < len(fp.rows)-1 {
		fp.cursor++
		if fp.cursor >= fp.offset+visible {
			fp.offset = fp.cursor - visible + 1
		}
	}
	return fp.autoExpand()
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
	drawBorder(screen, x-1, y-1, width+2, height+2, borderStyle)

	// Title in border
	title := " Files "
	if len(fp.files) > 0 {
		title = fmt.Sprintf(" Files (%d) ", len(fp.files))
	}
	for i, r := range title {
		if x+i < x+width {
			screen.SetContent(x+i, y-1, r, nil, borderStyle.Bold(true))
		}
	}

	if len(fp.rows) == 0 {
		drawText(screen, x+1, y+1, width-2, "No changes", StyleDimmed)
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
