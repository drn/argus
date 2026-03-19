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
	ntFieldProject = 0
	ntFieldBackend = 1
	ntFieldPrompt  = 2
)

// maxPromptLines is the maximum visible lines for the prompt textarea.
const maxPromptLines = 10

// NewTaskForm is a modal form for creating new tasks in the tcell runtime.
type NewTaskForm struct {
	*tview.Box
	projectNames []string
	projectIdx   int
	backendNames []string
	backendIdx   int
	prompt       []rune // raw prompt text
	cursorPos    int    // cursor position in prompt runes
	scrollOffset int    // first visible wrapped line (for scrolling)
	promptWidth  int    // cached inner width from last Draw, used by cursor movement
	focused      int    // 0=project, 1=backend, 2=prompt
	done         bool
	canceled     bool
	errMsg       string

	projects map[string]config.Project
	backends map[string]config.Backend
}

// NewNewTaskForm creates a new task form with sorted project and backend lists.
func NewNewTaskForm(projects map[string]config.Project, defaultProject string, backends map[string]config.Backend, defaultBackend string) *NewTaskForm {
	// Build sorted project names
	projNames := make([]string, 0, len(projects))
	for name := range projects {
		projNames = append(projNames, name)
	}
	sort.Strings(projNames)

	projIdx := 0
	for i, n := range projNames {
		if n == defaultProject {
			projIdx = i
			break
		}
	}

	// Build sorted backend names
	backNames := make([]string, 0, len(backends))
	for name := range backends {
		backNames = append(backNames, name)
	}
	sort.Strings(backNames)

	backIdx := 0
	for i, n := range backNames {
		if n == defaultBackend {
			backIdx = i
			break
		}
	}

	return &NewTaskForm{
		Box:          tview.NewBox(),
		projectNames: projNames,
		projectIdx:   projIdx,
		backendNames: backNames,
		backendIdx:   backIdx,
		focused:      ntFieldPrompt,
		projects:     projects,
		backends:     backends,
	}
}

// Done returns true if the form was submitted.
func (f *NewTaskForm) Done() bool { return f.done }

// Canceled returns true if the form was canceled.
func (f *NewTaskForm) Canceled() bool { return f.canceled }

// Task returns the task from the current form state.
func (f *NewTaskForm) Task() *model.Task {
	proj := ""
	if f.projectIdx < len(f.projectNames) {
		proj = f.projectNames[f.projectIdx]
	}
	backend := ""
	if f.backendIdx < len(f.backendNames) {
		backend = f.backendNames[f.backendIdx]
	}

	branch := ""
	if proj != "" {
		if p, ok := f.projects[proj]; ok {
			branch = p.Branch
		}
	}

	prompt := strings.TrimSpace(string(f.prompt))
	return &model.Task{
		Name:    model.GenerateNameFromPrompt(prompt),
		Status:  model.StatusPending,
		Project: proj,
		Branch:  branch,
		Prompt:  prompt,
		Backend: backend,
	}
}

// SelectedProject returns the selected project name.
func (f *NewTaskForm) SelectedProject() string {
	if f.projectIdx < len(f.projectNames) {
		return f.projectNames[f.projectIdx]
	}
	return ""
}

// SetError sets an error message to display on the form.
func (f *NewTaskForm) SetError(msg string) {
	f.errMsg = msg
}

// InputHandler handles key events for the form.
func (f *NewTaskForm) InputHandler() func(event *tcell.EventKey, setFocus func(p tview.Primitive)) {
	return f.WrapInputHandler(func(event *tcell.EventKey, setFocus func(p tview.Primitive)) {
		// Global form keys
		switch event.Key() {
		case tcell.KeyEscape:
			f.canceled = true
			return
		case tcell.KeyTab:
			f.focused = (f.focused + 1) % 3
			return
		case tcell.KeyBacktab:
			f.focused = (f.focused + 2) % 3
			return
		}

		switch f.focused {
		case ntFieldProject:
			f.handleSelectorKey(event, &f.projectIdx, len(f.projectNames))
		case ntFieldBackend:
			f.handleSelectorKey(event, &f.backendIdx, len(f.backendNames))
		case ntFieldPrompt:
			f.handlePromptKey(event)
		}
	})
}

func (f *NewTaskForm) handleSelectorKey(event *tcell.EventKey, idx *int, count int) {
	if count == 0 {
		return
	}
	switch event.Key() {
	case tcell.KeyLeft:
		*idx = (*idx - 1 + count) % count
	case tcell.KeyRight:
		*idx = (*idx + 1) % count
	case tcell.KeyDown:
		f.focused++
		if f.focused > ntFieldPrompt {
			f.focused = ntFieldPrompt
		}
	case tcell.KeyUp:
		f.focused--
		if f.focused < ntFieldProject {
			f.focused = ntFieldProject
		}
	}
}

