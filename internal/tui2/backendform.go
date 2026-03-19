package tui2

import (
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/drn/argus/internal/config"
)

const (
	bfFieldName      = 0
	bfFieldCommand   = 1
	bfFieldPromptFlag = 2
)

// BackendForm is a modal form for adding/editing backends.
type BackendForm struct {
	*tview.Box
	fields   [3][]rune // name, command, prompt flag
	cursors  [3]int
	focused  int
	editMode bool // true = editing (name read-only)
	done     bool
	canceled bool
	errMsg   string
}

// NewBackendForm creates a new backend form.
func NewBackendForm() *BackendForm {
	return &BackendForm{
		Box: tview.NewBox(),
	}
}

// LoadBackend populates the form for editing an existing backend.
func (bf *BackendForm) LoadBackend(name string, b config.Backend) {
	bf.fields[bfFieldName] = []rune(name)
	bf.fields[bfFieldCommand] = []rune(b.Command)
	bf.fields[bfFieldPromptFlag] = []rune(b.PromptFlag)
	bf.editMode = true
	bf.focused = bfFieldCommand // skip name in edit mode
}

func (bf *BackendForm) Done() bool     { return bf.done }
func (bf *BackendForm) Canceled() bool { return bf.canceled }
func (bf *BackendForm) SetError(msg string) { bf.errMsg = msg }

// Result returns the form values.
func (bf *BackendForm) Result() (name string, b config.Backend) {
	return string(bf.fields[bfFieldName]), config.Backend{
		Command:    string(bf.fields[bfFieldCommand]),
		PromptFlag: string(bf.fields[bfFieldPromptFlag]),
	}
}

// HandleKey processes key events for the form.
func (bf *BackendForm) HandleKey(ev *tcell.EventKey) {
	switch ev.Key() {
	case tcell.KeyEscape:
		bf.canceled = true
		return
	case tcell.KeyEnter:
		if bf.focused < bfFieldPromptFlag {
			bf.focused++
			if bf.editMode && bf.focused == bfFieldName {
				bf.focused++
			}
		} else {
			bf.done = true
		}
		return
	case tcell.KeyTab:
		bf.focused = (bf.focused + 1) % 3
		if bf.editMode && bf.focused == bfFieldName {
			bf.focused++
		}
		return
	case tcell.KeyBacktab:
		bf.focused = (bf.focused + 2) % 3
		if bf.editMode && bf.focused == bfFieldName {
			bf.focused = bfFieldPromptFlag
		}
		return
	case tcell.KeyBackspace, tcell.KeyBackspace2:
		f := bf.focused
		if bf.editMode && f == bfFieldName {
			return
		}
		if bf.cursors[f] > 0 {
			bf.fields[f] = append(bf.fields[f][:bf.cursors[f]-1], bf.fields[f][bf.cursors[f]:]...)
			bf.cursors[f]--
		}
		return
	case tcell.KeyLeft:
		if bf.cursors[bf.focused] > 0 {
			bf.cursors[bf.focused]--
		}
		return
	case tcell.KeyRight:
		if bf.cursors[bf.focused] < len(bf.fields[bf.focused]) {
			bf.cursors[bf.focused]++
		}
		return
	case tcell.KeyRune:
		if bf.editMode && bf.focused == bfFieldName {
			return
		}
		f := bf.focused
		r := ev.Rune()
		bf.fields[f] = append(bf.fields[f][:bf.cursors[f]], append([]rune{r}, bf.fields[f][bf.cursors[f]:]...)...)
		bf.cursors[f]++
		return
	}
}

// Draw renders the backend form as a modal.
func (bf *BackendForm) Draw(screen tcell.Screen) {
	bf.Box.DrawForSubclass(screen, bf)
	x, y, width, height := bf.GetInnerRect()
	if width <= 0 || height <= 0 {
		return
	}

	formW := min(60, width-4)
	formH := 12
	formX := x + (width-formW)/2
	formY := y + (height-formH)/2
	if formY < y {
		formY = y
	}

	drawBorder(screen, formX, formY, formW, formH, StyleFocusedBorder)

	title := "New Backend"
	if bf.editMode {
		title = "Edit Backend"
	}
	drawText(screen, formX+2, formY+1, formW-4, title, StyleTitle)

	labels := [3]string{"Name:", "Command:", "Prompt Flag:"}
	for i := range 3 {
		ly := formY + 3 + i*2
		if ly >= formY+formH-1 {
			break
		}
		style := StyleDimmed
		if i == bf.focused {
			style = tcell.StyleDefault.Foreground(ColorTitle)
		}
		drawText(screen, formX+2, ly, 14, labels[i], style)

		val := string(bf.fields[i])
		if i == bf.focused {
			before := string(bf.fields[i][:bf.cursors[i]])
			after := string(bf.fields[i][bf.cursors[i]:])
			val = before + "█" + after
		}
		fieldStyle := tcell.StyleDefault
		if bf.editMode && i == bfFieldName {
			fieldStyle = StyleDimmed
		}
		maxW := formW - 18
		if len(val) > maxW {
			val = val[len(val)-maxW:]
		}
		drawText(screen, formX+16, ly, maxW, val, fieldStyle)
	}

	if bf.errMsg != "" {
		drawText(screen, formX+2, formY+formH-2, formW-4, bf.errMsg, StyleError)
	}
}
