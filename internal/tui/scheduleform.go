package tui

import (
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/tui/theme"
	"github.com/drn/argus/internal/tui/widget"
)

const (
	sfFieldName     = 0
	sfFieldProject  = 1
	sfFieldBackend  = 2
	sfFieldSchedule = 3
	sfFieldPrompt   = 4
	sfFieldEnabled  = 5
	sfFieldCount    = 6
)

// ScheduleForm is a modal form for adding/editing scheduled tasks.
//
// Single-line text inputs are used for all fields except Prompt, which
// supports pasted multi-line content (rendered as a preview). Most prompt
// authoring happens on the web — this form is for quick CRUD, toggling
// enabled, and validating the cron expression in-place.
type ScheduleForm struct {
	*tview.Box

	// Text fields: name, schedule, prompt. Project / backend / enabled are
	// selectors and live in their own state.
	fields  [sfFieldCount][]rune
	cursors [sfFieldCount]int
	focused int

	editMode bool
	done     bool
	canceled bool
	errMsg   string

	// Project + backend selectors share the cycle pattern from projectform —
	// left/right cycles through options, with index in `<field>Idx`.
	projectOptions []string
	projectIdx     int
	backendOptions []string // first slot is "" → default
	backendIdx     int

	enabled bool

	// scheduleID is set in edit mode; used by the caller to know which row to
	// update.
	scheduleID string
}

// NewScheduleForm creates a new schedule form with the given project and
// backend lists. Pass non-empty lists; the form does not allow free-text
// project/backend entry.
func NewScheduleForm(projects, backends []string) *ScheduleForm {
	sf := &ScheduleForm{
		Box:            tview.NewBox(),
		projectOptions: projects,
		// Prepend "" so the user can select default-backend.
		backendOptions: append([]string{""}, backends...),
		enabled:        true,
		focused:        sfFieldName,
	}
	// Sensible defaults — most schedules are daily.
	sf.fields[sfFieldSchedule] = []rune("@daily")
	return sf
}

// LoadSchedule populates the form for editing an existing schedule.
func (sf *ScheduleForm) LoadSchedule(s *model.ScheduledTask) {
	sf.scheduleID = s.ID
	sf.fields[sfFieldName] = []rune(s.Name)
	sf.fields[sfFieldSchedule] = []rune(s.Schedule)
	sf.fields[sfFieldPrompt] = []rune(s.Prompt)
	sf.enabled = s.Enabled
	for i, p := range sf.projectOptions {
		if p == s.Project {
			sf.projectIdx = i
			break
		}
	}
	for i, b := range sf.backendOptions {
		if b == s.Backend {
			sf.backendIdx = i
			break
		}
	}
	sf.editMode = true
	sf.focused = sfFieldName
}

func (sf *ScheduleForm) Done() bool          { return sf.done }
func (sf *ScheduleForm) Canceled() bool      { return sf.canceled }
func (sf *ScheduleForm) SetError(msg string) { sf.errMsg = msg }
func (sf *ScheduleForm) ScheduleID() string  { return sf.scheduleID }

// Result returns the form's current values as a partially-populated
// ScheduledTask. The caller fills in CreatedAt / scheduler-managed fields.
func (sf *ScheduleForm) Result() *model.ScheduledTask {
	project := ""
	if sf.projectIdx >= 0 && sf.projectIdx < len(sf.projectOptions) {
		project = sf.projectOptions[sf.projectIdx]
	}
	backend := ""
	if sf.backendIdx >= 0 && sf.backendIdx < len(sf.backendOptions) {
		backend = sf.backendOptions[sf.backendIdx]
	}
	return &model.ScheduledTask{
		ID:       sf.scheduleID,
		Name:     strings.TrimSpace(string(sf.fields[sfFieldName])),
		Project:  project,
		Backend:  backend,
		Schedule: strings.TrimSpace(string(sf.fields[sfFieldSchedule])),
		Prompt:   string(sf.fields[sfFieldPrompt]),
		Enabled:  sf.enabled,
	}
}

