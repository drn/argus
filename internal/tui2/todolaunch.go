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

const (
	ltFieldProject = 0
	ltFieldPrompt  = 1
)

// ltMaxPromptLines is the maximum visible lines for the prompt textarea.
const ltMaxPromptLines = 6

// LaunchToDoModal is a modal overlay that lets the user pick a project,
// enter a prompt, and confirm launching a to-do note as a new Argus task.
type LaunchToDoModal struct {
	*tview.Box

	item         ToDoItem
	projectNames []string
	projectIdx   int
	projects     map[string]config.Project

	// Prompt input state
	prompt       []rune
	cursorPos    int
	scrollOffset int
	promptWidth  int // cached from last Draw
	focused      int // ltFieldProject or ltFieldPrompt

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
		focused:      ltFieldPrompt, // start on prompt
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

// Prompt returns the user-entered prompt text (trimmed).
func (m *LaunchToDoModal) Prompt() string {
	return strings.TrimSpace(string(m.prompt))
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

// PasteHandler handles bracketed paste events for the prompt field.
func (m *LaunchToDoModal) PasteHandler() func(pastedText string, setFocus func(p tview.Primitive)) {
	return m.WrapPasteHandler(func(pastedText string, setFocus func(p tview.Primitive)) {
		if m.focused != ltFieldPrompt {
			return
		}
		m.errMsg = ""
		runes := []rune(pastedText)
		if len(runes) == 0 {
			return
		}
		newPrompt := make([]rune, 0, len(m.prompt)+len(runes))
		newPrompt = append(newPrompt, m.prompt[:m.cursorPos]...)
		newPrompt = append(newPrompt, runes...)
		newPrompt = append(newPrompt, m.prompt[m.cursorPos:]...)
		m.prompt = newPrompt
		m.cursorPos += len(runes)
	})
}

// InputHandler handles key events for the modal.
func (m *LaunchToDoModal) InputHandler() func(event *tcell.EventKey, setFocus func(p tview.Primitive)) {
	return m.WrapInputHandler(func(event *tcell.EventKey, setFocus func(p tview.Primitive)) {
		m.errMsg = ""

		// Global keys
		switch event.Key() {
		case tcell.KeyEscape:
			m.canceled = true
			return
		case tcell.KeyTab:
			m.focused = (m.focused + 1) % 2
			return
		case tcell.KeyBacktab:
			m.focused = (m.focused + 1) % 2
			return
		}

		switch m.focused {
		case ltFieldProject:
			m.handleProjectKey(event)
		case ltFieldPrompt:
			m.handlePromptKey(event)
		}
	})
}

func (m *LaunchToDoModal) handleProjectKey(event *tcell.EventKey) {
	if len(m.projectNames) == 0 {
		return
	}
	switch event.Key() {
	case tcell.KeyLeft:
		m.projectIdx = (m.projectIdx - 1 + len(m.projectNames)) % len(m.projectNames)
	case tcell.KeyRight:
		m.projectIdx = (m.projectIdx + 1) % len(m.projectNames)
	case tcell.KeyDown, tcell.KeyEnter:
		m.focused = ltFieldPrompt
	case tcell.KeyUp:
		m.focused = ltFieldPrompt // wrap around
	}
}

func (m *LaunchToDoModal) handlePromptKey(event *tcell.EventKey) {
	mod := event.Modifiers()
	hasAlt := mod&tcell.ModAlt != 0

	switch event.Key() {
	case tcell.KeyEnter:
		if len(m.projectNames) > 0 {
			m.done = true
		}
		return
	case tcell.KeyBackspace, tcell.KeyBackspace2:
		if hasAlt {
			m.prompt, m.cursorPos = deleteWordLeft(m.prompt, m.cursorPos)
			return
		}
		if m.cursorPos > 0 {
			m.prompt = append(m.prompt[:m.cursorPos-1], m.prompt[m.cursorPos:]...)
			m.cursorPos--
		}
		return
	case tcell.KeyCtrlW:
		m.prompt, m.cursorPos = deleteWordLeft(m.prompt, m.cursorPos)
		return
	case tcell.KeyDelete:
		if hasAlt {
			m.prompt, m.cursorPos = deleteWordRight(m.prompt, m.cursorPos)
			return
		}
		if m.cursorPos < len(m.prompt) {
			m.prompt = append(m.prompt[:m.cursorPos], m.prompt[m.cursorPos+1:]...)
		}
		return
	case tcell.KeyLeft:
		if hasAlt {
			m.cursorPos = wordLeftPos(m.prompt, m.cursorPos)
			return
		}
		if m.cursorPos > 0 {
			m.cursorPos--
		}
		return
	case tcell.KeyRight:
		if hasAlt {
			m.cursorPos = wordRightPos(m.prompt, m.cursorPos)
			return
		}
		if m.cursorPos < len(m.prompt) {
			m.cursorPos++
		}
		return
	case tcell.KeyHome, tcell.KeyCtrlA:
		m.cursorPos = 0
		return
	case tcell.KeyEnd, tcell.KeyCtrlE:
		m.cursorPos = len(m.prompt)
		return
	case tcell.KeyCtrlU:
		m.prompt = m.prompt[m.cursorPos:]
		m.cursorPos = 0
		return
	case tcell.KeyCtrlK:
		m.prompt = m.prompt[:m.cursorPos]
		return
	case tcell.KeyUp:
		if !m.moveCursorUp() {
			m.focused = ltFieldProject
		}
		return
	case tcell.KeyDown:
		w := m.promptInnerW()
		lines := m.wrapPrompt(w)
		line, _ := m.cursorWrappedPos(w)
		if line >= len(lines)-1 {
			m.focused = ltFieldProject // wrap around
			return
		}
		m.moveCursorDown()
		return
	case tcell.KeyRune:
		r := event.Rune()
		if hasAlt {
			switch r {
			case 'b', 'B':
				m.cursorPos = wordLeftPos(m.prompt, m.cursorPos)
			case 'f', 'F':
				m.cursorPos = wordRightPos(m.prompt, m.cursorPos)
			case 'd', 'D':
				m.prompt, m.cursorPos = deleteWordRight(m.prompt, m.cursorPos)
			}
			return
		}
		m.prompt = append(m.prompt[:m.cursorPos], append([]rune{r}, m.prompt[m.cursorPos:]...)...)
		m.cursorPos++
		return
	}
}

// wrapPrompt splits prompt runes into visual lines of the given width.
func (m *LaunchToDoModal) wrapPrompt(width int) []wrappedLine {
	if width <= 0 {
		return nil
	}
	if len(m.prompt) == 0 {
		return []wrappedLine{{0, 0}}
	}
	var lines []wrappedLine
	i := 0
	for i < len(m.prompt) {
		remaining := len(m.prompt) - i
		if remaining <= width {
			lines = append(lines, wrappedLine{i, remaining})
			break
		}
		breakAt := -1
		for j := i + width; j > i; j-- {
			if m.prompt[j] == ' ' {
				breakAt = j
				break
			}
		}
		if breakAt <= i {
			lines = append(lines, wrappedLine{i, width})
			i += width
		} else {
			lineLen := breakAt - i + 1
			lines = append(lines, wrappedLine{i, lineLen})
			i = breakAt + 1
		}
	}
	return lines
}

func (m *LaunchToDoModal) cursorWrappedPos(width int) (int, int) {
	if width <= 0 {
		return 0, 0
	}
	lines := m.wrapPrompt(width)
	for i, wl := range lines {
		if m.cursorPos >= wl.start && m.cursorPos < wl.start+wl.length {
			return i, m.cursorPos - wl.start
		}
	}
	if len(lines) > 0 {
		last := lines[len(lines)-1]
		return len(lines) - 1, m.cursorPos - last.start
	}
	return 0, 0
}

func (m *LaunchToDoModal) promptInnerW() int {
	if m.promptWidth > 0 {
		return m.promptWidth
	}
	return 52
}

func (m *LaunchToDoModal) moveCursorUp() bool {
	w := m.promptInnerW()
	lines := m.wrapPrompt(w)
	line, col := m.cursorWrappedPos(w)
	if line == 0 {
		return false
	}
	prevLine := lines[line-1]
	newPos := prevLine.start + col
	if col > prevLine.length-1 {
		newPos = prevLine.start + prevLine.length - 1
		if newPos < prevLine.start {
			newPos = prevLine.start
		}
	}
	if newPos > len(m.prompt) {
		newPos = len(m.prompt)
	}
	m.cursorPos = newPos
	return true
}

func (m *LaunchToDoModal) moveCursorDown() {
	w := m.promptInnerW()
	lines := m.wrapPrompt(w)
	line, col := m.cursorWrappedPos(w)
	if line >= len(lines)-1 {
		return
	}
	nextLine := lines[line+1]
	newPos := nextLine.start + col
	endPos := nextLine.start + nextLine.length
	if newPos > endPos {
		newPos = endPos
	}
	if newPos > len(m.prompt) {
		newPos = len(m.prompt)
	}
	m.cursorPos = newPos
}

func (m *LaunchToDoModal) ensureCursorVisible(totalLines, visibleLines int) {
	if totalLines <= visibleLines {
		m.scrollOffset = 0
		return
	}
	w := m.promptInnerW()
	curLine, _ := m.cursorWrappedPos(w)
	if curLine < m.scrollOffset {
		m.scrollOffset = curLine
	}
	if curLine >= m.scrollOffset+visibleLines {
		m.scrollOffset = curLine - visibleLines + 1
	}
}

// buildToDoPrompt wraps model.BuildToDoPrompt for local use.
func buildToDoPrompt(userPrompt, noteContent string) string {
	return model.BuildToDoPrompt(userPrompt, noteContent)
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
	m.promptWidth = innerW

	// Compute wrapped prompt lines for dynamic height
	wrappedLines := m.wrapPrompt(innerW)
	promptLines := len(wrappedLines)
	visiblePromptLines := promptLines
	if visiblePromptLines > ltMaxPromptLines {
		visiblePromptLines = ltMaxPromptLines
	}
	if visiblePromptLines < 1 {
		visiblePromptLines = 1
	}
	m.ensureCursorVisible(promptLines, visiblePromptLines)

	// Height: border(1) + padding(1) + name(1) + gap(1) + project(2) + gap(1) + promptLabel(1) + prompt(N) + gap(1) + help(1) + padding(1) + border(1)
	// = 12 + visiblePromptLines
	modalH := 12 + visiblePromptLines
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
		name = name[:innerW-1] + "..."
	}
	drawText(screen, innerX, row, innerW, name, StyleNormal.Bold(true))
	row += 2

	// Project selector
	projLabelStyle := StyleDimmed
	if m.focused == ltFieldProject {
		projLabelStyle = StyleTitle
	}
	drawText(screen, innerX, row, innerW, "Project:", projLabelStyle)
	row++

	if len(m.projectNames) == 0 {
		drawText(screen, innerX, row, innerW, "(no projects configured)", StyleDimmed)
	} else {
		pname := m.projectNames[m.projectIdx]
		selector := "◀ " + pname + " ▶"
		selectorStyle := StyleNormal
		if m.focused == ltFieldProject {
			selectorStyle = StyleSelected
		}
		drawText(screen, innerX, row, innerW, selector, selectorStyle)

		posText := "(" + itoa(m.projectIdx+1) + "/" + itoa(len(m.projectNames)) + ")"
		posX := innerX + innerW - len(posText)
		if posX > innerX+len(selector)+1 {
			drawText(screen, posX, row, len(posText), posText, StyleDimmed)
		}
	}
	row += 2

	// Prompt field
	promptLabelStyle := StyleDimmed
	if m.focused == ltFieldPrompt {
		promptLabelStyle = StyleTitle
	}
	drawText(screen, innerX, row, innerW, "Prompt:", promptLabelStyle)
	row++

	curLine, curCol := m.cursorWrappedPos(innerW)
	inputBG := tcell.Color236
	inputStyle := tcell.StyleDefault.Foreground(ColorNormal).Background(inputBG)
	inputEmptyStyle := tcell.StyleDefault.Background(inputBG)
	cursorStyle := tcell.StyleDefault.Foreground(tcell.ColorBlack).Background(tcell.Color252)

	if m.focused == ltFieldPrompt {
		for vi := 0; vi < visiblePromptLines; vi++ {
			li := vi + m.scrollOffset
			if li >= len(wrappedLines) {
				for col := 0; col < innerW; col++ {
					screen.SetContent(innerX+col, row+vi, ' ', nil, inputEmptyStyle)
				}
				continue
			}
			start := wrappedLines[li].start
			length := wrappedLines[li].length
			for col := 0; col < innerW; col++ {
				var ch rune
				var st tcell.Style
				if col < length {
					ch = m.prompt[start+col]
					st = inputStyle
				} else {
					ch = ' '
					st = inputEmptyStyle
				}
				if li == curLine && col == curCol {
					st = cursorStyle
				}
				screen.SetContent(innerX+col, row+vi, ch, nil, st)
			}
		}
	} else {
		if len(m.prompt) == 0 {
			placeholderStyle := tcell.StyleDefault.Foreground(ColorDimmed).Background(inputBG)
			placeholder := "Additional instructions (optional)"
			pRunes := []rune(placeholder)
			for col := 0; col < innerW; col++ {
				if col < len(pRunes) {
					screen.SetContent(innerX+col, row, pRunes[col], nil, placeholderStyle)
				} else {
					screen.SetContent(innerX+col, row, ' ', nil, inputEmptyStyle)
				}
			}
		} else {
			modalBG := tcell.ColorDefault
			unfocusedStyle := tcell.StyleDefault.Foreground(ColorNormal).Background(modalBG)
			for vi := 0; vi < visiblePromptLines; vi++ {
				li := vi + m.scrollOffset
				if li >= len(wrappedLines) {
					break
				}
				start := wrappedLines[li].start
				length := wrappedLines[li].length
				lineStr := string(m.prompt[start : start+length])
				drawText(screen, innerX, row+vi, innerW, lineStr, unfocusedStyle)
			}
		}
	}
	row += visiblePromptLines

	// Error
	if m.errMsg != "" {
		row++
		drawText(screen, innerX, row, innerW, m.errMsg, StyleError)
		row++
	}

	row++ // gap

	// Help
	help := "Enter launch  Tab switch  Esc cancel"
	drawText(screen, innerX, row, innerW, help, StyleDimmed)
}
