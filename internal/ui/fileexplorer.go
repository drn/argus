package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// ChangedFile represents a file from git status output.
type ChangedFile struct {
	Status string // e.g. "M", "A", "D", "??"
	Path   string
}

// FileExplorer displays changed files in a scrollable sidebar.
type FileExplorer struct {
	theme  Theme
	width  int
	height int
	files  []ChangedFile
	scroll ScrollState
}

func NewFileExplorer(theme Theme) FileExplorer {
	return FileExplorer{theme: theme}
}

func (fe *FileExplorer) SetSize(w, h int) {
	fe.width = w
	fe.height = h
}

func (fe *FileExplorer) SetFiles(files []ChangedFile) {
	fe.files = files
	fe.scroll.ClampCursor(len(fe.files))
}

func (fe *FileExplorer) CursorUp() {
	fe.scroll.CursorUp()
}

func (fe *FileExplorer) CursorDown() {
	fe.scroll.CursorDown(len(fe.files), fe.visibleRows())
}

// SelectedFile returns the currently selected file, or nil if none.
func (fe *FileExplorer) SelectedFile() *ChangedFile {
	c := fe.scroll.Cursor()
	if c < 0 || c >= len(fe.files) {
		return nil
	}
	return &fe.files[c]
}

// FileCount returns the number of files.
func (fe *FileExplorer) FileCount() int {
	return len(fe.files)
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

	if len(fe.files) == 0 {
		content := header + "\n" + fe.theme.Dimmed.Render("  No changes")
		return renderPanel(content)
	}

	var b strings.Builder
	b.WriteString(header + "\n")

	visible := fe.visibleRows()
	offset := fe.scroll.Offset()
	cursor := fe.scroll.Cursor()
	end := offset + visible
	if end > len(fe.files) {
		end = len(fe.files)
	}

	for i := offset; i < end; i++ {
		f := fe.files[i]
		selected := focused && i == cursor

		// Status indicator with color
		statusStyle := fe.statusStyle(f.Status)
		indicator := statusStyle.Render(fe.statusIcon(f.Status))

		// File path (just the filename, not full path)
		name := f.Path
		// Truncate to fit
		maxNameW := innerW - 6
		if len(name) > maxNameW && maxNameW > 3 {
			name = "…" + name[len(name)-maxNameW+1:]
		}

		nameStyle := fe.theme.Normal
		if selected {
			nameStyle = fe.theme.Selected
		}

		cursor := "  "
		if selected {
			cursor = fe.theme.Selected.Render(" ▸")
		}

		b.WriteString(fmt.Sprintf("%s %s %s\n", cursor, indicator, nameStyle.Render(name)))
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
			files = append(files, ChangedFile{Status: status, Path: path})
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
			files = append(files, ChangedFile{Status: status, Path: path})
		}
	}
	return files
}
