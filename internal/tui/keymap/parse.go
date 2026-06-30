package keymap

import (
	"fmt"
	"strings"

	"github.com/gdamore/tcell/v2"
)

// ctrlKeys maps a lowercase letter to its tcell control-key constant. Built from
// the named constants (not arithmetic) so a tcell version bump that renumbers
// them can't silently break parsing — keymap_test pins a few entries.
var ctrlKeys = map[rune]tcell.Key{
	'a': tcell.KeyCtrlA, 'b': tcell.KeyCtrlB, 'c': tcell.KeyCtrlC, 'd': tcell.KeyCtrlD,
	'e': tcell.KeyCtrlE, 'f': tcell.KeyCtrlF, 'g': tcell.KeyCtrlG, 'h': tcell.KeyCtrlH,
	'i': tcell.KeyCtrlI, 'j': tcell.KeyCtrlJ, 'k': tcell.KeyCtrlK, 'l': tcell.KeyCtrlL,
	'm': tcell.KeyCtrlM, 'n': tcell.KeyCtrlN, 'o': tcell.KeyCtrlO, 'p': tcell.KeyCtrlP,
	'q': tcell.KeyCtrlQ, 'r': tcell.KeyCtrlR, 's': tcell.KeyCtrlS, 't': tcell.KeyCtrlT,
	'u': tcell.KeyCtrlU, 'v': tcell.KeyCtrlV, 'w': tcell.KeyCtrlW, 'x': tcell.KeyCtrlX,
	'y': tcell.KeyCtrlY, 'z': tcell.KeyCtrlZ,
}

// ctrlLetters is the reverse of ctrlKeys, for String().
var ctrlLetters = func() map[tcell.Key]rune {
	m := make(map[tcell.Key]rune, len(ctrlKeys))
	for r, k := range ctrlKeys {
		m[k] = r
	}
	return m
}()

// namedKeys maps a keyspec base name to its (Key, Rune). Names that decode to a
// rune (only "space") carry a non-zero rune and Key==KeyRune.
var namedKeys = map[string]Binding{
	"enter":     {Key: tcell.KeyEnter},
	"return":    {Key: tcell.KeyEnter},
	"esc":       {Key: tcell.KeyEscape},
	"escape":    {Key: tcell.KeyEscape},
	"tab":       {Key: tcell.KeyTab},
	"backtab":   {Key: tcell.KeyBacktab},
	"space":     {Key: tcell.KeyRune, Rune: ' '},
	"up":        {Key: tcell.KeyUp},
	"down":      {Key: tcell.KeyDown},
	"left":      {Key: tcell.KeyLeft},
	"right":     {Key: tcell.KeyRight},
	"home":      {Key: tcell.KeyHome},
	"end":       {Key: tcell.KeyEnd},
	"pgup":      {Key: tcell.KeyPgUp},
	"pgdn":      {Key: tcell.KeyPgDn},
	"backspace": {Key: tcell.KeyBackspace2},
	"delete":    {Key: tcell.KeyDelete},
}

// keyNames is the reverse of namedKeys for the canonical display name of a
// special key. Aliases resolve to the primary spelling.
var keyNames = map[tcell.Key]string{
	tcell.KeyEnter:      "enter",
	tcell.KeyEscape:     "esc",
	tcell.KeyTab:        "tab",
	tcell.KeyBacktab:    "backtab",
	tcell.KeyUp:         "up",
	tcell.KeyDown:       "down",
	tcell.KeyLeft:       "left",
	tcell.KeyRight:      "right",
	tcell.KeyHome:       "home",
	tcell.KeyEnd:        "end",
	tcell.KeyPgUp:       "pgup",
	tcell.KeyPgDn:       "pgdn",
	tcell.KeyBackspace2: "backspace",
	tcell.KeyDelete:     "delete",
}

// isArrow reports whether k is one of the four arrow keys. Plain (unmodified)
// arrows are reserved for navigation and cannot be rebound.
func isArrow(k tcell.Key) bool {
	switch k {
	case tcell.KeyUp, tcell.KeyDown, tcell.KeyLeft, tcell.KeyRight:
		return true
	}
	return false
}

