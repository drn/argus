package tui

import (
	"fmt"
	"strings"

	"github.com/gdamore/tcell/v2"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/sandboxprop"
	"github.com/drn/argus/internal/tui/theme"
	"github.com/drn/argus/internal/tui/widget"
	"github.com/drn/argus/internal/uxlog"
)

// proposalBackend is how Settings lists and decides agent sandbox
// write-path proposals: a local DB adapter or the remote apistore.
type proposalBackend interface {
	ListSandboxProposals(status string) ([]*db.SandboxProposal, error)
	ApproveSandboxProposal(id, scope string, paths []string) (*sandboxprop.ApproveResult, error)
	RejectSandboxProposal(id string) error
}

// localProposals decides proposals straight through the DB; the local TUI is
// a human at the keyboard, the same trust level as editing Settings.
type localProposals struct{ d *db.DB }

func (l localProposals) ListSandboxProposals(status string) ([]*db.SandboxProposal, error) {
	return l.d.ListSandboxProposals(status)
}

func (l localProposals) ApproveSandboxProposal(id, scope string, paths []string) (*sandboxprop.ApproveResult, error) {
	return sandboxprop.Approve(l.d, id, scope, paths, "tui")
}

func (l localProposals) RejectSandboxProposal(id string) error {
	_, err := sandboxprop.Reject(l.d, id, "tui")
	return err
}

func (sv *SettingsView) proposals() proposalBackend {
	if d, ok := sv.database.(*db.DB); ok {
		return localProposals{d}
	}
	if p, ok := sv.database.(proposalBackend); ok {
		return p
	}
	return nil
}

// loadProposals refreshes the pending list; failures leave it empty.
func (sv *SettingsView) loadProposals() {
	sv.proposalList = nil
	b := sv.proposals()
	if b == nil {
		return
	}
	list, err := b.ListSandboxProposals(db.SandboxProposalPending)
	if err != nil {
		uxlog.Log("[sandbox-proposal] tui list failed: %v", err)
		return
	}
	sv.proposalList = list
	if len(list) > 0 {
		sv.warnings = append(sv.warnings, fmt.Sprintf("%d sandbox write-path proposal(s) pending — Settings > Sandbox", len(list)))
	}
}

// proposalScope is the scope Enter will approve with: the user's `e` toggle,
// else project when the proposal has one.
func (sv *SettingsView) proposalScope(p *db.SandboxProposal) string {
	if s, ok := sv.proposalScopes[p.ID]; ok {
		if s == sandboxprop.ScopeProject && p.Project == "" {
			return sandboxprop.ScopeGlobal
		}
		return s
	}
	if p.Project != "" {
		return sandboxprop.ScopeProject
	}
	return sandboxprop.ScopeGlobal
}

func (sv *SettingsView) currentRow() *settingsRow {
	if sv.cursor < 0 || sv.cursor >= len(sv.rows) {
		return nil
	}
	return &sv.rows[sv.cursor]
}

func (sv *SettingsView) proposalForRow(row *settingsRow) *db.SandboxProposal {
	if row == nil {
		return nil
	}
	for _, p := range sv.proposalList {
		if p.ID == row.key {
			return p
		}
	}
	return nil
}

func proposalRowLabel(p *db.SandboxProposal, scope string) string {
	paths := make([]string, len(p.Entries))
	warn := ""
	for i, e := range p.Entries {
		paths[i] = e.Path
		if e.Warning != "" {
			warn = "⚠ "
		}
	}
	to := "global"
	if scope == sandboxprop.ScopeProject {
		to = "project " + p.Project
	}
	return stripControlRunes(fmt.Sprintf("%sProposal → %s: %s", warn, to, strings.Join(paths, ", ")))
}

