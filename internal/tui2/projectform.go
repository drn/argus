package tui2

import (
	"strings"
	"unicode/utf8"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/drn/argus/internal/config"
)

// pfMaxACVisible is the maximum number of autocomplete rows shown at once.
const pfMaxACVisible = 8

const (
	pfFieldName    = 0
	pfFieldPath    = 1
	pfFieldBranch  = 2
	pfFieldBackend = 3
	pfFieldSandbox = 4
	pfFieldCount   = 5
)

// Sandbox selector indices — must match sandboxOptions order.
const (
	sandboxInherit  = 0
	sandboxEnabled  = 1
	sandboxDisabled = 2
)

// sandboxOptions are the display labels for the per-project sandbox selector.
var sandboxOptions = []string{"Inherit", "Enabled", "Disabled"}

// ProjectForm is a modal form for adding/editing projects.
type ProjectForm struct {
	*tview.Box
	fields   [pfFieldCount][]rune // name, path, branch (fallback text), backend
	cursors  [pfFieldCount]int
	focused  int
	editMode bool // true = editing (name read-only)
	done     bool
	canceled bool
	errMsg   string

	// Branch selector state
	branchOptions []string // populated via SetBranchOptions
	branchIdx     int
	branchPath    string // path for which branches were last loaded

	// Sandbox selector state (0=Inherit, 1=Enabled, 2=Disabled).
	sandboxIdx int
	// Preserved per-project sandbox paths (not editable in form, survives round-trip).
	sandboxDenyRead   []string
	sandboxExtraWrite []string

	// Path autocomplete state.
	pathACMatches []string // full paths of matching dirs
	pathACIdx     int
	pathACOpen    bool

	// OnBranchFocus is called when the branch field gains focus and the
	// path has changed since the last load. The caller should fetch branches
	// in a background goroutine and call SetBranchOptions with the results.
	OnBranchFocus func(path string)
}

// NewProjectForm creates a new project form.
func NewProjectForm() *ProjectForm {
	return &ProjectForm{
		Box: tview.NewBox(),
	}
}

// LoadProject populates the form for editing an existing project.
func (pf *ProjectForm) LoadProject(name string, p config.Project) {
	pf.fields[pfFieldName] = []rune(name)
	pf.fields[pfFieldPath] = []rune(p.Path)
	pf.fields[pfFieldBranch] = []rune(p.Branch)
	pf.fields[pfFieldBackend] = []rune(p.Backend)
	pf.sandboxIdx = sandboxInherit
	if p.Sandbox.Enabled != nil {
		if *p.Sandbox.Enabled {
			pf.sandboxIdx = sandboxEnabled
		} else {
			pf.sandboxIdx = sandboxDisabled
		}
	}
	pf.sandboxDenyRead = p.Sandbox.DenyRead
	pf.sandboxExtraWrite = p.Sandbox.ExtraWrite
	pf.editMode = true
	pf.focused = pfFieldPath // skip name in edit mode
}

func (pf *ProjectForm) Done() bool          { return pf.done }
func (pf *ProjectForm) Canceled() bool      { return pf.canceled }
func (pf *ProjectForm) SetError(msg string) { pf.errMsg = msg }

// branchIsSelector returns true when the branch field should render as a
// left/right selector instead of a text input.
func (pf *ProjectForm) branchIsSelector() bool {
	return len(pf.branchOptions) > 0
}

// SetBranchOptions sets the branch dropdown options. Called from a background
// goroutine via QueueUpdateDraw after fetching branches.
func (pf *ProjectForm) SetBranchOptions(options []string) {
	pf.branchOptions = options
	pf.branchIdx = 0

	// Pre-select the current branch value if it matches an option.
	cur := string(pf.fields[pfFieldBranch])
	for i, b := range pf.branchOptions {
		if b == cur {
			pf.branchIdx = i
			break
		}
	}
}

// Result returns the form values. Tilde in the path is expanded to an
// absolute path so downstream code (worktree creation, git commands) gets
// a real filesystem path.
func (pf *ProjectForm) Result() (name string, p config.Project) {
	branch := string(pf.fields[pfFieldBranch])
	if pf.branchIsSelector() && pf.branchIdx < len(pf.branchOptions) {
		branch = pf.branchOptions[pf.branchIdx]
	}
	proj := config.Project{
		Path:    expandTilde(strings.TrimSpace(string(pf.fields[pfFieldPath]))),
		Branch:  branch,
		Backend: string(pf.fields[pfFieldBackend]),
	}
	switch pf.sandboxIdx {
	case sandboxEnabled:
		v := true
		proj.Sandbox.Enabled = &v
	case sandboxDisabled:
		v := false
		proj.Sandbox.Enabled = &v
	} // sandboxInherit → nil (default)
	proj.Sandbox.DenyRead = pf.sandboxDenyRead
	proj.Sandbox.ExtraWrite = pf.sandboxExtraWrite
	return string(pf.fields[pfFieldName]), proj
}

