package tui2

import (
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/skills"
)

const (
	ntFieldProject = 0
	ntFieldBackend = 1
	ntFieldPrompt  = 2
)

// maxPromptLines is the maximum visible lines for the prompt textarea.
const maxPromptLines = 10

// acMaxVisible is the maximum number of autocomplete items shown at once.
const acMaxVisible = 6

// NewTaskForm is a modal form for creating new tasks in the tcell runtime.
type NewTaskForm struct {
	*tview.Box
	projectNames []string
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

	// project typeahead state
	projInput     []rune   // typed text for project filter
	projCursorPos int      // cursor position in project input
	projACOpen    bool     // whether dropdown is showing
	projACMatches []string // filtered project names
	projACIdx     int      // selected item in dropdown
	projACScroll  int      // scroll offset in dropdown

	// autocomplete state
	skills    []skills.SkillItem
	acOpen    bool
	acMatches []skills.SkillItem
	acIdx     int
	acScroll  int
}

// NewNewTaskForm creates a new task form with sorted project and backend lists.
func NewNewTaskForm(projects map[string]config.Project, defaultProject string, backends map[string]config.Backend, defaultBackend string) *NewTaskForm {
	// Build sorted project names
	projNames := make([]string, 0, len(projects))
	for name := range projects {
		projNames = append(projNames, name)
	}
	sort.Strings(projNames)

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

	f := &NewTaskForm{
		Box:          tview.NewBox(),
		projectNames: projNames,
		projInput:    []rune(defaultProject),
		projCursorPos: len([]rune(defaultProject)),
		backendNames: backNames,
		backendIdx:   backIdx,
		focused:      ntFieldPrompt,
		projects:     projects,
		backends:     backends,
	}

	// Load skills for the default project
	f.loadSkills()
	return f
}

// Done returns true if the form was submitted.
func (f *NewTaskForm) Done() bool { return f.done }

// Canceled returns true if the form was canceled.
func (f *NewTaskForm) Canceled() bool { return f.canceled }

// resolveProject returns the project name if it exactly matches a known project.
func (f *NewTaskForm) resolveProject() string {
	input := string(f.projInput)
	if _, ok := f.projects[input]; ok {
		return input
	}
	// Case-insensitive fallback
	lower := strings.ToLower(input)
	for _, name := range f.projectNames {
		if strings.ToLower(name) == lower {
			return name
		}
	}
	return ""
}

// Task returns the task from the current form state.
func (f *NewTaskForm) Task() *model.Task {
	proj := f.resolveProject()
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
	return f.resolveProject()
}

// SetError sets an error message to display on the form and resets the
// done flag so the form remains open for the user to retry.
func (f *NewTaskForm) SetError(msg string) {
	f.errMsg = msg
	f.done = false
}

// selectedProjectPath returns the filesystem path of the currently selected project.
func (f *NewTaskForm) selectedProjectPath() string {
	proj := f.resolveProject()
	if proj == "" {
		return ""
	}
	if p, ok := f.projects[proj]; ok {
		return p.Path
	}
	return ""
}

// acTrigger returns the autocomplete trigger character for the selected backend:
// "$" for codex backends, "/" for all others.
func (f *NewTaskForm) acTrigger() string {
	if len(f.backendNames) > 0 && f.backendIdx < len(f.backendNames) {
		if b, ok := f.backends[f.backendNames[f.backendIdx]]; ok && agent.IsCodexBackend(b.Command) {
			return "$"
		}
	}
	return "/"
}

// updateProjectAC recomputes the project autocomplete matches based on the current input.
func (f *NewTaskForm) updateProjectAC() {
	input := strings.ToLower(string(f.projInput))
	f.projACMatches = nil
	for _, name := range f.projectNames {
		if input == "" || strings.Contains(strings.ToLower(name), input) {
			f.projACMatches = append(f.projACMatches, name)
		}
	}
	f.projACOpen = len(f.projACMatches) > 0
	if f.projACIdx >= len(f.projACMatches) {
		f.projACIdx = 0
		f.projACScroll = 0
	}
}

