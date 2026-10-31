// Package hotkey detects a held, modifier-only global hotkey (e.g. Right
// Option) and reports press-and-hold / release transitions.
package hotkey

import (
	"errors"
	"fmt"
	"strings"
)

// CGEventFlags generic modifier bits.
const (
	FlagShift   uint64 = 0x20000
	FlagControl uint64 = 0x40000
	FlagOption  uint64 = 0x80000
	FlagCommand uint64 = 0x100000
	FlagFn      uint64 = 0x800000
)

// CGEventFlags device-dependent (sided) modifier bits.
const (
	FlagLeftControl  uint64 = 0x1
	FlagLeftShift    uint64 = 0x2
	FlagRightShift   uint64 = 0x4
	FlagLeftCommand  uint64 = 0x8
	FlagRightCommand uint64 = 0x10
	FlagLeftOption   uint64 = 0x20
	FlagRightOption  uint64 = 0x40
	FlagRightControl uint64 = 0x2000
)

const genericMask = FlagShift | FlagControl | FlagOption | FlagCommand | FlagFn

// ErrEmptyCombo is returned by ParseCombo for a blank combo string.
var ErrEmptyCombo = errors.New("hotkey: empty combo")

type modifier struct {
	name        string
	generic     uint64
	left, right uint64
}

// modifiers is in display order.
var modifiers = []modifier{
	{"ctrl", FlagControl, FlagLeftControl, FlagRightControl},
	{"option", FlagOption, FlagLeftOption, FlagRightOption},
	{"shift", FlagShift, FlagLeftShift, FlagRightShift},
	{"cmd", FlagCommand, FlagLeftCommand, FlagRightCommand},
	{"fn", FlagFn, 0, 0},
}

type side int

const (
	sideAny side = iota
	sideLeft
	sideRight
)

type tokenSpec struct {
	mod  int
	side side
}

var tokens = map[string]tokenSpec{
	"ctrl":          {0, sideAny},
	"control":       {0, sideAny},
	"left_ctrl":     {0, sideLeft},
	"left_control":  {0, sideLeft},
	"right_ctrl":    {0, sideRight},
	"right_control": {0, sideRight},
	"option":        {1, sideAny},
	"alt":           {1, sideAny},
	"left_option":   {1, sideLeft},
	"left_alt":      {1, sideLeft},
	"right_option":  {1, sideRight},
	"right_alt":     {1, sideRight},
	"shift":         {2, sideAny},
	"left_shift":    {2, sideLeft},
	"right_shift":   {2, sideRight},
	"cmd":           {3, sideAny},
	"command":       {3, sideAny},
	"left_cmd":      {3, sideLeft},
	"left_command":  {3, sideLeft},
	"right_cmd":     {3, sideRight},
	"right_command": {3, sideRight},
	"fn":            {4, sideAny},
}

type modReq struct {
	any, left, right bool
}

// Combo is a modifier-only hotkey. The zero value matches nothing.
type Combo struct {
	reqs    [5]modReq
	generic uint64 // generic bits that must be down (and no others)
	device  uint64 // sided bits that must be down
	forbid  uint64 // opposite-side bits that must be up
}

// ParseCombo parses a "+"-joined, case-insensitive list of modifier tokens,
// e.g. "right_option" or "ctrl+shift". Hyphens are accepted for underscores.
func ParseCombo(s string) (Combo, error) {
	if strings.TrimSpace(s) == "" {
		return Combo{}, ErrEmptyCombo
	}
	var c Combo
	for _, raw := range strings.Split(s, "+") {
		tok := strings.ReplaceAll(strings.ToLower(strings.TrimSpace(raw)), "-", "_")
		if tok == "" {
			return Combo{}, fmt.Errorf("hotkey: empty token in combo %q", s)
		}
		spec, ok := tokens[tok]
		if !ok {
			return Combo{}, fmt.Errorf("hotkey: unknown modifier %q in combo %q (modifier-only combos are supported)", tok, s)
		}
		r := &c.reqs[spec.mod]
		switch spec.side {
		case sideLeft:
			r.left = true
		case sideRight:
			r.right = true
		default:
			r.any = true
		}
	}
	for i, m := range modifiers {
		r := &c.reqs[i]
		if r.left || r.right {
			r.any = false
		}
		if !r.any && !r.left && !r.right {
			continue
		}
		c.generic |= m.generic
		if r.left {
			c.device |= m.left
		}
		if r.right {
			c.device |= m.right
		}
		if r.left != r.right {
			if r.left {
				c.forbid |= m.right
			} else {
				c.forbid |= m.left
			}
		}
	}
	return c, nil
}

// IsZero reports whether c is the zero Combo.
func (c Combo) IsZero() bool { return c.generic == 0 }

// Held reports whether every modifier the combo requires is down, regardless
// of any extra modifiers.
func (c Combo) Held(flags uint64) bool {
	if c.IsZero() {
		return false
	}
	return flags&c.generic == c.generic && flags&c.device == c.device
}

// Matches reports whether exactly the combo's modifiers are down: no other
// generic modifier, and for a one-sided modifier not the opposite side too.
// Caps Lock and non-modifier bits are ignored.
func (c Combo) Matches(flags uint64) bool {
	return c.Held(flags) && flags&genericMask == c.generic && flags&c.forbid == 0
}

// String renders the combo in canonical form, e.g. "ctrl+right_option".
func (c Combo) String() string {
	var parts []string
	for i, m := range modifiers {
		r := c.reqs[i]
		if r.left {
			parts = append(parts, "left_"+m.name)
		}
		if r.right {
			parts = append(parts, "right_"+m.name)
		}
		if r.any {
			parts = append(parts, m.name)
		}
	}
	return strings.Join(parts, "+")
}
