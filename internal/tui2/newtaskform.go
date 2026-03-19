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

// NewTaskForm is a modal form for creating new tasks in the tcell runtime.
type NewTaskForm struct {
	*tview.Box
	projectNames []string
	projectIdx   int
	backendNames []string
	backendIdx   int
	prompt       []rune // raw prompt text
	cursorPos    int    // cursor position in prompt runes
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

	return &model.Task{
		Name:    strings.TrimSpace(string(f.prompt)),
		Status:  model.StatusPending,
		Project: proj,
		Branch:  branch,
		Prompt:  strings.TrimSpace(string(f.prompt)),
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
		f.focused = ntFieldBackend
	case tcell.KeyRune:
		r := event.Rune()
		f.prompt = append(f.prompt[:f.cursorPos], append([]rune{r}, f.prompt[f.cursorPos:]...)...)
		f.cursorPos++
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
	modalH := 12
	if f.errMsg != "" {
		modalH += 2
	}
	if modalW < 20 || modalH > sh {
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
	innerW := modalW - 4
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

	// Prompt input
	promptStr := string(f.prompt)
	inputStyle := StyleNormal
	if f.focused == ntFieldPrompt {
		// Show cursor
		for i := 0; i < innerW; i++ {
			var ch rune
			var st tcell.Style
			if i < len(f.prompt) {
				ch = f.prompt[i]
				st = inputStyle
			} else {
				ch = ' '
				st = tcell.StyleDefault
			}
			if i == f.cursorPos && f.focused == ntFieldPrompt {
				st = tcell.StyleDefault.Foreground(tcell.Color(17)).Background(tcell.Color(153))
			}
			screen.SetContent(innerX+i, row, ch, nil, st)
		}
	} else {
		if len(promptStr) == 0 {
			drawText(screen, innerX, row, innerW, "Prompt for the agent", StyleDimmed)
		} else {
			drawText(screen, innerX, row, innerW, promptStr, inputStyle)
		}
	}
	row += 2

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
	posText := strings.Repeat(" ", 1) + "(" + strings.Repeat(" ", 0)
	posText = "(" + itoa(idx+1) + "/" + itoa(len(names)) + ")"
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
