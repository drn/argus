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
	cursor int
	offset int
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
	if fe.cursor >= len(fe.files) {
		fe.cursor = max(0, len(fe.files)-1)
	}
}

func (fe *FileExplorer) CursorUp() {
	if fe.cursor > 0 {
		fe.cursor--
		if fe.cursor < fe.offset {
			fe.offset = fe.cursor
		}
	}
}

func (fe *FileExplorer) CursorDown() {
	if fe.cursor < len(fe.files)-1 {
		fe.cursor++
		visible := fe.visibleRows()
		if fe.cursor >= fe.offset+visible {
			fe.offset = fe.cursor - visible + 1
		}
	}
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
	innerH := max(fe.height-2, 1)

	borderColor := "238"
	if focused {
		borderColor = "87"
	}
	border := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(borderColor)).
		Width(fe.width - 2).
		Height(innerH)

	header := fe.theme.Section.Render("  FILES")
	if len(fe.files) > 0 {
		header += fe.theme.Dimmed.Render(fmt.Sprintf(" (%d)", len(fe.files)))
	}

	if len(fe.files) == 0 {
		content := header + "\n" + fe.theme.Dimmed.Render("  No changes")
		return border.Render(content)
	}

	var b strings.Builder
	b.WriteString(header + "\n")

	visible := fe.visibleRows()
	end := fe.offset + visible
	if end > len(fe.files) {
		end = len(fe.files)
	}

	for i := fe.offset; i < end; i++ {
		f := fe.files[i]
		selected := focused && i == fe.cursor

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

	return border.Render(b.String())
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