func (sf *ScheduleForm) HandleKey(ev *tcell.EventKey) {
	switch ev.Key() {
	case tcell.KeyEscape, tcell.KeyCtrlQ:
		sf.canceled = true
		return
	case tcell.KeyTab:
		sf.focused = (sf.focused + 1) % sfFieldCount
		return
	case tcell.KeyBacktab:
		sf.focused = (sf.focused + sfFieldCount - 1) % sfFieldCount
		return
	case tcell.KeyEnter:
		// Enter on a selector toggles/cycles forward. Enter on the last
		// selector (Enabled) submits.
		if sf.focused == sfFieldEnabled {
			sf.done = true
			return
		}
		if sf.isSelector(sf.focused) {
			sf.cycle(sf.focused, 1)
			return
		}
		sf.focused++
		return
	case tcell.KeyCtrlS:
		sf.done = true
		return
	}

	if sf.isSelector(sf.focused) {
		switch ev.Key() {
		case tcell.KeyLeft:
			sf.cycle(sf.focused, -1)
			return
		case tcell.KeyRight:
			sf.cycle(sf.focused, 1)
			return
		}
		return
	}

	// Text field input.
	switch ev.Key() {
	case tcell.KeyBackspace, tcell.KeyBackspace2:
		f := sf.focused
		if sf.cursors[f] > 0 {
			sf.fields[f] = append(sf.fields[f][:sf.cursors[f]-1], sf.fields[f][sf.cursors[f]:]...)
			sf.cursors[f]--
		}
		return
	case tcell.KeyLeft:
		if sf.cursors[sf.focused] > 0 {
			sf.cursors[sf.focused]--
		}
		return
	case tcell.KeyRight:
		if sf.cursors[sf.focused] < len(sf.fields[sf.focused]) {
			sf.cursors[sf.focused]++
		}
		return
	case tcell.KeyRune:
		f := sf.focused
		r := ev.Rune()
		sf.fields[f] = append(sf.fields[f][:sf.cursors[f]], append([]rune{r}, sf.fields[f][sf.cursors[f]:]...)...)
		sf.cursors[f]++
		return
	}
}

func (sf *ScheduleForm) isSelector(field int) bool {
	return field == sfFieldProject || field == sfFieldBackend || field == sfFieldEnabled
}

func (sf *ScheduleForm) cycle(field, dir int) {
	switch field {
	case sfFieldProject:
		if n := len(sf.projectOptions); n > 0 {
			sf.projectIdx = (sf.projectIdx + dir + n) % n
		}
	case sfFieldBackend:
		if n := len(sf.backendOptions); n > 0 {
			sf.backendIdx = (sf.backendIdx + dir + n) % n
		}
	case sfFieldEnabled:
		sf.enabled = !sf.enabled
	}
}

// PasteHandler accepts pasted text into the focused text field, including
// the prompt field where multi-line content is preserved verbatim.
func (sf *ScheduleForm) PasteHandler() func(pastedText string, setFocus func(p tview.Primitive)) {
	return sf.WrapPasteHandler(func(pastedText string, setFocus func(p tview.Primitive)) {
		if sf.isSelector(sf.focused) {
			return
		}
		runes := []rune(pastedText)
		if len(runes) == 0 {
			return
		}
		f := sf.focused
		newField := make([]rune, 0, len(sf.fields[f])+len(runes))
		newField = append(newField, sf.fields[f][:sf.cursors[f]]...)
		newField = append(newField, runes...)
		newField = append(newField, sf.fields[f][sf.cursors[f]:]...)
		sf.fields[f] = newField
		sf.cursors[f] += len(runes)
	})
}

