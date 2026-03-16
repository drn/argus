package ui

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// ChangedFile represents a file from git status output.
type ChangedFile struct {
	Status string // e.g. "M", "A", "D", "??"
	Path   string
	IsDir  bool // true if this entry is a directory (trailing / in git status)
}

// displayRow is a flattened row in the file explorer, which may be a top-level
// file/dir or a child of an expanded directory.
type displayRow struct {
	ChangedFile
	indent int // 0 = top-level, 1+ = child of expanded dir
}

// FileExplorer displays changed files in a scrollable sidebar.
type FileExplorer struct {
	theme  Theme
	width  int
	height int
	files  []ChangedFile
	scroll ScrollState

	// Directory expansion state
	expanded    map[string]bool          // dir path -> expanded
	dirChildren map[string][]ChangedFile // dir path -> child files
	rows        []displayRow             // flattened display rows
}

func NewFileExplorer(theme Theme) FileExplorer {
	return FileExplorer{
		theme:       theme,
		expanded:    make(map[string]bool),
		dirChildren: make(map[string][]ChangedFile),
	}
}

func (fe *FileExplorer) SetSize(w, h int) {
	fe.width = w
	fe.height = h
}

func (fe *FileExplorer) SetFiles(files []ChangedFile) {
	fe.files = files
	// Prune expansion state for directories no longer in the file list
	currentDirs := make(map[string]bool, len(files))
	for _, f := range files {
		if f.IsDir {
			currentDirs[f.Path] = true
		}
	}
	for path := range fe.expanded {
		if !currentDirs[path] {
			delete(fe.expanded, path)
			delete(fe.dirChildren, path)
		}
	}
	fe.buildDisplayRows()
	fe.scroll.ClampCursor(len(fe.rows))
}

// ToggleDir toggles the expansion of a directory. Returns true if the directory
// needs its children fetched (newly expanded with no cached children).
func (fe *FileExplorer) ToggleDir(dirPath string) bool {
	if fe.expanded[dirPath] {
		// Collapse
		fe.expanded[dirPath] = false
		fe.buildDisplayRows()
		fe.scroll.ClampCursor(len(fe.rows))
		return false
	}
	// Expand
	fe.expanded[dirPath] = true
	if _, ok := fe.dirChildren[dirPath]; ok {
		// Already have children cached
		fe.buildDisplayRows()
		fe.scroll.ClampCursor(len(fe.rows))
		return false
	}
	return true // caller needs to fetch children
}

// SetDirChildren sets the children for an expanded directory and rebuilds rows.
func (fe *FileExplorer) SetDirChildren(dirPath string, children []ChangedFile) {
	fe.dirChildren[dirPath] = children
	fe.buildDisplayRows()
	fe.scroll.ClampCursor(len(fe.rows))
}

func (fe *FileExplorer) buildDisplayRows() {
	fe.rows = nil
	for _, f := range fe.files {
		fe.rows = append(fe.rows, displayRow{ChangedFile: f, indent: 0})
		if f.IsDir && fe.expanded[f.Path] {
			if children, ok := fe.dirChildren[f.Path]; ok {
				for _, child := range children {
					fe.rows = append(fe.rows, displayRow{ChangedFile: child, indent: 1})
				}
			}
		}
	}
}

func (fe *FileExplorer) CursorUp() {
	fe.scroll.CursorUp()
}

func (fe *FileExplorer) CursorDown() {
	fe.scroll.CursorDown(len(fe.rows), fe.visibleRows())
}

// SelectedRow returns the currently selected display row, or nil if none.
func (fe *FileExplorer) SelectedRow() *displayRow {
	c := fe.scroll.Cursor()
	if c < 0 || c >= len(fe.rows) {
		return nil
	}
	return &fe.rows[c]
}

// SelectedFile returns the currently selected file, or nil if none.
func (fe *FileExplorer) SelectedFile() *ChangedFile {
	row := fe.SelectedRow()
	if row == nil {
		return nil
	}
	return &row.ChangedFile
}

// FileCount returns the number of top-level files.
func (fe *FileExplorer) FileCount() int {
	return len(fe.files)
}

// DisplayRowCount returns the total number of visible rows (including expanded children).
func (fe *FileExplorer) DisplayRowCount() int {
	return len(fe.rows)
}

func (fe *FileExplorer) visibleRows() int {
	// Reserve 3 for border + header
	rows := fe.height - 3
	if rows < 1 {
		rows = 1
	}
	return rows
}

