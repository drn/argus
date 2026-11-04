package sandboxprop

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

func setup(t *testing.T) (*db.DB, *model.Task) {
	t.Helper()
	d, err := db.OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })
	testutil.NoError(t, d.SetProject("op", config.Project{Path: "/tmp/op"}))
	return d, &model.Task{ID: "t1", Project: "op"}
}

func projectList(t *testing.T, d *db.DB) []string {
	t.Helper()
	ps, err := d.Projects()
	testutil.NoError(t, err)
	return ps["op"].Sandbox.ExtraWrite
}

func TestPropose_DoesNotChangeConfig(t *testing.T) {
	d, task := setup(t)
	res, err := Propose(d, task, []Item{{Path: "~/.gem", Reason: "gems"}})
	testutil.NoError(t, err)
	testutil.Equal(t, res.Proposal.Status, "pending")
	testutil.Equal(t, len(projectList(t, d)), 0)
	v, _ := d.GetConfigValue("sandbox.extra_write")
	testutil.Equal(t, v, "")
}

func TestPropose_Validation(t *testing.T) {
	d, task := setup(t)
	_, err := Propose(d, task, []Item{{Path: "~/.gem"}, {Path: "relative/dir"}})
	testutil.Contains(t, err.Error(), "relative/dir")
	l, _ := d.ListSandboxProposals("")
	testutil.Equal(t, len(l), 0)

	_, err = Propose(d, task, nil)
	testutil.Equal(t, err != nil, true)

	many := make([]Item, MaxPathsPerProposal+1)
	for i := range many {
		many[i].Path = "~/.p" + string(rune('a'+i))
	}
	_, err = Propose(d, task, many)
	testutil.Equal(t, err != nil, true)
}

func TestPropose_SensitiveAcceptedWithWarning(t *testing.T) {
	d, task := setup(t)
	res, err := Propose(d, task, []Item{{Path: "~/.aws/cache", Reason: "x"}})
	testutil.NoError(t, err)
	testutil.Equal(t, len(res.Warnings), 1)
	testutil.Equal(t, res.Proposal.Entries[0].Warning != "", true)
}

func TestPropose_MergeAndCap(t *testing.T) {
	d, task := setup(t)
	a, _ := Propose(d, task, []Item{{Path: "~/.gem"}, {Path: "~/.expo"}})
	b, err := Propose(d, task, []Item{{Path: "~/.expo"}, {Path: "~/.gem"}, {Path: "~/.gem"}})
	testutil.NoError(t, err)
	testutil.Equal(t, b.Merged, true)
	testutil.Equal(t, b.Proposal.ID, a.Proposal.ID)

	for i := 0; i < db.MaxPendingSandboxProposalsPerTask; i++ {
		Propose(d, task, []Item{{Path: "~/.cap" + string(rune('a'+i))}}) //nolint:errcheck
	}
	_, err = Propose(d, task, []Item{{Path: "~/.overflow"}})
	testutil.ErrorIs(t, err, db.ErrSandboxProposalCap)
}

func TestApprove_Project(t *testing.T) {
	d, task := setup(t)
	res, _ := Propose(d, task, []Item{{Path: "~/.gem"}, {Path: "~/.expo"}})
	out, err := Approve(d, res.Proposal.ID, "", nil, "master")
	testutil.NoError(t, err)
	testutil.Equal(t, out.Scope, ScopeProject)
	testutil.Equal(t, out.TakesEffect, TakesEffectNewSessions)
	testutil.DeepEqual(t, projectList(t, d), []string{"~/.gem", "~/.expo"})
	v, _ := d.GetConfigValue("sandbox.extra_write")
	testutil.Equal(t, v, "")
}

func TestApprove_GlobalEditedDedupe(t *testing.T) {
	d, task := setup(t)
	testutil.NoError(t, d.SetConfigValue("sandbox.extra_write", "~/.sketch"))
	res, _ := Propose(d, task, []Item{{Path: "~/.gem"}})
	out, err := Approve(d, res.Proposal.ID, ScopeGlobal, []string{"~/.sketch", "~/.cocoapods"}, "master")
	testutil.NoError(t, err)
	testutil.DeepEqual(t, out.Applied, []string{"~/.cocoapods"})
	testutil.DeepEqual(t, out.AlreadyGranted, []string{"~/.sketch"})
	v, _ := d.GetConfigValue("sandbox.extra_write")
	testutil.Equal(t, v, "~/.sketch,~/.cocoapods")
	testutil.Equal(t, len(projectList(t, d)), 0)
}

func TestApprove_EditRevalidated(t *testing.T) {
	d, task := setup(t)
	res, _ := Propose(d, task, []Item{{Path: "~/.gem"}})
	_, err := Approve(d, res.Proposal.ID, "", []string{"relative"}, "master")
	testutil.Equal(t, err != nil, true)
	p, _ := d.GetSandboxProposal(res.Proposal.ID)
	testutil.Equal(t, p.Status, "pending")
	testutil.Equal(t, len(projectList(t, d)), 0)
}