// isModifiable reports whether a named key may carry a cmd/shift modifier in a
// keyspec. These are the navigation keys the historical dispatch matched with a
// loose modifier test (cmd+arrows for pane/task nav; shift+arrows/pgup/pgdn/end
// for scrollback).
func isModifiable(k tcell.Key) bool {
	switch k {
	case tcell.KeyUp, tcell.KeyDown, tcell.KeyLeft, tcell.KeyRight,
		tcell.KeyPgUp, tcell.KeyPgDn, tcell.KeyHome, tcell.KeyEnd:
		return true
	}
	return false
}

// Parse converts a keyspec string ("ctrl+l", "cmd+up", "shift+down", "q", "?",
// "J", "space", "enter") into a Binding. See the package doc for the grammar.
//
// tcell quirks encoded here: ctrl+<letter> is a named key constant (NOT
// rune+ModCtrl); a modifier on a printable letter is rejected (tcell delivers
// shift+letter as the uppercase rune, and ctrl+letter is already covered); only
// arrow keys carry a modifier in the resulting Binding.
func Parse(spec string) (Binding, error) {
	s := strings.TrimSpace(spec)
	if s == "" {
		return Binding{}, fmt.Errorf("empty keyspec")
	}
	parts := strings.Split(s, "+")
	base := parts[len(parts)-1]
	mods := parts[:len(parts)-1]

	var ctrl, shift, ctrlAlt bool
	for _, m := range mods {
		switch strings.ToLower(strings.TrimSpace(m)) {
		case "ctrl", "control":
			ctrl = true
		case "cmd", "opt", "option", "alt":
			// All alias onto the iTerm2 Ctrl+Alt (mod-7) convention argus
			// round-trips for modified arrows — see gotchas/keybindings.md.
			ctrlAlt = true
		case "shift":
			shift = true
		default:
			return Binding{}, fmt.Errorf("unknown modifier %q in %q", m, spec)
		}
	}

	baseLower := strings.ToLower(strings.TrimSpace(base))

	// ctrl+<letter> → named control key.
	if ctrl && !ctrlAlt && !shift {
		if len(base) == 1 {
			r := rune(baseLower[0])
			if k, ok := ctrlKeys[r]; ok {
				return Binding{Key: k}, nil
			}
		}
		return Binding{}, fmt.Errorf("ctrl+%s is not supported (only ctrl+<letter>; use cmd+<arrow> for arrows)", base)
	}

	// Named base (arrows, enter, space, …) possibly with cmd/shift.
	if nb, ok := namedKeys[baseLower]; ok {
		if ctrlAlt || shift {
			if !isModifiable(nb.Key) {
				return Binding{}, fmt.Errorf("modifier on %q is not supported (only navigation keys)", base)
			}
			b := Binding{Key: nb.Key}
			if ctrlAlt {
				b.Mods |= tcell.ModCtrl | tcell.ModAlt
			}
			if shift {
				b.Mods |= tcell.ModShift
			}
			return b, nil
		}
		return nb, nil
	}

	// Bare single rune.
	if !ctrl && !ctrlAlt && !shift {
		runes := []rune(base)
		if len(runes) == 1 {
			return Binding{Key: tcell.KeyRune, Rune: runes[0]}, nil
		}
		return Binding{}, fmt.Errorf("unknown key %q", base)
	}

	// A modifier was set on a printable rune (e.g. shift+j, ctrl+/).
	return Binding{}, fmt.Errorf("modifier on %q is not supported (use the uppercase letter, or ctrl+<letter>)", base)
}

// String renders a Binding back to its canonical keyspec, for help display.
func (b Binding) String() string {
	if b.Key == tcell.KeyRune {
		if b.Rune == ' ' {
			return "space"
		}
		return string(b.Rune)
	}
	// Modified navigation keys.
	if b.Mods != 0 {
		name := keyNames[b.Key]
		switch {
		case b.Mods&(tcell.ModCtrl|tcell.ModAlt) != 0:
			return "cmd+" + name
		case b.Mods&tcell.ModShift != 0:
			return "shift+" + name
		default:
			return name
		}
	}
	// Named special keys take precedence over the ctrl-letter rendering so e.g.
	// Tab/Enter (which share numeric space with ctrl+i/ctrl+m on some terminals)
	// show as their names.
	if name, ok := keyNames[b.Key]; ok {
		return name
	}
	if letter, ok := ctrlLetters[b.Key]; ok {
		return "ctrl+" + string(letter)
	}
	return fmt.Sprintf("key(%d)", int(b.Key))
}
