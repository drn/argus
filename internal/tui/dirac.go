package tui

import (
	"github.com/gdamore/tcell/v2"
)

// dirAC is a reusable directory autocomplete widget. It manages the dropdown
// state (matches, selected index, open/closed) and provides key handling and
// rendering. Consumers call Update after every input change, HandleKey for
// navigation events, and Draw to render the dropdown.
// Callers must pass the same maxVisible value to both Len and Draw.
type dirAC struct {
	matches []string // full paths of matching dirs
	idx     int
	open    bool
}

// Update recomputes directory completions for the given input string.
// Call this after every keystroke or paste that changes the path buffer.
func (ac *dirAC) Update(input string) {
	matches := dirCompletions(input)
	if matches == nil {
		ac.Close()
		return
	}
	ac.matches = matches
	ac.open = true
	if ac.idx >= len(ac.matches) {
		ac.idx = 0
	}
}

// Accept selects the current autocomplete match and returns the accepted path
// (with trailing slash, tilde-collapsed). Returns "" if the dropdown is closed
// or the index is out of range. After accepting, Update is called with the
// tilde-collapsed path to re-open the dropdown if the directory has children.
// This works because dirCompletions calls expandTilde internally.
func (ac *dirAC) Accept() string {
	if !ac.open || ac.idx >= len(ac.matches) {
		return ""
	}
	path := collapseTilde(ac.matches[ac.idx]) + "/"
	ac.Close()
	ac.Update(path)
	return path
}

// Close dismisses the autocomplete dropdown.
func (ac *dirAC) Close() {
	ac.open = false
	ac.matches = nil
	ac.idx = 0
}

// Open returns whether the autocomplete dropdown is visible.
func (ac *dirAC) Open() bool {
	return ac.open
}

// HandleKey processes navigation keys (Tab, Enter, Up, Down, Escape) when the
// autocomplete dropdown is relevant. Returns true if the event was consumed.
// The caller should pass the current input string so Tab can trigger+accept in
// one step when the dropdown is closed.
func (ac *dirAC) HandleKey(ev *tcell.EventKey, input string) (consumed bool, accepted string) {
	switch ev.Key() {
	case tcell.KeyTab:
		if ac.open {
			return true, ac.Accept()
		}
		ac.Update(input)
		if ac.open {
			return true, ac.Accept()
		}
		return false, "" // no completions — let caller handle Tab
	case tcell.KeyEnter:
		if ac.open {
			return true, ac.Accept()
		}
		return false, ""
	case tcell.KeyEscape, tcell.KeyCtrlQ:
		if ac.open {
			ac.Close()
			return true, ""
		}
		return false, ""
	case tcell.KeyDown:
		if ac.open && len(ac.matches) > 0 {
			ac.idx = (ac.idx + 1) % len(ac.matches)
			return true, ""
		}
		return false, ""
	case tcell.KeyUp:
		if ac.open && len(ac.matches) > 0 {
			if ac.idx == 0 {
				ac.idx = len(ac.matches) - 1
			} else {
				ac.idx--
			}
			return true, ""
		}
		return false, ""
	}
	return false, ""
}

// Draw renders the autocomplete dropdown at (x, y) with width w.
// maxVisible caps the number of rows shown. Returns the number of rows drawn.
func (ac *dirAC) Draw(screen tcell.Screen, x, y, w, maxVisible int) int {
	if !ac.open || len(ac.matches) == 0 {
		return 0
	}

	visible := len(ac.matches)
	if visible > maxVisible {
		visible = maxVisible
	}

	acScroll := 0
	if ac.idx >= visible {
		acScroll = ac.idx - visible + 1
	}

	selectedStyle := tcell.StyleDefault.Bold(true).Foreground(ColorSelected)
	for vi := 0; vi < visible; vi++ {
		idx := acScroll + vi
		if idx >= len(ac.matches) {
			break
		}
		display := collapseTilde(ac.matches[idx])

		indicator := "  "
		if idx == ac.idx {
			indicator = "> "
		}
		line := indicator + display
		st := StyleDimmed
		if idx == ac.idx {
			st = selectedStyle
		}
		lineRunes := []rune(line)
		for c := 0; c < w && c < len(lineRunes); c++ {
			screen.SetContent(x+c, y+vi, lineRunes[c], nil, st)
		}
	}
	return visible
}

// Len returns the number of visible autocomplete rows (capped at maxVisible).
func (ac *dirAC) Len(maxVisible int) int {
	if !ac.open || len(ac.matches) == 0 {
		return 0
	}
	if len(ac.matches) > maxVisible {
		return maxVisible
	}
	return len(ac.matches)
}