func TestApprove_WarningSurfaced(t *testing.T) {
	d, task := setup(t)
	res, _ := Propose(d, task, []Item{{Path: "~/.ssh/x"}})
	out, err := Approve(d, res.Proposal.ID, "", nil, "master")
	testutil.NoError(t, err)
	testutil.Equal(t, len(out.Warnings), 1)
}

func TestApprove_Errors(t *testing.T) {
	d, task := setup(t)
	_, err := Approve(d, "nope", "", nil, "m")
	testutil.ErrorIs(t, err, db.ErrSandboxProposalNotFound)

	res, _ := Propose(d, task, []Item{{Path: "~/.gem"}})
	_, err = Approve(d, res.Proposal.ID, "bogus", nil, "m")
	testutil.Equal(t, err != nil, true)
	_, err = Approve(d, res.Proposal.ID, "", []string{}, "m")
	testutil.Equal(t, err != nil, true)

	_, err = Approve(d, res.Proposal.ID, "", nil, "m")
	testutil.NoError(t, err)
	_, err = Approve(d, res.Proposal.ID, "", nil, "m")
	testutil.ErrorIs(t, err, db.ErrSandboxProposalDecided)
}

func TestApprove_NoProjectNeedsGlobal(t *testing.T) {
	d, _ := setup(t)
	res, _ := Propose(d, &model.Task{ID: "t2"}, []Item{{Path: "~/.gem"}})
	_, err := Approve(d, res.Proposal.ID, ScopeProject, nil, "m")
	testutil.Equal(t, err != nil, true)
	out, err := Approve(d, res.Proposal.ID, "", nil, "m")
	testutil.NoError(t, err)
	testutil.Equal(t, out.Scope, ScopeGlobal)
}

func TestApprove_MissingProjectRevertsNothingApplied(t *testing.T) {
	d, task := setup(t)
	res, _ := Propose(d, task, []Item{{Path: "~/.gem"}})
	testutil.NoError(t, d.DeleteProject("op"))
	_, err := Approve(d, res.Proposal.ID, "", nil, "m")
	testutil.Equal(t, err != nil, true)
	p, _ := d.GetSandboxProposal(res.Proposal.ID)
	testutil.Equal(t, p.Status, "pending")
}

func TestReject(t *testing.T) {
	d, task := setup(t)
	res, _ := Propose(d, task, []Item{{Path: "~/.gem"}})
	p, err := Reject(d, res.Proposal.ID, "master")
	testutil.NoError(t, err)
	testutil.Equal(t, p.Status, "rejected")
	testutil.Equal(t, len(projectList(t, d)), 0)
	_, err = Reject(d, res.Proposal.ID, "master")
	testutil.ErrorIs(t, err, db.ErrSandboxProposalDecided)
}

func TestValidPath_NormalizesAndRejectsInvisible(t *testing.T) {
	d, task := setup(t)
	_, err := Propose(d, task, []Item{{Path: "~/.gem/"}, {Path: "~/.gem"}})
	testutil.NoError(t, err)
	l, _ := d.ListSandboxProposals("")
	testutil.DeepEqual(t, []string{l[0].Entries[0].Path}, []string{"~/.gem"})
	testutil.Equal(t, len(l[0].Entries), 1)

	for _, bad := range []string{"~/.gem\u202e", "~/a\u200bb", "~/a\u2028b"} {
		_, err := Propose(d, task, []Item{{Path: bad}})
		testutil.Equal(t, err != nil, true)
	}
}

func TestPropose_SymlinkToSensitiveWarns(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	testutil.NoError(t, os.MkdirAll(filepath.Join(home, ".ssh"), 0o700))
	link := filepath.Join(t.TempDir(), "x")
	testutil.NoError(t, os.Symlink(filepath.Join(home, ".ssh"), link))
	d, task := setup(t)
	res, err := Propose(d, task, []Item{{Path: link}})
	testutil.NoError(t, err)
	testutil.Equal(t, len(res.Warnings), 1)
	testutil.Contains(t, res.Warnings[0], "resolves to")
}

func TestApprove_ConcurrentKeepsAllPaths(t *testing.T) {
	d, task := setup(t)
	var ids []string
	for _, p := range []string{"~/.a1", "~/.a2", "~/.a3", "~/.a4"} {
		r, err := Propose(d, task, []Item{{Path: p}})
		testutil.NoError(t, err)
		ids = append(ids, r.Proposal.ID)
	}
	var wg sync.WaitGroup
	for _, id := range ids {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := Approve(d, id, "", nil, "m")
			testutil.NoError(t, err)
		}()
	}
	wg.Wait()
	testutil.Equal(t, len(projectList(t, d)), 4)
}

func TestPropose_ParallelRespectsCap(t *testing.T) {
	d, task := setup(t)
	var wg sync.WaitGroup
	for i := 0; i < 25; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			Propose(d, task, []Item{{Path: fmt.Sprintf("~/.p%d", i)}}) //nolint:errcheck
		}()
	}
	wg.Wait()
	l, _ := d.ListSandboxProposals("pending")
	testutil.Equal(t, len(l), db.MaxPendingSandboxProposalsPerTask)
}