// handleApproveProposal approves the selected proposal at its current scope.
func (sv *SettingsView) handleApproveProposal() bool {
	p := sv.proposalForRow(sv.currentRow())
	b := sv.proposals()
	if p == nil || b == nil {
		return false
	}
	if proposalNeedsConfirm(p, sv.proposalScope(p)) && sv.proposalArmed != p.ID {
		sv.proposalArmed = p.ID
		sv.proposalMsg = "⚠ Flagged/global grant — press Enter again to confirm."
		return true
	}
	sv.proposalArmed = ""
	res, err := b.ApproveSandboxProposal(p.ID, sv.proposalScope(p), nil)
	if err != nil {
		sv.proposalMsg = "Approve failed: " + err.Error()
		uxlog.Log("[sandbox-proposal] tui approve failed: id=%s err=%v", p.ID, err)
	} else {
		sv.proposalMsg = fmt.Sprintf("Approved %d path(s) for %s — applies to NEW sessions; restart affected tasks.", len(res.Applied), res.Scope)
		if len(res.Warnings) > 0 {
			sv.proposalMsg += " Warning: " + strings.Join(res.Warnings, "; ")
		}
	}
	sv.Refresh()
	return true
}

// proposalNeedsConfirm is true when approval should take a second keypress:
// any flagged entry, or a global-scope grant.
func proposalNeedsConfirm(p *db.SandboxProposal, scope string) bool {
	if scope == sandboxprop.ScopeGlobal {
		return true
	}
	for _, e := range p.Entries {
		if e.Warning != "" {
			return true
		}
	}
	return false
}

// handleRejectProposal rejects the selected proposal.
func (sv *SettingsView) handleRejectProposal() bool {
	p := sv.proposalForRow(sv.currentRow())
	b := sv.proposals()
	if p == nil || b == nil {
		return false
	}
	sv.proposalArmed = ""
	if err := b.RejectSandboxProposal(p.ID); err != nil {
		sv.proposalMsg = "Reject failed: " + err.Error()
		uxlog.Log("[sandbox-proposal] tui reject failed: id=%s err=%v", p.ID, err)
	} else {
		sv.proposalMsg = "Rejected proposal; no config changed."
	}
	sv.Refresh()
	return true
}

// handleToggleProposalScope flips project/global for the selected proposal.
func (sv *SettingsView) handleToggleProposalScope() bool {
	p := sv.proposalForRow(sv.currentRow())
	if p == nil {
		return false
	}
	next := sandboxprop.ScopeGlobal
	if sv.proposalScope(p) == sandboxprop.ScopeGlobal && p.Project != "" {
		next = sandboxprop.ScopeProject
	}
	if sv.proposalScopes == nil {
		sv.proposalScopes = map[string]string{}
	}
	sv.proposalScopes[p.ID] = next
	sv.proposalArmed = ""
	sv.rebuildRows()
	return true
}

func (sv *SettingsView) renderProposalDetail(screen tcell.Screen, x, y, w, h int, row *settingsRow) {
	widget.DrawText(screen, x, y, w, "Agent write-path proposal", theme.StyleTitle)
	p := sv.proposalForRow(row)
	if p == nil {
		return
	}
	line := 2
	put := func(s string, st tcell.Style) {
		if line < h {
			widget.DrawText(screen, x, y+line, w, s, st)
			line++
		}
	}
	put("Task: "+stripControlRunes(p.TaskID)+"  Project: "+stripControlRunes(p.Project), theme.StyleDimmed)
	put("Approve scope: "+sv.proposalScope(p), tcell.StyleDefault.Foreground(theme.ColorTitle))
	line++
	for _, e := range p.Entries {
		put(stripControlRunes(e.Path), tcell.StyleDefault)
		if e.Reason != "" {
			put("  why: "+stripControlRunes(e.Reason), theme.StyleDimmed)
		}
		if e.Warning != "" {
			put("  ⚠ "+stripControlRunes(e.Warning), tcell.StyleDefault.Foreground(theme.ColorError))
		}
	}
	line++
	put("Enter approve   e toggle project/global   d reject", theme.StyleDimmed)
	put("Applies to NEW sessions only — restart affected tasks.", theme.StyleDimmed)
	if sv.proposalMsg != "" {
		line++
		put(sv.proposalMsg, tcell.StyleDefault.Foreground(theme.ColorTitle))
	}
}

func stripControlRunes(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, s)
}
