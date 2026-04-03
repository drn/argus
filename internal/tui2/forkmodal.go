package tui2

import (
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/model"
)

// ForkTaskModal shows a confirmation dialog before forking a task.
// Includes a project selector that defaults to the source task's project.
// Pressing Enter confirms, Esc cancels.
type ForkTaskModal struct {
	*tview.Box
	task      *model.Task
	confirmed bool
	canceled  bool

	// project typeahead
	projectNames  []string
	projects      map[string]config.Project
	projInput     []rune
	projCursorPos int
	projACOpen    bool
	projACMatches []string
	projACIdx     int
	projACScroll  int
}

// NewForkTaskModal creates a fork confirmation dialog for the given task.
// projects is the full project map; the source task's project is pre-filled.
func NewForkTaskModal(task *model.Task, projects map[string]config.Project) *ForkTaskModal {
	projNames := make([]string, 0, len(projects))
	for name := range projects {
		projNames = append(projNames, name)
	}
	sort.Strings(projNames)

	return &ForkTaskModal{
		Box:           tview.NewBox(),
		task:          task,
		projectNames:  projNames,
		projects:      projects,
		projInput:     []rune(task.Project),
		projCursorPos: len([]rune(task.Project)),
	}
}

func (m *ForkTaskModal) Confirmed() bool   { return m.confirmed }
func (m *ForkTaskModal) Canceled() bool    { return m.canceled }
func (m *ForkTaskModal) Task() *model.Task { return m.task }

// SelectedProject returns the resolved project name from the typeahead input.
func (m *ForkTaskModal) SelectedProject() string {
	input := string(m.projInput)
	if _, ok := m.projects[input]; ok {
		return input
	}
	lower := strings.ToLower(input)
	for _, name := range m.projectNames {
		if strings.ToLower(name) == lower {
			return name
		}
	}
	return ""
}

// updateProjectAC recomputes the project autocomplete matches.
func (m *ForkTaskModal) updateProjectAC() {
	input := strings.ToLower(string(m.projInput))
	m.projACMatches = nil
	for _, name := range m.projectNames {
		if input == "" || strings.Contains(strings.ToLower(name), input) {
			m.projACMatches = append(m.projACMatches, name)
		}
	}
	m.projACOpen = len(m.projACMatches) > 0
	if m.projACIdx >= len(m.projACMatches) {
		m.projACIdx = 0
		m.projACScroll = 0
	}
}

func (m *ForkTaskModal) projACMoveDown() {
	if len(m.projACMatches) == 0 {
		return
	}
	m.projACIdx = (m.projACIdx + 1) % len(m.projACMatches)
	if m.projACIdx == 0 {
		m.projACScroll = 0
	} else if m.projACIdx >= m.projACScroll+acMaxVisible {
		m.projACScroll = m.projACIdx - acMaxVisible + 1
	}
}

func (m *ForkTaskModal) projACMoveUp() {
	if len(m.projACMatches) == 0 {
		return
	}
	if m.projACIdx == 0 {
		m.projACIdx = len(m.projACMatches) - 1
		if m.projACIdx >= acMaxVisible {
			m.projACScroll = m.projACIdx - acMaxVisible + 1
		}
	} else {
		m.projACIdx--
		if m.projACIdx < m.projACScroll {
			m.projACScroll = m.projACIdx
		}
	}
}

func (m *ForkTaskModal) projACAccept() {
	if len(m.projACMatches) == 0 {
		return
	}
	name := m.projACMatches[m.projACIdx]
	m.projInput = []rune(name)
	m.projCursorPos = len(m.projInput)
	m.projACOpen = false
}

// InputHandler handles key events for the fork dialog.
func (m *ForkTaskModal) InputHandler() func(event *tcell.EventKey, setFocus func(p tview.Primitive)) {
	return m.WrapInputHandler(func(event *tcell.EventKey, setFocus func(p tview.Primitive)) {
		// Autocomplete navigation takes priority
		if m.projACOpen {
			switch event.Key() {
			case tcell.KeyDown, tcell.KeyTab:
				m.projACMoveDown()
				return
			case tcell.KeyUp, tcell.KeyBacktab:
				m.projACMoveUp()
				return
			case tcell.KeyEnter:
				m.projACAccept()
				return
			case tcell.KeyEscape:
				m.projACOpen = false
				return
			}
		}

		switch event.Key() {
		case tcell.KeyEnter:
			m.confirmed = true
		case tcell.KeyEscape, tcell.KeyCtrlQ:
			m.canceled = true
		case tcell.KeyBackspace, tcell.KeyBackspace2:
			if m.projCursorPos > 0 {
				_, size := utf8.DecodeLastRuneInString(string(m.projInput[:m.projCursorPos]))
				runeCount := len([]rune(string(m.projInput[:m.projCursorPos])))
				_ = size
				m.projCursorPos--
				m.projInput = append(m.projInput[:m.projCursorPos], m.projInput[m.projCursorPos+1:]...)
				_ = runeCount
				m.updateProjectAC()
			}
		case tcell.KeyLeft:
			if m.projCursorPos > 0 {
				m.projCursorPos--
			}
		case tcell.KeyRight:
			if m.projCursorPos < len(m.projInput) {
				m.projCursorPos++
			}
		case tcell.KeyCtrlA:
			m.projCursorPos = 0
		case tcell.KeyCtrlE:
			m.projCursorPos = len(m.projInput)
		case tcell.KeyCtrlU:
			m.projInput = m.projInput[:0]
			m.projCursorPos = 0
			m.updateProjectAC()
		case tcell.KeyRune:
			ch := event.Rune()
			m.projInput = append(m.projInput, 0)
			copy(m.projInput[m.projCursorPos+1:], m.projInput[m.projCursorPos:])
			m.projInput[m.projCursorPos] = ch
			m.projCursorPos++
			m.updateProjectAC()
		}
	})
}