func (f *NewTaskForm) handlePromptKey(event *tcell.EventKey) {
	switch event.Key() {
	case tcell.KeyEnter:
		if len(f.prompt) > 0 {
			f.done = true
		}
	case tcell.KeyBackspace, tcell.KeyBackspace2:
		if f.cursorPos > 0 {
			f.prompt = append(f.prompt[:f.cursorPos-1], f.prompt[f.cursorPos:]...)
			f.cursorPos--
		}
	case tcell.KeyDelete:
		if f.cursorPos < len(f.prompt) {
			f.prompt = append(f.prompt[:f.cursorPos], f.prompt[f.cursorPos+1:]...)
		}
	case tcell.KeyLeft:
		if f.cursorPos > 0 {
			f.cursorPos--
		}
	case tcell.KeyRight:
		if f.cursorPos < len(f.prompt) {
			f.cursorPos++
		}
	case tcell.KeyHome, tcell.KeyCtrlA:
		f.cursorPos = 0
	case tcell.KeyEnd, tcell.KeyCtrlE:
		f.cursorPos = len(f.prompt)
	case tcell.KeyCtrlU:
		f.prompt = f.prompt[f.cursorPos:]
		f.cursorPos = 0
	case tcell.KeyCtrlK:
		f.prompt = f.prompt[:f.cursorPos]
	case tcell.KeyUp:
		// Move cursor up one wrapped line if possible, otherwise leave prompt field
		if !f.moveCursorUp() {
			f.focused = ntFieldBackend
		}
	case tcell.KeyDown:
		// Move cursor down one wrapped line (no-op if on last line)
		f.moveCursorDown()
	case tcell.KeyRune:
		r := event.Rune()
		f.prompt = append(f.prompt[:f.cursorPos], append([]rune{r}, f.prompt[f.cursorPos:]...)...)
		f.cursorPos++
	}
}

// wrappedLine represents a visual line segment within the prompt rune slice.
type wrappedLine struct {
	start  int // index into f.prompt where this line begins
	length int // number of runes on this line
}

// wrapPrompt splits the prompt runes into visual lines of the given width.
func (f *NewTaskForm) wrapPrompt(width int) []wrappedLine {
	if width <= 0 {
		return nil
	}
	if len(f.prompt) == 0 {
		return []wrappedLine{{0, 0}}
	}
	var lines []wrappedLine
	for i := 0; i < len(f.prompt); i += width {
		end := i + width
		if end > len(f.prompt) {
			end = len(f.prompt)
		}
		lines = append(lines, wrappedLine{i, end - i})
	}
	return lines
}

// cursorWrappedPos returns (line index, column) of the cursor within wrapped lines.
func (f *NewTaskForm) cursorWrappedPos(width int) (int, int) {
	if width <= 0 {
		return 0, 0
	}
	line := f.cursorPos / width
	col := f.cursorPos % width
	return line, col
}

// promptInnerW returns the cached prompt width from the last Draw call,
// falling back to a reasonable default (min(60, sw-4) - 4 = 52 at 64+ cols).
func (f *NewTaskForm) promptInnerW() int {
	if f.promptWidth > 0 {
		return f.promptWidth
	}
	return 52
}

// moveCursorUp moves the cursor up one wrapped line. Returns false if already on the first line.
func (f *NewTaskForm) moveCursorUp() bool {
	w := f.promptInnerW()
	line, col := f.cursorWrappedPos(w)
	if line == 0 {
		return false
	}
	newPos := (line-1)*w + col
	if newPos > len(f.prompt) {
		newPos = len(f.prompt)
	}
	f.cursorPos = newPos
	return true
}

// moveCursorDown moves the cursor down one wrapped line.
func (f *NewTaskForm) moveCursorDown() {
	w := f.promptInnerW()
	lines := f.wrapPrompt(w)
	line, col := f.cursorWrappedPos(w)
	if line >= len(lines)-1 {
		return
	}
	newPos := (line+1)*w + col
	if newPos > len(f.prompt) {
		newPos = len(f.prompt)
	}
	f.cursorPos = newPos
}

// ensureCursorVisible adjusts scrollOffset so the cursor line is visible.
func (f *NewTaskForm) ensureCursorVisible(innerW, visibleLines int) {
	curLine, _ := f.cursorWrappedPos(innerW)
	if curLine < f.scrollOffset {
		f.scrollOffset = curLine
	}
	if curLine >= f.scrollOffset+visibleLines {
		f.scrollOffset = curLine - visibleLines + 1
	}
}