// maybeLoadBranches fires OnBranchFocus when the path has changed since
// the last load. The actual git call happens in a background goroutine.
func (pf *ProjectForm) maybeLoadBranches() {
	path := string(pf.fields[pfFieldPath])
	if path == pf.branchPath || pf.OnBranchFocus == nil {
		return
	}
	pf.branchPath = path
	pf.OnBranchFocus(path)
}

// HandleKey processes key events for the form.
func (pf *ProjectForm) HandleKey(ev *tcell.EventKey) {
	// Path field autocomplete intercepts certain keys.
	if pf.focused == pfFieldPath {
		if pf.handlePathACKey(ev) {
			return
		}
	}

	switch ev.Key() {
	case tcell.KeyEscape, tcell.KeyCtrlQ:
		pf.canceled = true
		return
	case tcell.KeyEnter:
		if pf.focused < pfFieldSandbox {
			pf.focused++
			if pf.editMode && pf.focused == pfFieldName {
				pf.focused++
			}
			if pf.focused == pfFieldBranch {
				pf.maybeLoadBranches()
			}
		} else {
			pf.done = true
		}
		return
	case tcell.KeyTab:
		pf.closePathAC()
		pf.focused = (pf.focused + 1) % pfFieldCount
		if pf.editMode && pf.focused == pfFieldName {
			pf.focused++
		}
		if pf.focused == pfFieldBranch {
			pf.maybeLoadBranches()
		}
		return
	case tcell.KeyBacktab:
		pf.closePathAC()
		pf.focused = (pf.focused + pfFieldCount - 1) % pfFieldCount
		if pf.editMode && pf.focused == pfFieldName {
			pf.focused = pfFieldSandbox
		}
		if pf.focused == pfFieldBranch {
			pf.maybeLoadBranches()
		}
		return
	}

	// Selector fields — left/right cycles options.
	if pf.focused == pfFieldBranch && pf.branchIsSelector() {
		pf.handleBranchSelector(ev)
		return
	}
	if pf.focused == pfFieldSandbox {
		pf.handleSandboxSelector(ev)
		return
	}

	// Text field input.
	switch ev.Key() {
	case tcell.KeyBackspace, tcell.KeyBackspace2:
		f := pf.focused
		if pf.editMode && f == pfFieldName {
			return
		}
		if pf.cursors[f] > 0 {
			pf.fields[f] = append(pf.fields[f][:pf.cursors[f]-1], pf.fields[f][pf.cursors[f]:]...)
			pf.cursors[f]--
		}
		if f == pfFieldPath {
			pf.updatePathAC()
		}
		return
	case tcell.KeyLeft:
		if pf.cursors[pf.focused] > 0 {
			pf.cursors[pf.focused]--
		}
		return
	case tcell.KeyRight:
		if pf.cursors[pf.focused] < len(pf.fields[pf.focused]) {
			pf.cursors[pf.focused]++
		}
		return
	case tcell.KeyRune:
		if pf.editMode && pf.focused == pfFieldName {
			return
		}
		f := pf.focused
		r := ev.Rune()
		pf.fields[f] = append(pf.fields[f][:pf.cursors[f]], append([]rune{r}, pf.fields[f][pf.cursors[f]:]...)...)
		pf.cursors[f]++
		if f == pfFieldPath {
			pf.updatePathAC()
		}
		return
	}
}

// handlePathACKey handles autocomplete-specific keys when the path field is
// focused. Returns true if the event was consumed.
func (pf *ProjectForm) handlePathACKey(ev *tcell.EventKey) bool {
	switch ev.Key() {
	case tcell.KeyEscape:
		if pf.pathACOpen {
			pf.closePathAC()
			return true
		}
		return false
	case tcell.KeyTab:
		if pf.pathACOpen {
			pf.acceptPathAC()
			return true
		}
		// Trigger autocomplete on first Tab press.
		pf.updatePathAC()
		if pf.pathACOpen {
			pf.acceptPathAC()
			return true
		}
		return false
	case tcell.KeyEnter:
		if pf.pathACOpen {
			pf.acceptPathAC()
			return true
		}
		return false
	case tcell.KeyDown:
		if pf.pathACOpen && len(pf.pathACMatches) > 0 {
			pf.pathACIdx = (pf.pathACIdx + 1) % len(pf.pathACMatches)
			return true
		}
		return false // no-op when AC is closed
	case tcell.KeyUp:
		if pf.pathACOpen && len(pf.pathACMatches) > 0 {
			if pf.pathACIdx == 0 {
				pf.pathACIdx = len(pf.pathACMatches) - 1
			} else {
				pf.pathACIdx--
			}
			return true
		}
		return false // no-op when AC is closed
	}
	return false
}