// projACMoveDown moves the project autocomplete cursor down one item (wraps).
func (f *NewTaskForm) projACMoveDown() {
	if len(f.projACMatches) == 0 {
		return
	}
	f.projACIdx = (f.projACIdx + 1) % len(f.projACMatches)
	if f.projACIdx == 0 {
		f.projACScroll = 0
	} else if f.projACIdx >= f.projACScroll+acMaxVisible {
		f.projACScroll = f.projACIdx - acMaxVisible + 1
	}
}

// projACMoveUp moves the project autocomplete cursor up one item (wraps).
func (f *NewTaskForm) projACMoveUp() {
	if len(f.projACMatches) == 0 {
		return
	}
	if f.projACIdx == 0 {
		f.projACIdx = len(f.projACMatches) - 1
		if f.projACIdx >= acMaxVisible {
			f.projACScroll = f.projACIdx - acMaxVisible + 1
		}
	} else {
		f.projACIdx--
		if f.projACIdx < f.projACScroll {
			f.projACScroll = f.projACIdx
		}
	}
}

// projACAccept selects the current autocomplete match and closes the dropdown.
func (f *NewTaskForm) projACAccept() {
	if len(f.projACMatches) == 0 {
		return
	}
	name := f.projACMatches[f.projACIdx]
	f.projInput = []rune(name)
	f.projCursorPos = len(f.projInput)
	f.projACOpen = false
	f.loadSkills()
}

// loadSkills scans skill directories for the currently selected project.
func (f *NewTaskForm) loadSkills() {
	var extraDirs []string
	if pp := f.selectedProjectPath(); pp != "" {
		extraDirs = []string{filepath.Join(pp, ".claude", "skills")}
	}
	f.skills = skills.LoadSkills(extraDirs)
}

// updateAutocomplete recomputes the autocomplete matches based on the current
// prompt value. Autocomplete is active when the value starts with the trigger
// character ("/" for claude, "$" for codex) and contains no spaces.
func (f *NewTaskForm) updateAutocomplete() {
	val := string(f.prompt)
	trigger := f.acTrigger()
	if !strings.HasPrefix(val, trigger) || (len(val) > 1 && strings.ContainsRune(val[1:], ' ')) {
		f.acOpen = false
		return
	}
	filter := ""
	if len(val) > 1 {
		filter = val[1:]
	}
	f.acMatches = skills.FilterSkills(f.skills, filter)
	if len(f.acMatches) == 0 {
		f.acOpen = false
		return
	}
	f.acOpen = true
	if f.acIdx >= len(f.acMatches) {
		f.acIdx = 0
		f.acScroll = 0
	}
}

// acMoveDown moves the autocomplete cursor down one item (wraps around).
func (f *NewTaskForm) acMoveDown() {
	if len(f.acMatches) == 0 {
		return
	}
	f.acIdx = (f.acIdx + 1) % len(f.acMatches)
	if f.acIdx == 0 {
		f.acScroll = 0
	} else if f.acIdx >= f.acScroll+acMaxVisible {
		f.acScroll = f.acIdx - acMaxVisible + 1
	}
}

// acMoveUp moves the autocomplete cursor up one item (wraps around).
func (f *NewTaskForm) acMoveUp() {
	if len(f.acMatches) == 0 {
		return
	}
	if f.acIdx == 0 {
		f.acIdx = len(f.acMatches) - 1
		if f.acIdx >= acMaxVisible {
			f.acScroll = f.acIdx - acMaxVisible + 1
		}
	} else {
		f.acIdx--
		if f.acIdx < f.acScroll {
			f.acScroll = f.acIdx
		}
	}
}