// PasteHandler handles pasted text for the project input.
func (m *ForkTaskModal) PasteHandler() func(text string, setFocus func(p tview.Primitive)) {
	return func(text string, setFocus func(p tview.Primitive)) {
		runes := []rune(text)
		tail := append([]rune{}, m.projInput[m.projCursorPos:]...)
		m.projInput = append(m.projInput[:m.projCursorPos], runes...)
		m.projInput = append(m.projInput, tail...)
		m.projCursorPos += len(runes)
		m.updateProjectAC()
	}
}

// Draw renders the fork task modal as a centered dialog.
func (m *ForkTaskModal) Draw(screen tcell.Screen) {
	m.Box.DrawForSubclass(screen, m)
	x, y, width, height := m.GetInnerRect()
	if width <= 0 || height <= 0 {
		return
	}

	formW := min(60, width-4)
	formH := 14
	formX := x + (width-formW)/2
	formY := max(y+(height-formH)/2, y)

	// Clear the modal area.
	clearStyle := tcell.StyleDefault
	for row := formY; row < formY+formH && row < y+height; row++ {
		for col := formX; col < formX+formW; col++ {
			screen.SetContent(col, row, ' ', nil, clearStyle)
		}
	}

	drawBorder(screen, formX, formY, formW, formH, StyleFocusedBorder)
	drawText(screen, formX+2, formY+1, formW-4, "Fork task?", StyleTitle)

	// Source task name.
	drawText(screen, formX+4, formY+3, formW-6, m.task.Name, StyleNormal)

	// Details.
	if m.task.Worktree != "" {
		drawText(screen, formX+4, formY+4, formW-6, "worktree: "+m.task.Worktree, StyleDimmed)
	}
	if m.task.Branch != "" {
		drawText(screen, formX+4, formY+5, formW-6, "branch: "+m.task.Branch, StyleDimmed)
	}

	// Project selector.
	projLabel := "project:"
	projY := formY + 7
	drawText(screen, formX+4, projY, len(projLabel), projLabel, StyleDimmed)

	inputX := formX + 4 + len(projLabel) + 1
	inputW := formW - 6 - len(projLabel) - 1
	projStr := string(m.projInput)
	// Draw input with cursor
	inputStyle := StyleNormal
	for i := 0; i < inputW; i++ {
		ch := ' '
		style := inputStyle
		if i < len([]rune(projStr)) {
			ch = []rune(projStr)[i]
		}
		if i == m.projCursorPos {
			style = style.Reverse(true)
		}
		screen.SetContent(inputX+i, projY, ch, nil, style)
	}

	// Project changed indicator
	if m.SelectedProject() != "" && m.SelectedProject() != m.task.Project {
		changeNote := "(was: " + m.task.Project + ")"
		drawText(screen, formX+4, projY+1, formW-6, changeNote, StyleDimmed)
	}

	// Autocomplete dropdown
	if m.projACOpen && len(m.projACMatches) > 0 {
		acY := projY + 1
		if m.SelectedProject() != "" && m.SelectedProject() != m.task.Project {
			acY = projY + 2
		}
		visible := min(acMaxVisible, len(m.projACMatches))
		for i := 0; i < visible; i++ {
			idx := m.projACScroll + i
			if idx >= len(m.projACMatches) {
				break
			}
			name := m.projACMatches[idx]
			style := StyleNormal
			if idx == m.projACIdx {
				style = style.Reverse(true)
			}
			row := acY + i
			if row >= formY+formH-1 {
				break
			}
			for col := inputX; col < inputX+inputW; col++ {
				screen.SetContent(col, row, ' ', nil, style)
			}
			drawText(screen, inputX, row, inputW, name, style)
		}
	}

	drawText(screen, formX+4, formY+formH-3, formW-6, "Creates a new task with context from", StyleDimmed)
	drawText(screen, formX+4, formY+formH-2, formW-6, "the source agent's output and diff.", StyleDimmed)

	// Hint.
	drawText(screen, formX+4, formY+formH-1, formW-6, "[enter] confirm  [esc] cancel", StyleDimmed)
}
