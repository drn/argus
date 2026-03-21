package tui2

import (
	"sort"
	"unicode/utf8"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/drn/argus/internal/config"
)

// LaunchToDoModal is a modal overlay that lets the user pick a project
// and confirm launching a to-do note as a new Argus task.
type LaunchToDoModal struct {
	*tview.Box

	item         ToDoItem
	projectNames []string
	projectIdx   int
	projects     map[string]config.Project

	done     bool
	canceled bool
	errMsg   string
}

// NewLaunchToDoModal creates the launch confirmation modal.
func NewLaunchToDoModal(item ToDoItem, projects map[string]config.Project, defaultProject string) *LaunchToDoModal {
	names := make([]string, 0, len(projects))
	for n := range projects {
		names = append(names, n)
	}
	sort.Strings(names)

	idx := 0
	for i, n := range names {
		if n == defaultProject {
			idx = i
			break
		}
	}

	return &LaunchToDoModal{
		Box:          tview.NewBox(),
		item:         item,
		projectNames: names,
		projectIdx:   idx,
		projects:     projects,
	}
}

// Done returns true if the user confirmed.
func (m *LaunchToDoModal) Done() bool { return m.done }

// Canceled returns true if the user canceled.
func (m *LaunchToDoModal) Canceled() bool { return m.canceled }

// SelectedProject returns the chosen project name.
func (m *LaunchToDoModal) SelectedProject() string {
	if m.projectIdx < len(m.projectNames) {
		return m.projectNames[m.projectIdx]
	}
	return ""
}

// SetError sets an error message.
func (m *LaunchToDoModal) SetError(msg string) {
	m.errMsg = msg
	m.done = false
}

// Item returns the to-do item being launched.
func (m *LaunchToDoModal) Item() ToDoItem {
	return m.item
}

// InputHandler handles key events for the modal.
func (m *LaunchToDoModal) InputHandler() func(event *tcell.EventKey, setFocus func(p tview.Primitive)) {
	return m.WrapInputHandler(func(event *tcell.EventKey, setFocus func(p tview.Primitive)) {
		m.errMsg = ""

		switch event.Key() {
		case tcell.KeyEscape:
			m.canceled = true
			return
		case tcell.KeyEnter:
			if len(m.projectNames) > 0 {
				m.done = true
			}
			return
		case tcell.KeyLeft:
			if len(m.projectNames) > 0 {
				m.projectIdx = (m.projectIdx - 1 + len(m.projectNames)) % len(m.projectNames)
			}
		case tcell.KeyRight:
			if len(m.projectNames) > 0 {
				m.projectIdx = (m.projectIdx + 1) % len(m.projectNames)
			}
		}
	})
}

// Draw renders the launch confirmation modal.
func (m *LaunchToDoModal) Draw(screen tcell.Screen) {
	m.Box.DrawForSubclass(screen, m)
	sx, sy, sw, sh := m.GetInnerRect()
	if sw <= 0 || sh <= 0 {
		return
	}

	modalW := min(60, sw-4)
	if modalW < 20 {
		return
	}
	innerW := modalW - 4

	// Height: border(1) + padding(1) + name(1) + gap(1) + project(2) + gap(1) + help(1) + padding(1) + border(1) = 10
	modalH := 10
	if m.errMsg != "" {
		modalH += 2
	}
	if modalH > sh {
		return
	}

	mx := sx + (sw-modalW)/2
	my := sy + (sh-modalH)/2

	// Clear modal area
	clearStyle := tcell.StyleDefault.Background(tcell.ColorDefault)
	for row := my; row < my+modalH; row++ {
		for col := mx; col < mx+modalW; col++ {
			screen.SetContent(col, row, ' ', nil, clearStyle)
		}
	}

	drawBorder(screen, mx, my, modalW, modalH, StyleFocusedBorder)

	// Title
	title := " Launch as Task "
	titleX := mx + (modalW-utf8.RuneCountInString(title))/2
	titleStyle := tcell.StyleDefault.Foreground(ColorTitle).Bold(true)
	for i, r := range title {
		screen.SetContent(titleX+i, my, r, nil, titleStyle)
	}

	innerX := mx + 2
	row := my + 2

	// To-do name
	name := m.item.Name
	if len(name) > innerW {
		name = name[:innerW-1] + "…"
	}
	drawText(screen, innerX, row, innerW, name, StyleNormal.Bold(true))
	row += 2

	// Project selector
	drawText(screen, innerX, row, innerW, "Project:", StyleTitle)
	row++

	if len(m.projectNames) == 0 {
		drawText(screen, innerX, row, innerW, "(no projects configured)", StyleDimmed)
	} else {
		name := m.projectNames[m.projectIdx]
		selector := "◀ " + name + " ▶"
		drawText(screen, innerX, row, innerW, selector, StyleSelected)

		posText := "(" + itoa(m.projectIdx+1) + "/" + itoa(len(m.projectNames)) + ")"
		posX := innerX + innerW - len(posText)
		if posX > innerX+len(selector)+1 {
			drawText(screen, posX, row, len(posText), posText, StyleDimmed)
		}
	}
	row++

	// Error
	if m.errMsg != "" {
		row++
		drawText(screen, innerX, row, innerW, m.errMsg, StyleError)
		row++
	}

	row++ // gap

	// Help
	help := "Enter confirm  ◀▶ project  Esc cancel"
	drawText(screen, innerX, row, innerW, help, StyleDimmed)
}