// PasteHandler handles bracketed paste events, inserting the entire pasted
// text at the cursor position in a single operation instead of per-character.
func (f *NewTaskForm) PasteHandler() func(pastedText string, setFocus func(p tview.Primitive)) {
	return f.WrapPasteHandler(func(pastedText string, setFocus func(p tview.Primitive)) {
		f.errMsg = ""
		runes := []rune(pastedText)
		if len(runes) == 0 {
			return
		}
		switch f.focused {
		case ntFieldProject:
			newInput := make([]rune, 0, len(f.projInput)+len(runes))
			newInput = append(newInput, f.projInput[:f.projCursorPos]...)
			newInput = append(newInput, runes...)
			newInput = append(newInput, f.projInput[f.projCursorPos:]...)
			f.projInput = newInput
			f.projCursorPos += len(runes)
			f.updateProjectAC()
		case ntFieldPrompt:
			newPrompt := make([]rune, 0, len(f.prompt)+len(runes))
			newPrompt = append(newPrompt, f.prompt[:f.cursorPos]...)
			newPrompt = append(newPrompt, runes...)
			newPrompt = append(newPrompt, f.prompt[f.cursorPos:]...)
			f.prompt = newPrompt
			f.cursorPos += len(runes)
			f.updateAutocomplete()
			// ntFieldBackend: backend selector ignores paste
		}
	})
}

// InputHandler handles key events for the form.
func (f *NewTaskForm) InputHandler() func(event *tcell.EventKey, setFocus func(p tview.Primitive)) {
	return f.WrapInputHandler(func(event *tcell.EventKey, setFocus func(p tview.Primitive)) {
		// Clear error on any keypress
		f.errMsg = ""

		// Global form keys
		switch event.Key() {
		case tcell.KeyEscape, tcell.KeyCtrlQ:
			if f.acOpen || f.projACOpen { // two-step: first press closes autocomplete, second cancels form
				f.acOpen = false
				f.projACOpen = false
				return
			}
			f.canceled = true
			return
		case tcell.KeyTab:
			f.acOpen = false
			f.projACOpen = false
			f.focused = (f.focused + 1) % 3
			return
		case tcell.KeyBacktab:
			f.acOpen = false
			f.projACOpen = false
			f.focused = (f.focused + 2) % 3
			return
		}

		switch f.focused {
		case ntFieldProject:
			f.handleProjectKey(event)
		case ntFieldBackend:
			f.handleSelectorKey(event, &f.backendIdx, len(f.backendNames))
		case ntFieldPrompt:
			f.handlePromptKey(event)
		}
	})
}

