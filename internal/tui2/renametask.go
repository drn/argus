package tui2

import (
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// RenameTaskForm is a single-field modal for renaming a task.
// Rename is display-only — worktree dir, branch, and session ID are preserved.
type RenameTaskForm struct {
	*tview.Box
	name     []rune
	cursor   int
	done     bool
	canceled bool
	errMsg   string
}

// NewRenameTaskForm creates a rename form pre-populated with the current name.
func NewRenameTaskForm(currentName string) *RenameTaskForm {
	runes := []rune(currentName)
	return &RenameTaskForm{
		Box:    tview.NewBox(),
		name:   runes,
		cursor: len(runes),
	}
}

func (rf *RenameTaskForm) Done() bool      { return rf.done }
func (rf *RenameTaskForm) Canceled() bool  { return rf.canceled }
func (rf *RenameTaskForm) Name() string    { return string(rf.name) }
func (rf *RenameTaskForm) SetError(msg string) { rf.errMsg = msg }

// HandleKey processes key events for the rename form.
func (rf *RenameTaskForm) HandleKey(ev *tcell.EventKey) {
	switch ev.Key() {
	case tcell.KeyEscape:
		rf.canceled = true
	case tcell.KeyEnter:
		rf.done = true
	case tcell.KeyBackspace, tcell.KeyBackspace2:
		if rf.cursor > 0 {
			rf.name = append(rf.name[:rf.cursor-1], rf.name[rf.cursor:]...)
			rf.cursor--
		}
	case tcell.KeyLeft:
		if rf.cursor > 0 {
			rf.cursor--
		}
	case tcell.KeyRight:
		if rf.cursor < len(rf.name) {
			rf.cursor++
		}
	case tcell.KeyRune:
		r := ev.Rune()
		rf.name = append(rf.name[:rf.cursor], append([]rune{r}, rf.name[rf.cursor:]...)...)
		rf.cursor++
	}
}

// Draw renders the rename form as a modal.
func (rf *RenameTaskForm) Draw(screen tcell.Screen) {
	rf.Box.DrawForSubclass(screen, rf)
	x, y, width, height := rf.GetInnerRect()
	if width <= 0 || height <= 0 {
		return
	}

	formW := min(50, width-4)
	formH := 7
	formX := x + (width-formW)/2
	formY := y + (height-formH)/2
	if formY < y {
		formY = y
	}

	drawBorder(screen, formX, formY, formW, formH, StyleFocusedBorder)
	drawText(screen, formX+2, formY+1, formW-4, "Rename Task", StyleTitle)

	// Name field with cursor.
	before := string(rf.name[:rf.cursor])
	after := string(rf.name[rf.cursor:])
	val := before + "█" + after
	maxW := formW - 4
	if len(val) > maxW {
		val = val[len(val)-maxW:]
	}
	drawText(screen, formX+2, formY+3, maxW, val, tcell.StyleDefault)

	if rf.errMsg != "" {
		drawText(screen, formX+2, formY+5, formW-4, rf.errMsg, StyleError)
	}
}