func (fe FileExplorer) View(focused bool) string {
	innerW := max(fe.width-4, 10)

	renderPanel := func(content string) string {
		return borderedPanel(fe.width, fe.height, focused, content)
	}

	header := fe.theme.Section.Render("  FILES")
	if len(fe.files) > 0 {
		header += fe.theme.Dimmed.Render(fmt.Sprintf(" (%d)", len(fe.files)))
	}

	if len(fe.rows) == 0 {
		content := header + "\n" + fe.theme.Dimmed.Render("  No changes")
		return renderPanel(content)
	}

	var b strings.Builder
	b.WriteString(header + "\n")

	visible := fe.visibleRows()
	offset := fe.scroll.Offset()
	cursor := fe.scroll.Cursor()
	end := offset + visible
	if end > len(fe.rows) {
		end = len(fe.rows)
	}

	for i := offset; i < end; i++ {
		row := fe.rows[i]
		selected := focused && i == cursor

		nameStyle := fe.theme.Normal
		if selected {
			nameStyle = fe.theme.Selected
		}

		cursorStr := "  "
		if selected {
			cursorStr = fe.theme.Selected.Render(" ▸")
		}

		if row.IsDir {
			// Directory row with expand/collapse indicator
			arrow := "▶"
			if fe.expanded[row.Path] {
				arrow = "▼"
			}
			dirName := strings.TrimSuffix(row.Path, "/")
			// Truncate to fit
			maxNameW := innerW - 8
			if len(dirName) > maxNameW && maxNameW > 3 {
				dirName = "…" + dirName[len(dirName)-maxNameW+1:]
			}

			statusStyle := fe.statusStyle(row.Status)
			indicator := statusStyle.Render(fe.statusIcon(row.Status))

			arrowStyle := fe.theme.Dimmed
			if selected {
				arrowStyle = fe.theme.Selected
			}

			b.WriteString(fmt.Sprintf("%s %s %s %s\n",
				cursorStr,
				indicator,
				arrowStyle.Render(arrow),
				nameStyle.Render(dirName+"/"),
			))
		} else if row.indent > 0 {
			// Child file of expanded directory — indented
			statusStyle := fe.statusStyle(row.Status)
			indicator := statusStyle.Render(fe.statusIcon(row.Status))

			// Show just the filename (relative to parent dir)
			name := filepath.Base(row.Path)
			maxNameW := innerW - 10 // extra indent
			if len(name) > maxNameW && maxNameW > 3 {
				name = "…" + name[len(name)-maxNameW+1:]
			}

			b.WriteString(fmt.Sprintf("%s   %s %s\n",
				cursorStr,
				indicator,
				nameStyle.Render(name),
			))
		} else {
			// Regular file row (unchanged from before)
			statusStyle := fe.statusStyle(row.Status)
			indicator := statusStyle.Render(fe.statusIcon(row.Status))

			name := row.Path
			maxNameW := innerW - 6
			if len(name) > maxNameW && maxNameW > 3 {
				name = "…" + name[len(name)-maxNameW+1:]
			}

			b.WriteString(fmt.Sprintf("%s %s %s\n", cursorStr, indicator, nameStyle.Render(name)))
		}
	}

	return renderPanel(b.String())
}

func (fe FileExplorer) statusIcon(status string) string {
	switch status {
	case "M", "MM":
		return "M"
	case "A":
		return "A"
	case "D":
		return "D"
	case "??":
		return "?"
	case "R":
		return "R"
	default:
		return status
	}
}

func (fe FileExplorer) statusStyle(status string) lipgloss.Style {
	switch status {
	case "M", "MM":
		return fe.theme.InReview
	case "A", "??":
		return fe.theme.Complete
	case "D":
		return fe.theme.Error
	default:
		return fe.theme.Normal
	}
}

// ParseGitStatus parses `git status --short` output into ChangedFile entries.
func ParseGitStatus(output string) []ChangedFile {
	if output == "" {
		return nil
	}
	lines := strings.Split(strings.TrimRight(output, "\n"), "\n")
	var files []ChangedFile
	for _, line := range lines {
		if len(line) < 4 {
			continue
		}
		status := strings.TrimSpace(line[:2])
		path := strings.TrimSpace(line[3:])
		if path != "" {
			isDir := strings.HasSuffix(path, "/")
			files = append(files, ChangedFile{Status: status, Path: path, IsDir: isDir})
		}
	}
	return files
}

// ParseGitDiffNameStatus parses `git diff --name-status` output into ChangedFile entries.
func ParseGitDiffNameStatus(output string) []ChangedFile {
	if output == "" {
		return nil
	}
	lines := strings.Split(strings.TrimRight(output, "\n"), "\n")
	var files []ChangedFile
	for _, line := range lines {
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) != 2 {
			continue
		}
		status := strings.TrimSpace(parts[0])
		path := strings.TrimSpace(parts[1])
		if path != "" {
			isDir := strings.HasSuffix(path, "/")
			files = append(files, ChangedFile{Status: status, Path: path, IsDir: isDir})
		}
	}
	return files
}

// MergeChangedFiles merges two file lists into a single deduplicated, sorted slice.
// Files in overlay take precedence over files in base when the same path appears in both.
// This is used to combine committed branch files (base) with uncommitted changes (overlay)
// so the file explorer always shows the full picture of what changed on the branch.
func MergeChangedFiles(base, overlay []ChangedFile) []ChangedFile {
	if len(base) == 0 && len(overlay) == 0 {
		return nil
	}
	seen := make(map[string]ChangedFile, len(base)+len(overlay))
	for _, f := range base {
		seen[f.Path] = f
	}
	for _, f := range overlay {
		seen[f.Path] = f // overlay wins on conflict
	}
	result := make([]ChangedFile, 0, len(seen))
	for _, f := range seen {
		result = append(result, f)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Path < result[j].Path
	})
	return result
}