// handleProjectKey handles key events when the project typeahead field is focused.
func (f *NewTaskForm) handleProjectKey(event *tcell.EventKey) {
	mod := event.Modifiers()
	hasAlt := mod&tcell.ModAlt != 0

	switch event.Key() {
	case tcell.KeyEnter:
		if f.projACOpen && len(f.projACMatches) > 0 {
			f.projACAccept()
			return
		}
		f.projACOpen = false
		f.focused = ntFieldBackend
		return
	case tcell.KeyDown:
		if f.projACOpen {
			f.projACMoveDown()
			return
		}
		f.projACOpen = false
		f.focused = ntFieldBackend
		return
	case tcell.KeyUp:
		if f.projACOpen {
			f.projACMoveUp()
			return
		}
		f.projACOpen = false
		f.focused = ntFieldPrompt
		return
	case tcell.KeyBackspace, tcell.KeyBackspace2:
		if hasAlt {
			f.projInput, f.projCursorPos = deleteWordLeft(f.projInput, f.projCursorPos)
			f.updateProjectAC()
			return
		}
		if f.projCursorPos > 0 {
			f.projInput = append(f.projInput[:f.projCursorPos-1], f.projInput[f.projCursorPos:]...)
			f.projCursorPos--
			f.updateProjectAC()
		}
		return
	case tcell.KeyCtrlW:
		f.projInput, f.projCursorPos = deleteWordLeft(f.projInput, f.projCursorPos)
		f.updateProjectAC()
		return
	case tcell.KeyDelete:
		if f.projCursorPos < len(f.projInput) {
			f.projInput = append(f.projInput[:f.projCursorPos], f.projInput[f.projCursorPos+1:]...)
			f.updateProjectAC()
		}
		return
	case tcell.KeyLeft:
		if hasAlt {
			f.projCursorPos = wordLeftPos(f.projInput, f.projCursorPos)
			return
		}
		if f.projCursorPos > 0 {
			f.projCursorPos--
		}
		return
	case tcell.KeyRight:
		if hasAlt {
			f.projCursorPos = wordRightPos(f.projInput, f.projCursorPos)
			return
		}
		if f.projCursorPos < len(f.projInput) {
			f.projCursorPos++
		}
		return
	case tcell.KeyHome, tcell.KeyCtrlA:
		f.projCursorPos = 0
		return
	case tcell.KeyEnd, tcell.KeyCtrlE:
		f.projCursorPos = len(f.projInput)
		return
	case tcell.KeyCtrlU:
		f.projInput = f.projInput[f.projCursorPos:]
		f.projCursorPos = 0
		f.updateProjectAC()
		return
	case tcell.KeyCtrlK:
		f.projInput = f.projInput[:f.projCursorPos]
		f.updateProjectAC()
		return
	case tcell.KeyRune:
		r := event.Rune()
		if hasAlt {
			switch r {
			case 'b', 'B':
				f.projCursorPos = wordLeftPos(f.projInput, f.projCursorPos)
			case 'f', 'F':
				f.projCursorPos = wordRightPos(f.projInput, f.projCursorPos)
			case 'd', 'D':
				f.projInput, f.projCursorPos = deleteWordRight(f.projInput, f.projCursorPos)
				f.updateProjectAC()
			}
			return
		}
		f.projInput = append(f.projInput[:f.projCursorPos], append([]rune{r}, f.projInput[f.projCursorPos:]...)...)
		f.projCursorPos++
		f.updateProjectAC()
		return
	}
}

func (f *NewTaskForm) handleSelectorKey(event *tcell.EventKey, idx *int, count int) {
	if count == 0 {
		return
	}
	switch event.Key() {
	case tcell.KeyLeft:
		*idx = (*idx - 1 + count) % count
		if idx == &f.backendIdx {
			f.updateAutocomplete()
		}
	case tcell.KeyRight:
		*idx = (*idx + 1) % count
		if idx == &f.backendIdx {
			f.updateAutocomplete()
		}
	case tcell.KeyDown, tcell.KeyEnter:
		f.focused++
		if f.focused > ntFieldPrompt {
			f.focused = ntFieldPrompt
		}
	case tcell.KeyUp:
		if f.focused > ntFieldProject {
			f.focused--
		}
	}
}

