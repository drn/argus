// Package hera renders the native Hera coordination view inside the Argus TUI
// (Milestone 6a of merging Hera into Argus — see
// context/plans/merge-hera-into-argus.md).
//
// 6a scope: the LEFT rail is built fully (sections, cursor nav, collapse,
// multi-binding fan-out, ready_to_close + role-status marks) and rendered
// READ-ONLY from the M1 db.DB hera store. The two RIGHT regions
// (coordinator / agent panes) are placeholders — their live PTY feeds land in
// 6b, and all mutations land in 6c. No code here mutates hera state.
package hera

// Focus identifies which of the three Hera-view regions currently holds
// keyboard focus. Ported from Hera's internal/view/focus.go FocusState. In 6a
// only FocusRail is interactive; FocusCoord/FocusAgent are reserved for the
// pane feeds wired in 6b.
type Focus int

const (
	// FocusRail is the left navigation rail (always present, the only
	// interactive region in 6a).
	FocusRail Focus = iota
	// FocusCoord is the middle coordinator/HERA pane (placeholder in 6a).
	FocusCoord
	// FocusAgent is the right details/AGENT pane (placeholder in 6a).
	FocusAgent
)

// FocusMachine tracks the focused region and which panes are present, so focus
// never lands on an absent region. Mirrors Hera's FocusMachine three-mode
// layout (D13) but trimmed to what 6a needs: the rail is always present, and
// Advance/Retreat step only through present regions. 6b wires Tab / Ctrl+arrows
// to Advance/Retreat once the panes carry live feeds; 6a renders the focused
// region's border highlight from State() and otherwise keeps focus on the rail.
type FocusMachine struct {
	state        Focus
	coordPresent bool
	agentPresent bool
}

// NewFocusMachine starts focused on the rail with both panes present (the
// normal split). Present-pane flags are reconciled by the page as the rail
// selection changes (6b).
func NewFocusMachine() *FocusMachine {
	return &FocusMachine{state: FocusRail, coordPresent: true, agentPresent: true}
}

// State returns the currently focused region.
func (f *FocusMachine) State() Focus { return f.state }

// SetCoordPresent records whether the coordinator pane is in the layout,
// bumping focus off it if it just disappeared. Returns true if the focused
// state changed (caller should repaint).
func (f *FocusMachine) SetCoordPresent(v bool) bool {
	f.coordPresent = v
	return f.rebalance()
}

// SetAgentPresent records whether the agent pane is in the layout, bumping
// focus off it if it just disappeared. Returns true if the focused state
// changed.
func (f *FocusMachine) SetAgentPresent(v bool) bool {
	f.agentPresent = v
	return f.rebalance()
}

// rebalance bumps focus off a now-absent region to the nearest present one.
// The rail is always present, so the fallback terminates.
func (f *FocusMachine) rebalance() bool {
	prev := f.state
	if f.state == FocusCoord && !f.coordPresent {
		if f.agentPresent {
			f.state = FocusAgent
		} else {
			f.state = FocusRail
		}
	}
	if f.state == FocusAgent && !f.agentPresent {
		if f.coordPresent {
			f.state = FocusCoord
		} else {
			f.state = FocusRail
		}
	}
	return f.state != prev
}

// Advance moves focus one region to the right, skipping absent regions. No-op
// from the right-most present region.
func (f *FocusMachine) Advance() {
	switch f.state {
	case FocusRail:
		switch {
		case f.coordPresent:
			f.state = FocusCoord
		case f.agentPresent:
			f.state = FocusAgent
		}
	case FocusCoord:
		if f.agentPresent {
			f.state = FocusAgent
		}
	case FocusAgent:
		// already right-most
	}
}

// Retreat moves focus one region to the left, skipping absent regions. No-op
// from the rail.
func (f *FocusMachine) Retreat() {
	switch f.state {
	case FocusAgent:
		switch {
		case f.coordPresent:
			f.state = FocusCoord
		default:
			f.state = FocusRail
		}
	case FocusCoord:
		f.state = FocusRail
	case FocusRail:
		// already left-most
	}
}

// ToRail forces focus back to the rail (Hera's Ctrl+Q "escape to rail").
func (f *FocusMachine) ToRail() { f.state = FocusRail }