// updatePathAC computes directory completions for the current path input.
// Note: os.ReadDir runs synchronously — acceptable for local filesystems
// (same pattern as QuickAddForm), but may lag on NFS/iCloud mounts.
func (pf *ProjectForm) updatePathAC() {
	raw := string(pf.fields[pfFieldPath])
	matches := dirCompletions(raw)
	if matches == nil {
		pf.closePathAC()
		return
	}
	pf.pathACMatches = matches
	pf.pathACOpen = true
	if pf.pathACIdx >= len(pf.pathACMatches) {
		pf.pathACIdx = 0
	}
}

// acceptPathAC replaces the path input with the selected autocomplete match.
func (pf *ProjectForm) acceptPathAC() {
	if !pf.pathACOpen || pf.pathACIdx >= len(pf.pathACMatches) {
		return
	}
	path := collapseTilde(pf.pathACMatches[pf.pathACIdx]) + "/"
	pf.fields[pfFieldPath] = []rune(path)
	pf.cursors[pfFieldPath] = len(pf.fields[pfFieldPath])
	pf.closePathAC()
	// Re-open dropdown if the accepted directory has sub-directories.
	pf.updatePathAC()
}

// closePathAC dismisses the autocomplete dropdown.
func (pf *ProjectForm) closePathAC() {
	pf.pathACOpen = false
	pf.pathACMatches = nil
	pf.pathACIdx = 0
}

// handleBranchSelector processes keys when the branch field is in selector mode.
func (pf *ProjectForm) handleBranchSelector(ev *tcell.EventKey) {
	n := len(pf.branchOptions)
	if n == 0 {
		return
	}
	switch ev.Key() {
	case tcell.KeyLeft:
		pf.branchIdx = (pf.branchIdx - 1 + n) % n
	case tcell.KeyRight:
		pf.branchIdx = (pf.branchIdx + 1) % n
	}
}

// handleSandboxSelector processes keys when the sandbox field is focused.
func (pf *ProjectForm) handleSandboxSelector(ev *tcell.EventKey) {
	n := len(sandboxOptions)
	switch ev.Key() {
	case tcell.KeyLeft:
		pf.sandboxIdx = (pf.sandboxIdx - 1 + n) % n
	case tcell.KeyRight:
		pf.sandboxIdx = (pf.sandboxIdx + 1) % n
	}
}

// PasteHandler handles bracketed paste events, inserting pasted text into the
// focused field in a single operation.
func (pf *ProjectForm) PasteHandler() func(pastedText string, setFocus func(p tview.Primitive)) {
	return pf.WrapPasteHandler(func(pastedText string, setFocus func(p tview.Primitive)) {
		f := pf.focused
		if pf.editMode && f == pfFieldName {
			return
		}
		// Ignore paste on selector fields.
		if f == pfFieldBranch && pf.branchIsSelector() {
			return
		}
		if f == pfFieldSandbox {
			return
		}
		runes := []rune(pastedText)
		if len(runes) == 0 {
			return
		}
		newField := make([]rune, 0, len(pf.fields[f])+len(runes))
		newField = append(newField, pf.fields[f][:pf.cursors[f]]...)
		newField = append(newField, runes...)
		newField = append(newField, pf.fields[f][pf.cursors[f]:]...)
		pf.fields[f] = newField
		pf.cursors[f] += len(runes)
		if f == pfFieldPath {
			pf.updatePathAC()
		}
	})
}

