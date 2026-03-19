package tui2

import (
	"unicode/utf8"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/drn/argus/internal/config"
)

const (
	pfFieldName    = 0
	pfFieldPath    = 1
	pfFieldBranch  = 2
	pfFieldBackend = 3
)

// ProjectForm is a modal form for adding/editing projects.
type ProjectForm struct {
	*tview.Box
	fields   [4][]rune // name, path, branch, backend
	cursors  [4]int
	focused  int
	editMode bool // true = editing (name read-only)
	done     bool
	canceled bool
	errMsg   string
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
	pf.editMode = true
	pf.focused = pfFieldPath // skip name in edit mode
}

func (pf *ProjectForm) Done() bool     { return pf.done }
func (pf *ProjectForm) Canceled() bool { return pf.canceled }
func (pf *ProjectForm) SetError(msg string) { pf.errMsg = msg }

// Result returns the form values.
func (pf *ProjectForm) Result() (name string, p config.Project) {
	return string(pf.fields[pfFieldName]), config.Project{
		Path:    string(pf.fields[pfFieldPath]),
		Branch:  string(pf.fields[pfFieldBranch]),
		Backend: string(pf.fields[pfFieldBackend]),
	}
}

// HandleKey processes key events for the form.
func (pf *ProjectForm) HandleKey(ev *tcell.EventKey) {
	switch ev.Key() {
	case tcell.KeyEscape:
		pf.canceled = true
		return
	case tcell.KeyEnter:
		if pf.focused < pfFieldBackend {
			pf.focused++
			if pf.editMode && pf.focused == pfFieldName {
				pf.focused++ // skip name in edit mode
			}
		} else {
			pf.done = true
		}
		return
	case tcell.KeyTab:
		pf.focused = (pf.focused + 1) % 4
		if pf.editMode && pf.focused == pfFieldName {
			pf.focused++
		}
		return
	case tcell.KeyBacktab:
		pf.focused = (pf.focused + 3) % 4
		if pf.editMode && pf.focused == pfFieldName {
			pf.focused = pfFieldBackend
		}
		return
	case tcell.KeyBackspace, tcell.KeyBackspace2:
		f := pf.focused
		if pf.editMode && f == pfFieldName {
			return
		}
		if pf.cursors[f] > 0 {
			pf.fields[f] = append(pf.fields[f][:pf.cursors[f]-1], pf.fields[f][pf.cursors[f]:]...)
			pf.cursors[f]--
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
		return
	}
	_ = utf8.RuneError // ensure import
}

// Draw renders the project form as a modal.
func (pf *ProjectForm) Draw(screen tcell.Screen) {
	pf.Box.DrawForSubclass(screen, pf)
	x, y, width, height := pf.GetInnerRect()
	if width <= 0 || height <= 0 {
		return
	}

	// Center the form.
	formW := min(60, width-4)
	formH := 14
	formX := x + (width-formW)/2
	formY := y + (height-formH)/2
	if formY < y {
		formY = y
	}

	drawBorder(screen, formX, formY, formW, formH, StyleFocusedBorder)

	title := "New Project"
	if pf.editMode {
		title = "Edit Project"
	}
	drawText(screen, formX+2, formY+1, formW-4, title, StyleTitle)

	labels := [4]string{"Name:", "Path:", "Branch:", "Backend:"}
	for i := range 4 {
		ly := formY + 3 + i*2
		if ly >= formY+formH-1 {
			break
		}
		style := StyleDimmed
		if i == pf.focused {
			style = tcell.StyleDefault.Foreground(ColorTitle)
		}
		drawText(screen, formX+2, ly, 10, labels[i], style)

		// Field value.
		val := string(pf.fields[i])
		if i == pf.focused {
			// Insert cursor.
			before := string(pf.fields[i][:pf.cursors[i]])
			after := string(pf.fields[i][pf.cursors[i]:])
			val = before + "█" + after
		}
		if pf.editMode && i == pfFieldName {
			style = StyleDimmed
		} else {
			style = tcell.StyleDefault
		}
		maxW := formW - 14
		if len(val) > maxW {
			val = val[len(val)-maxW:]
		}
		drawText(screen, formX+12, ly, maxW, val, style)
	}

	if pf.errMsg != "" {
		drawText(screen, formX+2, formY+formH-2, formW-4, pf.errMsg, StyleError)
	}
}