// Draw renders the schedule form as a centred modal.
func (sf *ScheduleForm) Draw(screen tcell.Screen) {
	sf.DrawForSubclass(screen, sf)
	x, y, width, height := sf.GetInnerRect()
	if width <= 0 || height <= 0 {
		return
	}

	formW := min(72, width-4)
	formH := 18 // 6 fields * 2 rows + title + error + padding
	formX := x + (width-formW)/2
	formY := y + (height-formH)/2
	if formY < y {
		formY = y
	}

	widget.DrawBorder(screen, formX, formY, formW, formH, theme.StyleFocusedBorder)

	title := "New Scheduled Task"
	if sf.editMode {
		title = "Edit Scheduled Task"
	}
	widget.DrawText(screen, formX+2, formY+1, formW-4, title, theme.StyleTitle)

	labelCol := formX + 2
	valueCol := formX + 14
	maxValW := formW - 16

	for i := range sfFieldCount {
		ly := formY + 3 + i*2
		if ly >= formY+formH-1 {
			break
		}
		labels := [sfFieldCount]string{"Name:", "Project:", "Backend:", "Schedule:", "Prompt:", "Enabled:"}
		labelStyle := theme.StyleDimmed
		if i == sf.focused {
			labelStyle = tcell.StyleDefault.Foreground(theme.ColorTitle)
		}
		widget.DrawText(screen, labelCol, ly, 12, labels[i], labelStyle)

		switch i {
		case sfFieldProject:
			sf.drawSelector(screen, valueCol, ly, maxValW, sf.projectValue(), i == sf.focused, len(sf.projectOptions) > 1)
		case sfFieldBackend:
			label := sf.backendValue()
			if label == "" {
				label = "(default)"
			}
			sf.drawSelector(screen, valueCol, ly, maxValW, label, i == sf.focused, len(sf.backendOptions) > 1)
		case sfFieldEnabled:
			label := "Off"
			if sf.enabled {
				label = "On"
			}
			sf.drawSelector(screen, valueCol, ly, maxValW, label, i == sf.focused, true)
		default:
			sf.drawTextField(screen, valueCol, ly, maxValW, i)
		}
	}

	// Hint footer.
	hint := "[tab] next field  [enter] cycle/submit  [ctrl-s] save  [esc] cancel"
	if sf.errMsg != "" {
		widget.DrawText(screen, formX+2, formY+formH-2, formW-4, sf.errMsg, theme.StyleError)
	} else {
		widget.DrawText(screen, formX+2, formY+formH-2, formW-4, hint, theme.StyleDimmed)
	}
}

func (sf *ScheduleForm) drawSelector(screen tcell.Screen, x, y, w int, label string, focused, hasOptions bool) {
	prefix := "  "
	suffix := "  "
	if focused && hasOptions {
		prefix = "◀ "
		suffix = " ▶"
	}
	display := prefix + label + suffix
	style := theme.StyleNormal
	if focused {
		style = theme.StyleSelected
	}
	widget.DrawText(screen, x, y, w, display, style)
}

func (sf *ScheduleForm) drawTextField(screen tcell.Screen, x, y, w, idx int) {
	val := string(sf.fields[idx])
	// For the prompt field, show only the first line + a "+N more" hint when
	// multi-line. The full text is preserved in the rune buffer regardless.
	if idx == sfFieldPrompt {
		lines := strings.Split(val, "\n")
		val = lines[0]
		if len(lines) > 1 {
			val += "  …(+" + itoa(len(lines)-1) + " more lines)"
		}
	}
	if sf.focused == idx {
		// Show cursor — but only when editing the visible chunk. Multi-line
		// prompts always show the cursor at end-of-first-line for simplicity;
		// users edit prompts via the web for fine control.
		before := string(sf.fields[idx][:sf.cursors[idx]])
		after := string(sf.fields[idx][sf.cursors[idx]:])
		if idx == sfFieldPrompt {
			// Just append the cursor at the end of the displayed content.
			val = val + "█"
		} else {
			val = before + "█" + after
		}
	}
	runes := []rune(val)
	if len(runes) > w {
		runes = runes[len(runes)-w:]
	}
	widget.DrawText(screen, x, y, w, string(runes), tcell.StyleDefault)
}

func (sf *ScheduleForm) projectValue() string {
	if sf.projectIdx >= 0 && sf.projectIdx < len(sf.projectOptions) {
		return sf.projectOptions[sf.projectIdx]
	}
	return ""
}

func (sf *ScheduleForm) backendValue() string {
	if sf.backendIdx >= 0 && sf.backendIdx < len(sf.backendOptions) {
		return sf.backendOptions[sf.backendIdx]
	}
	return ""
}