// Draw renders the project form as a modal.
func (pf *ProjectForm) Draw(screen tcell.Screen) {
	pf.Box.DrawForSubclass(screen, pf)
	x, y, width, height := pf.GetInnerRect()
	if width <= 0 || height <= 0 {
		return
	}

	// Compute autocomplete row count for dynamic form height.
	acRows := 0
	if pf.pathACOpen && len(pf.pathACMatches) > 0 {
		acRows = len(pf.pathACMatches)
		if acRows > pfMaxACVisible {
			acRows = pfMaxACVisible
		}
	}

	// Center the form.
	formW := min(60, width-4)
	formH := 13 + acRows // pfFieldCount*2 rows + 3 overhead (title, border, error)
	formX := x + (width-formW)/2
	formY := y + (height-formH)/2
	if formY < y {
		formY = y
	}

	modalBG := tcell.ColorDefault
	drawBorder(screen, formX, formY, formW, formH, StyleFocusedBorder)

	title := " New Project "
	if pf.editMode {
		title = " Edit Project "
	}
	titleX := formX + (formW-utf8.RuneCountInString(title))/2
	titleStyle := tcell.StyleDefault.Foreground(ColorTitle).Bold(true).Background(modalBG)
	for i, r := range title {
		screen.SetContent(titleX+i, formY, r, nil, titleStyle)
	}

	labels := [pfFieldCount]string{"Name:", "Path:", "Branch:", "Backend:", "Sandbox:"}
	maxW := formW - 14
	extraOffset := 0 // extra rows inserted after path field for AC dropdown
	for i := range pfFieldCount {
		ly := formY + 2 + i*2 + extraOffset
		if ly >= formY+formH-1 {
			break
		}
		style := StyleDimmed
		if i == pf.focused {
			style = tcell.StyleDefault.Foreground(ColorTitle)
		}
		drawText(screen, formX+2, ly, 10, labels[i], style)

		// Selector fields.
		if i == pfFieldBranch && pf.branchIsSelector() {
			pf.drawBranchSelector(screen, formX+12, ly, maxW)
			continue
		}
		if i == pfFieldSandbox {
			pf.drawSandboxSelector(screen, formX+12, ly, maxW)
			continue
		}

		// Field value (text input).
		val := string(pf.fields[i])
		if i == pf.focused {
			before := string(pf.fields[i][:pf.cursors[i]])
			after := string(pf.fields[i][pf.cursors[i]:])
			val = before + "█" + after
		}
		if pf.editMode && i == pfFieldName {
			style = StyleDimmed
		} else {
			style = tcell.StyleDefault
		}
		if len(val) > maxW {
			val = val[len(val)-maxW:]
		}
		drawText(screen, formX+12, ly, maxW, val, style)

		// Draw autocomplete dropdown right after the path field.
		if i == pfFieldPath && pf.pathACOpen && len(pf.pathACMatches) > 0 {
			extraOffset += pf.drawPathAC(screen, formX+12, ly+1, maxW)
		}
	}

	if pf.errMsg != "" {
		drawText(screen, formX+2, formY+formH-2, formW-4, pf.errMsg, StyleError)
	}
}

// drawPathAC renders the autocomplete dropdown below the path input.
// Returns the number of rows drawn.
func (pf *ProjectForm) drawPathAC(screen tcell.Screen, x, y, w int) int {
	visible := len(pf.pathACMatches)
	if visible > pfMaxACVisible {
		visible = pfMaxACVisible
	}

	acScroll := 0
	if pf.pathACIdx >= visible {
		acScroll = pf.pathACIdx - visible + 1
	}

	selectedStyle := tcell.StyleDefault.Bold(true).Foreground(ColorSelected)
	for vi := 0; vi < visible; vi++ {
		idx := acScroll + vi
		if idx >= len(pf.pathACMatches) {
			break
		}
		display := collapseTilde(pf.pathACMatches[idx])

		indicator := "  "
		if idx == pf.pathACIdx {
			indicator = "> "
		}
		line := indicator + display
		st := StyleDimmed
		if idx == pf.pathACIdx {
			st = selectedStyle
		}
		lineRunes := []rune(line)
		for c := 0; c < w && c < len(lineRunes); c++ {
			screen.SetContent(x+c, y+vi, lineRunes[c], nil, st)
		}
	}
	return visible
}

// drawSandboxSelector renders the sandbox field as a ◀/▶ selector.
func (pf *ProjectForm) drawSandboxSelector(screen tcell.Screen, x, y, w int) {
	name := sandboxOptions[pf.sandboxIdx]
	selector := "◀ " + name + " ▶"
	st := StyleNormal
	if pf.focused == pfFieldSandbox {
		st = StyleSelected
	}
	drawText(screen, x, y, w, selector, st)
}

// drawBranchSelector renders the branch field as a ◀/▶ selector.
func (pf *ProjectForm) drawBranchSelector(screen tcell.Screen, x, y, w int) {
	if len(pf.branchOptions) == 0 {
		drawText(screen, x, y, w, "(none)", StyleDimmed)
		return
	}

	name := pf.branchOptions[pf.branchIdx]
	selector := "◀ " + name + " ▶"
	st := StyleNormal
	if pf.focused == pfFieldBranch {
		st = StyleSelected
	}
	drawText(screen, x, y, w, selector, st)

	// Position indicator.
	posText := "(" + itoa(pf.branchIdx+1) + "/" + itoa(len(pf.branchOptions)) + ")"
	posX := x + w - utf8.RuneCountInString(posText)
	if posX > x+utf8.RuneCountInString(selector)+1 {
		drawText(screen, posX, y, utf8.RuneCountInString(posText), posText, StyleDimmed)
	}
}