func (f *NewTaskForm) handlePromptKey(event *tcell.EventKey) {
	mod := event.Modifiers()
	hasAlt := mod&tcell.ModAlt != 0

	switch event.Key() {
	case tcell.KeyEnter:
		// Select autocomplete suggestion if open
		if f.acOpen && len(f.acMatches) > 0 {
			text := f.acTrigger() + f.acMatches[f.acIdx].Name + " "
			f.prompt = []rune(text)
			f.cursorPos = len(f.prompt)
			f.acOpen = false
			return
		}
		if len(f.prompt) > 0 {
			f.done = true
		}
		return
	case tcell.KeyBackspace, tcell.KeyBackspace2:
		if hasAlt {
			// Alt+Backspace: delete word left
			f.prompt, f.cursorPos = deleteWordLeft(f.prompt, f.cursorPos)
			f.updateAutocomplete()
			return
		}
		if f.cursorPos > 0 {
			f.prompt = append(f.prompt[:f.cursorPos-1], f.prompt[f.cursorPos:]...)
			f.cursorPos--
			f.updateAutocomplete()
		}
		return
	case tcell.KeyCtrlW:
		// Ctrl+W: delete word left
		f.prompt, f.cursorPos = deleteWordLeft(f.prompt, f.cursorPos)
		f.updateAutocomplete()
		return
	case tcell.KeyDelete:
		if hasAlt {
			// Alt+Delete: delete word right
			f.prompt, f.cursorPos = deleteWordRight(f.prompt, f.cursorPos)
			f.updateAutocomplete()
			return
		}
		if f.cursorPos < len(f.prompt) {
			f.prompt = append(f.prompt[:f.cursorPos], f.prompt[f.cursorPos+1:]...)
			f.updateAutocomplete()
		}
		return
	case tcell.KeyLeft:
		if hasAlt {
			// Alt+Left: jump word left
			f.cursorPos = wordLeftPos(f.prompt, f.cursorPos)
			return
		}
		if f.cursorPos > 0 {
			f.cursorPos--
		}
		return
	case tcell.KeyRight:
		if hasAlt {
			// Alt+Right: jump word right
			f.cursorPos = wordRightPos(f.prompt, f.cursorPos)
			return
		}
		if f.cursorPos < len(f.prompt) {
			f.cursorPos++
		}
		return
	case tcell.KeyHome, tcell.KeyCtrlA:
		f.cursorPos = 0
		return
	case tcell.KeyEnd, tcell.KeyCtrlE:
		f.cursorPos = len(f.prompt)
		return
	case tcell.KeyCtrlU:
		f.prompt = f.prompt[f.cursorPos:]
		f.cursorPos = 0
		f.updateAutocomplete()
		return
	case tcell.KeyCtrlK:
		f.prompt = f.prompt[:f.cursorPos]
		f.updateAutocomplete()
		return
	case tcell.KeyUp:
		if f.acOpen {
			f.acMoveUp()
			return
		}
		// Move cursor up one wrapped line if possible, otherwise leave prompt field
		if !f.moveCursorUp() {
			f.focused = ntFieldBackend
		}
		return
	case tcell.KeyDown:
		if f.acOpen {
			f.acMoveDown()
			return
		}
		// Move cursor down one wrapped line, or wrap to project if on last line
		w := f.promptInnerW()
		lines := f.wrapPrompt(w)
		line, _ := f.cursorWrappedPos(w)
		if line >= len(lines)-1 {
			// On last line — wrap to project (circular navigation)
			f.focused = ntFieldProject
			return
		}
		f.moveCursorDown()
		return
	case tcell.KeyRune:
		r := event.Rune()
		// Alt+B: jump word left, Alt+F: jump word right, Alt+D: delete word right
		if hasAlt {
			switch r {
			case 'b', 'B':
				f.cursorPos = wordLeftPos(f.prompt, f.cursorPos)
				return
			case 'f', 'F':
				f.cursorPos = wordRightPos(f.prompt, f.cursorPos)
				return
			case 'd', 'D':
				f.prompt, f.cursorPos = deleteWordRight(f.prompt, f.cursorPos)
				f.updateAutocomplete()
				return
			}
			return // ignore other alt+rune combos
		}
		f.prompt = append(f.prompt[:f.cursorPos], append([]rune{r}, f.prompt[f.cursorPos:]...)...)
		f.cursorPos++
		f.updateAutocomplete()
		return
	}
}

// wrappedLine represents a visual line segment within the prompt rune slice.
type wrappedLine struct {
	start  int // index into f.prompt where this line begins
	length int // number of runes on this line
}