// Draw renders the modal form.
func (f *NewTaskForm) Draw(screen tcell.Screen) {
	f.Box.DrawForSubclass(screen, f)
	sx, sy, sw, sh := f.GetInnerRect()
	if sw <= 0 || sh <= 0 {
		return
	}

	// Modal dimensions
	modalW := min(60, sw-4)
	innerW := modalW - 4
	f.promptWidth = innerW // cache for key handlers
	if modalW < 20 {
		return
	}

	// Compute wrapped prompt lines for dynamic height
	wrappedLines := f.wrapPrompt(innerW)
	promptLines := len(wrappedLines)
	visiblePromptLines := promptLines
	if visiblePromptLines > maxPromptLines {
		visiblePromptLines = maxPromptLines
	}
	if visiblePromptLines < 1 {
		visiblePromptLines = 1
	}

	// Ensure cursor is visible within the scroll window
	f.ensureCursorVisible(innerW, visiblePromptLines)

	// Modal height: border(1) + padding(1) + project(2) + backend(2) + label(1) + prompt(N) + gap(1) + help(1) + padding(1) + border(1)
	// = 11 + visiblePromptLines
	modalH := 11 + visiblePromptLines
	if f.errMsg != "" {
		modalH += 2
	}
	if modalH > sh {
		return
	}

	mx := sx + (sw-modalW)/2
	my := sy + (sh-modalH)/2

	// Clear modal area
	clearStyle := tcell.StyleDefault.Background(tcell.Color235)
	for row := my; row < my+modalH; row++ {
		for col := mx; col < mx+modalW; col++ {
			screen.SetContent(col, row, ' ', nil, clearStyle)
		}
	}

	// Border
	drawBorder(screen, mx, my, modalW, modalH, StyleFocusedBorder)

	// Title
	title := " New Task "
	titleX := mx + (modalW-utf8.RuneCountInString(title))/2
	for i, r := range title {
		screen.SetContent(titleX+i, my, r, nil, StyleTitle)
	}

	innerX := mx + 2
	row := my + 2

	// Project selector
	f.drawSelector(screen, innerX, row, innerW, "Project", f.projectNames, f.projectIdx, f.focused == ntFieldProject)
	row += 2

	// Backend selector
	f.drawSelector(screen, innerX, row, innerW, "Backend", f.backendNames, f.backendIdx, f.focused == ntFieldBackend)
	row += 2

	// Prompt field
	labelStyle := StyleDimmed
	if f.focused == ntFieldPrompt {
		labelStyle = StyleTitle
	}
	drawText(screen, innerX, row, innerW, "Prompt:", labelStyle)
	row++

	// Prompt input — wrapped across multiple visual lines
	curLine, curCol := f.cursorWrappedPos(innerW)
	inputStyle := StyleNormal
	cursorStyle := tcell.StyleDefault.Foreground(tcell.Color(17)).Background(tcell.Color(153))

	if f.focused == ntFieldPrompt {
		for vi := 0; vi < visiblePromptLines; vi++ {
			li := vi + f.scrollOffset
			if li >= len(wrappedLines) {
				// Empty line below content — just clear
				for col := 0; col < innerW; col++ {
					screen.SetContent(innerX+col, row+vi, ' ', nil, tcell.StyleDefault)
				}
				continue
			}
			start := wrappedLines[li].start
			length := wrappedLines[li].length
			for col := 0; col < innerW; col++ {
				var ch rune
				var st tcell.Style
				if col < length {
					ch = f.prompt[start+col]
					st = inputStyle
				} else {
					ch = ' '
					st = tcell.StyleDefault
				}
				if li == curLine && col == curCol {
					st = cursorStyle
				}
				screen.SetContent(innerX+col, row+vi, ch, nil, st)
			}
		}
	} else {
		promptStr := string(f.prompt)
		if len(promptStr) == 0 {
			drawText(screen, innerX, row, innerW, "Prompt for the agent", StyleDimmed)
		} else {
			// Render wrapped lines when unfocused too
			for vi := 0; vi < visiblePromptLines; vi++ {
				li := vi + f.scrollOffset
				if li >= len(wrappedLines) {
					break
				}
				start := wrappedLines[li].start
				length := wrappedLines[li].length
				lineStr := string(f.prompt[start : start+length])
				drawText(screen, innerX, row+vi, innerW, lineStr, inputStyle)
			}
		}
	}
	row += visiblePromptLines + 1

	// Error message
	if f.errMsg != "" {
		drawText(screen, innerX, row, innerW, f.errMsg, StyleError)
		row++
	}

	// Help text
	help := "Enter submit  Tab next  Esc cancel"
	drawText(screen, innerX, row, innerW, help, StyleDimmed)
}

func (f *NewTaskForm) drawSelector(screen tcell.Screen, x, y, w int, label string, names []string, idx int, focused bool) {
	labelStyle := StyleDimmed
	if focused {
		labelStyle = StyleTitle
	}
	drawText(screen, x, y, w, label+":", labelStyle)

	if len(names) == 0 {
		drawText(screen, x+len(label)+2, y, w-len(label)-2, "(none)", StyleDimmed)
		return
	}

	name := names[idx]
	selector := "◀ " + name + " ▶"
	selectorStyle := StyleNormal
	if focused {
		selectorStyle = StyleSelected
	}
	drawText(screen, x+len(label)+2, y, w-len(label)-2, selector, selectorStyle)

	// Position indicator
	posText := "(" + itoa(idx+1) + "/" + itoa(len(names)) + ")"
	posX := x + w - len(posText)
	if posX > x+len(label)+2+len(selector)+1 {
		drawText(screen, posX, y, len(posText), posText, StyleDimmed)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	s := ""
	for n > 0 {
		s = string(rune('0'+n%10)) + s
		n /= 10
	}
	return s
}
