package tui

import (
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/sandboxprop"
	"github.com/drn/argus/internal/testutil"
)

func proposalSettings(t *testing.T, paths ...string) (*SettingsView, *db.DB, string) {
	t.Helper()
	d := testDB(t)
	testutil.NoError(t, d.SetProject("op", config.Project{Path: "/tmp/op"}))
	items := make([]sandboxprop.Item, len(paths))
	for i, p := range paths {
		items[i] = sandboxprop.Item{Path: p, Reason: "needed\x1b[31m"}
	}
	res, err := sandboxprop.Propose(d, &model.Task{ID: "t1", Project: "op"}, items)
	testutil.NoError(t, err)
	sv := NewSettingsView(d)
	sv.Refresh()
	sv.setCategory(catSandbox)
	sv.focus = focusPane
	selectRowInCategory(t, sv, catSandbox, srSandboxProposal, "")
	return sv, d, res.Proposal.ID
}

func propKey(sv *SettingsView, r rune) { sv.HandleKey(tcell.NewEventKey(tcell.KeyRune, r, 0)) }

func TestSettingsProposals_ApproveProject(t *testing.T) {
	sv, d, _ := proposalSettings(t, "~/.gem", "~/.ssh/x")
	sv.SetRect(0, 0, 100, 30)
	sv.Draw(drawSim(t))
	testutil.Equal(t, len(sv.warnings) > 0, true)

	sv.HandleKey(tcell.NewEventKey(tcell.KeyEnter, 0, 0))
	ps, _ := d.Projects()
	testutil.DeepEqual(t, ps["op"].Sandbox.ExtraWrite, []string{"~/.gem", "~/.ssh/x"})
	testutil.Contains(t, sv.proposalMsg, "NEW sessions")
	testutil.Contains(t, sv.proposalMsg, "Warning")
	testutil.Equal(t, len(sv.proposalList), 0)
}

func TestSettingsProposals_ToggleScopeGlobal(t *testing.T) {
	sv, d, _ := proposalSettings(t, "~/.gem")
	propKey(sv, 'e')
	testutil.Contains(t, sv.rows[sv.cursor].label, "global")
	propKey(sv, 'e')
	testutil.Contains(t, sv.rows[sv.cursor].label, "project op")
	propKey(sv, 'e')
	sv.HandleKey(tcell.NewEventKey(tcell.KeyEnter, 0, 0))
	v, _ := d.GetConfigValue("sandbox.extra_write")
	testutil.Equal(t, v, "~/.gem")
	ps, _ := d.Projects()
	testutil.Equal(t, len(ps["op"].Sandbox.ExtraWrite), 0)
}

func TestSettingsProposals_Reject(t *testing.T) {
	sv, d, id := proposalSettings(t, "~/.gem")
	propKey(sv, 'd')
	p, _ := d.GetSandboxProposal(id)
	testutil.Equal(t, p.Status, "rejected")
	ps, _ := d.Projects()
	testutil.Equal(t, len(ps["op"].Sandbox.ExtraWrite), 0)
	testutil.Contains(t, sv.proposalMsg, "Rejected")
}

func TestSettingsProposals_ApproveError(t *testing.T) {
	sv, d, id := proposalSettings(t, "~/.gem")
	testutil.NoError(t, d.DeleteProject("op"))
	sv.HandleKey(tcell.NewEventKey(tcell.KeyEnter, 0, 0))
	testutil.Contains(t, sv.proposalMsg, "Approve failed")
	p, _ := d.GetSandboxProposal(id)
	testutil.Equal(t, p.Status, "pending")
}

func TestSettingsProposals_NoBackendInert(t *testing.T) {
	sv, _, _ := proposalSettings(t, "~/.gem")
	sv.rows[sv.cursor].key = "missing"
	testutil.False(t, sv.handleApproveProposal())
	testutil.False(t, sv.handleRejectProposal())
	testutil.False(t, sv.handleToggleProposalScope())
}

type fakeRemoteProposals struct {
	list      []*db.SandboxProposal
	approved  string
	rejected  string
	approveEr error
}

func (f *fakeRemoteProposals) ListSandboxProposals(string) ([]*db.SandboxProposal, error) {
	return f.list, nil
}
func (f *fakeRemoteProposals) ApproveSandboxProposal(id, scope string, _ []string) (*sandboxprop.ApproveResult, error) {
	f.approved = id + ":" + scope
	return &sandboxprop.ApproveResult{Scope: scope}, f.approveEr
}
func (f *fakeRemoteProposals) RejectSandboxProposal(id string) error { f.rejected = id; return nil }

type remoteStoreStub struct {
	*db.DB
	fake *fakeRemoteProposals
}

func (r *remoteStoreStub) ListSandboxProposals(st string) ([]*db.SandboxProposal, error) {
	return r.fake.ListSandboxProposals(st)
}
func (r *remoteStoreStub) ApproveSandboxProposal(id, scope string, p []string) (*sandboxprop.ApproveResult, error) {
	return r.fake.ApproveSandboxProposal(id, scope, p)
}
func (r *remoteStoreStub) RejectSandboxProposal(id string) error {
	return r.fake.RejectSandboxProposal(id)
}

func TestSettingsProposals_Remote(t *testing.T) {
	fake := &fakeRemoteProposals{list: []*db.SandboxProposal{{ID: "p9", TaskID: "t", Entries: []db.SandboxProposalEntry{{Path: "~/.gem"}}}}}
	sv := NewSettingsView(&remoteStoreStub{DB: testDB(t), fake: fake})
	sv.Refresh()
	sv.setCategory(catSandbox)
	sv.focus = focusPane
	selectRowInCategory(t, sv, catSandbox, srSandboxProposal, "")
	testutil.Contains(t, sv.rows[sv.cursor].label, "global")
	sv.HandleKey(tcell.NewEventKey(tcell.KeyEnter, 0, 0))
	testutil.Equal(t, fake.approved, "p9:global")
	selectRowInCategory(t, sv, catSandbox, srSandboxProposal, "")
	propKey(sv, 'd')
	testutil.Equal(t, fake.rejected, "p9")
}