// wrapPrompt splits the prompt runes into visual lines of the given width,
// breaking at word boundaries when possible. A "word boundary" is a space
// character. If a single word exceeds the width, it is hard-broken.
func (f *NewTaskForm) wrapPrompt(width int) []wrappedLine {
	if width <= 0 {
		return nil
	}
	if len(f.prompt) == 0 {
		return []wrappedLine{{0, 0}}
	}
	var lines []wrappedLine
	i := 0
	for i < len(f.prompt) {
		remaining := len(f.prompt) - i
		if remaining <= width {
			// Rest fits on one line
			lines = append(lines, wrappedLine{i, remaining})
			break
		}
		// Find last space within the width to break at
		// (i+width is safe: the remaining <= width guard above handles the boundary case)
		breakAt := -1
		for j := i + width; j > i; j-- {
			if f.prompt[j] == ' ' {
				breakAt = j
				break
			}
		}
		if breakAt <= i {
			// No space found — hard break at width
			lines = append(lines, wrappedLine{i, width})
			i += width
		} else {
			// Break at the space; include the space on this line
			lineLen := breakAt - i + 1
			lines = append(lines, wrappedLine{i, lineLen})
			i = breakAt + 1
		}
	}
	return lines
}

// cursorWrappedPos returns (line index, column) of the cursor within wrapped lines.
func (f *NewTaskForm) cursorWrappedPos(width int) (int, int) {
	if width <= 0 {
		return 0, 0
	}
	lines := f.wrapPrompt(width)
	for i, wl := range lines {
		if f.cursorPos >= wl.start && f.cursorPos < wl.start+wl.length {
			return i, f.cursorPos - wl.start
		}
	}
	// Cursor is at the end of the last line
	if len(lines) > 0 {
		last := lines[len(lines)-1]
		return len(lines) - 1, f.cursorPos - last.start
	}
	return 0, 0
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
	lines := f.wrapPrompt(w)
	line, col := f.cursorWrappedPos(w)
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
	nextLine := lines[line+1]
	newPos := nextLine.start + col
	endPos := nextLine.start + nextLine.length
	if newPos > endPos {
		newPos = endPos
	}
	if newPos > len(f.prompt) {
		newPos = len(f.prompt)
	}
	f.cursorPos = newPos
}

// ensureCursorVisible adjusts scrollOffset so the cursor line is visible.
func (f *NewTaskForm) ensureCursorVisible(totalLines, visibleLines int) {
	// If all content fits in the visible area, never scroll
	if totalLines <= visibleLines {
		f.scrollOffset = 0
		return
	}
	w := f.promptInnerW()
	curLine, _ := f.cursorWrappedPos(w)
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
	f.ensureCursorVisible(promptLines, visiblePromptLines)

	// Autocomplete row count for modal height
	acRows := 0
	if f.acOpen && len(f.acMatches) > 0 {
		acRows = len(f.acMatches)
		if acRows > acMaxVisible {
			acRows = acMaxVisible + 1 // extra row for scroll indicator
		}
	}

	// Project autocomplete row count
	projACRows := 0
	if f.projACOpen && len(f.projACMatches) > 0 {
		projACRows = len(f.projACMatches)
		if projACRows > acMaxVisible {
			projACRows = acMaxVisible + 1
		}
	}

	// Modal height: border(1) + padding(1) + project(2) + projAC(P) + backend(2) + label(1) + prompt(N) + ac(M) + gap(1) + help(1) + padding(1) + border(1)
	modalH := 11 + visiblePromptLines + acRows + projACRows
	if f.errMsg != "" {
		modalH += 2
	}
	if modalH > sh {
		return
	}

	mx := sx + (sw-modalW)/2
	my := sy + (sh-modalH)/2

	// Clear modal area
	modalBG := tcell.ColorDefault
	clearStyle := tcell.StyleDefault.Background(modalBG)
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
	titleStyle := tcell.StyleDefault.Foreground(ColorTitle).Bold(true).Background(modalBG)
	for i, r := range title {
		screen.SetContent(titleX+i, my, r, nil, titleStyle)
	}

	innerX := mx + 2
	row := my + 2

	// Project typeahead
	f.drawProjectField(screen, innerX, row, innerW)
	row += 2
	if projACRows > 0 {
		f.drawProjectAC(screen, innerX, row, innerW)
		row += projACRows
	}

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
	inputStyle := tcell.StyleDefault.Foreground(ColorNormal).Background(modalBG)
	inputEmptyStyle := tcell.StyleDefault.Background(modalBG)
	cursorStyle := tcell.StyleDefault.Foreground(tcell.ColorBlack).Background(tcell.Color252)

	if f.focused == ntFieldPrompt {
		for vi := 0; vi < visiblePromptLines; vi++ {
			li := vi + f.scrollOffset
			if li >= len(wrappedLines) {
				// Empty line below content
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
					ch = f.prompt[start+col]
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
		if len(f.prompt) == 0 {
			// Placeholder text with input background
			placeholderStyle := tcell.StyleDefault.Foreground(ColorDimmed).Background(modalBG)
			placeholder := "Prompt for the agent"
			pRunes := []rune(placeholder)
			for col := 0; col < innerW; col++ {
				if col < len(pRunes) {
					screen.SetContent(innerX+col, row, pRunes[col], nil, placeholderStyle)
				} else {
					screen.SetContent(innerX+col, row, ' ', nil, inputEmptyStyle)
				}
			}
		} else {
			// Render wrapped lines when unfocused too
			unfocusedStyle := tcell.StyleDefault.Foreground(ColorNormal).Background(modalBG)
			for vi := 0; vi < visiblePromptLines; vi++ {
				li := vi + f.scrollOffset
				if li >= len(wrappedLines) {
					break
				}
				start := wrappedLines[li].start
				length := wrappedLines[li].length
				lineStr := string(f.prompt[start : start+length])
				drawText(screen, innerX, row+vi, innerW, lineStr, unfocusedStyle)
			}
		}
	}
	row += visiblePromptLines

	// Autocomplete dropdown
	if f.acOpen && len(f.acMatches) > 0 {
		f.drawAutocomplete(screen, innerX, row, innerW)
		row += acRows
	}

	row++ // gap

	// Error message
	if f.errMsg != "" {
		drawText(screen, innerX, row, innerW, f.errMsg, StyleError)
		row++
	}

	// Help text
	help := "Enter submit  Tab next  Esc cancel"
	drawText(screen, innerX, row, innerW, help, StyleDimmed)
}

// drawAutocomplete renders the skill suggestion dropdown.
func (f *NewTaskForm) drawAutocomplete(screen tcell.Screen, x, y, w int) {
	end := f.acScroll + acMaxVisible
	if end > len(f.acMatches) {
		end = len(f.acMatches)
	}
	trigger := f.acTrigger()

	// Compute longest skill name for alignment
	maxName := 0
	for i := f.acScroll; i < end; i++ {
		n := utf8.RuneCountInString(trigger + f.acMatches[i].Name)
		if n > maxName {
			maxName = n
		}
	}

	selectedStyle := tcell.StyleDefault.Bold(true).Foreground(tcell.Color(87))

	for vi, i := 0, f.acScroll; i < end; vi, i = vi+1, i+1 {
		skill := f.acMatches[i]
		isSelected := i == f.acIdx

		indicator := "  "
		if isSelected {
			indicator = "> "
		}

		nameStr := trigger + skill.Name
		plainNameW := utf8.RuneCountInString(nameStr)
		padding := maxName - plainNameW + 2
		if padding < 1 {
			padding = 1
		}

		// Truncate description to fit
		descW := w - utf8.RuneCountInString(indicator) - maxName - 2
		desc := skill.Description
		if descW <= 0 {
			desc = ""
		} else {
			runes := []rune(desc)
			if len(runes) > descW {
				desc = string(runes[:descW-1]) + "…"
			}
		}

		line := indicator + nameStr + strings.Repeat(" ", padding) + desc
		lineRunes := []rune(line)
		for col := 0; col < w && col < len(lineRunes); col++ {
			st := StyleDimmed
			if isSelected {
				st = selectedStyle
			}
			screen.SetContent(x+col, y+vi, lineRunes[col], nil, st)
		}
	}

	// Scroll indicator
	if len(f.acMatches) > acMaxVisible {
		countStr := "  (" + itoa(f.acIdx+1) + "/" + itoa(len(f.acMatches)) + ")"
		drawText(screen, x, y+end-f.acScroll, w, countStr, StyleDimmed)
	}
}

// drawProjectField renders the project typeahead input field.
func (f *NewTaskForm) drawProjectField(screen tcell.Screen, x, y, w int) {
	focused := f.focused == ntFieldProject
	modalBG := tcell.ColorDefault

	labelStyle := StyleDimmed
	if focused {
		labelStyle = StyleTitle
	}
	label := "Project:"
	labelW := utf8.RuneCountInString(label)
	drawText(screen, x, y, w, label, labelStyle)

	inputX := x + labelW + 1
	inputW := w - labelW - 1
	if inputW <= 0 {
		return
	}

	inputRow := y + 1
	inputRunes := f.projInput
	inputEmptyStyle := tcell.StyleDefault.Background(modalBG)
	inputStyle := tcell.StyleDefault.Foreground(ColorNormal).Background(modalBG)
	cursorStyle := tcell.StyleDefault.Foreground(tcell.ColorBlack).Background(tcell.Color252)

	if focused {
		for col := 0; col < inputW; col++ {
			var ch rune
			var st tcell.Style
			if col < len(inputRunes) {
				ch = inputRunes[col]
				st = inputStyle
			} else {
				ch = ' '
				st = inputEmptyStyle
			}
			if col == f.projCursorPos {
				st = cursorStyle
			}
			screen.SetContent(inputX+col, inputRow, ch, nil, st)
		}
	} else {
		if len(inputRunes) == 0 {
			placeholderStyle := tcell.StyleDefault.Foreground(ColorDimmed).Background(modalBG)
			placeholder := "Type to search..."
			pRunes := []rune(placeholder)
			for col := 0; col < inputW; col++ {
				if col < len(pRunes) {
					screen.SetContent(inputX+col, inputRow, pRunes[col], nil, placeholderStyle)
				} else {
					screen.SetContent(inputX+col, inputRow, ' ', nil, inputEmptyStyle)
				}
			}
		} else {
			unfocusedStyle := tcell.StyleDefault.Foreground(ColorNormal).Background(modalBG)
			drawText(screen, inputX, inputRow, inputW, string(inputRunes), unfocusedStyle)
		}
	}
}

// drawProjectAC renders the project autocomplete dropdown.
func (f *NewTaskForm) drawProjectAC(screen tcell.Screen, x, y, w int) {
	end := f.projACScroll + acMaxVisible
	if end > len(f.projACMatches) {
		end = len(f.projACMatches)
	}

	selectedStyle := tcell.StyleDefault.Bold(true).Foreground(tcell.Color(87))

	for vi, i := 0, f.projACScroll; i < end; vi, i = vi+1, i+1 {
		name := f.projACMatches[i]
		isSelected := i == f.projACIdx

		indicator := "  "
		if isSelected {
			indicator = "> "
		}

		line := indicator + name
		lineRunes := []rune(line)
		for col := 0; col < w && col < len(lineRunes); col++ {
			st := StyleDimmed
			if isSelected {
				st = selectedStyle
			}
			screen.SetContent(x+col, y+vi, lineRunes[col], nil, st)
		}
	}

	// Scroll indicator
	if len(f.projACMatches) > acMaxVisible {
		countStr := "  (" + itoa(f.projACIdx+1) + "/" + itoa(len(f.projACMatches)) + ")"
		drawText(screen, x, y+end-f.projACScroll, w, countStr, StyleDimmed)
	}
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
