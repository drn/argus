package hera

import (
	"errors"
	"testing"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

// fakeAcceptSender records Send calls so tests can assert whether a
// notification fired (and with what body/tldr) without a real hera.Service.
type fakeAcceptSender struct {
	calls []acceptSendCall
	err   error
}

type acceptSendCall struct {
	from, to int64
	body     string
	tldr     string
}

func (f *fakeAcceptSender) Send(fromRoleID, toRoleID int64, body, tldr string, inReplyTo *int64) (*db.HeraMessage, error) {
	f.calls = append(f.calls, acceptSendCall{from: fromRoleID, to: toRoleID, body: body, tldr: tldr})
	if f.err != nil {
		return nil, f.err
	}
	return &db.HeraMessage{ID: int64(len(f.calls))}, nil
}

func TestAcceptRole_InProgressFlipsToCompleteAndNotifies(t *testing.T) {
	d, err := db.OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })

	task, _, role := seedRecycleRole(t, d, db.HeraKindWorker, "orch", "worker-1", "/wt/w1", "argus/w1", "mission")
	_, _, coord := seedRecycleRole(t, d, db.HeraKindCoordinator, "orch", "coord", "/wt/coord", "argus/coord", "coordinate")

	sender := &fakeAcceptSender{}
	flipped, err := AcceptRole(d, sender, coord.ID, role.ID, "")
	testutil.NoError(t, err)
	testutil.Equal(t, flipped, true)

	got, err := d.Get(task.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, got.Status, model.StatusComplete)

	testutil.Equal(t, len(sender.calls), 1)
	testutil.Equal(t, sender.calls[0].from, coord.ID)
	testutil.Equal(t, sender.calls[0].to, role.ID)
	testutil.Contains(t, sender.calls[0].body, "accepted")
}

func TestAcceptRole_InReviewFlipsToComplete(t *testing.T) {
	d, err := db.OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })

	task, _, role := seedRecycleRole(t, d, db.HeraKindWorker, "orch", "worker-1", "/wt/w1", "argus/w1", "mission")
	task.Status = model.StatusInReview
	testutil.NoError(t, d.Update(task))
	_, _, coord := seedRecycleRole(t, d, db.HeraKindCoordinator, "orch", "coord", "/wt/coord", "argus/coord", "coordinate")

	sender := &fakeAcceptSender{}
	flipped, err := AcceptRole(d, sender, coord.ID, role.ID, "")
	testutil.NoError(t, err)
	testutil.Equal(t, flipped, true)

	got, err := d.Get(task.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, got.Status, model.StatusComplete)
	testutil.Equal(t, len(sender.calls), 1)
}

func TestAcceptRole_AlreadyCompleteIsNoOp(t *testing.T) {
	d, err := db.OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })

	task, _, role := seedRecycleRole(t, d, db.HeraKindWorker, "orch", "worker-1", "/wt/w1", "argus/w1", "mission")
	task.Status = model.StatusComplete
	testutil.NoError(t, d.Update(task))
	_, _, coord := seedRecycleRole(t, d, db.HeraKindCoordinator, "orch", "coord", "/wt/coord", "argus/coord", "coordinate")

	sender := &fakeAcceptSender{}
	flipped, err := AcceptRole(d, sender, coord.ID, role.ID, "")
	testutil.NoError(t, err)
	testutil.Equal(t, flipped, false)
	testutil.Equal(t, len(sender.calls), 0)

	got, err := d.Get(task.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, got.Status, model.StatusComplete)
}

func TestAcceptRole_CustomMessageAppended(t *testing.T) {
	d, err := db.OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })

	_, _, role := seedRecycleRole(t, d, db.HeraKindWorker, "orch", "worker-1", "/wt/w1", "argus/w1", "mission")
	_, _, coord := seedRecycleRole(t, d, db.HeraKindCoordinator, "orch", "coord", "/wt/coord", "argus/coord", "coordinate")

	sender := &fakeAcceptSender{}
	_, err = AcceptRole(d, sender, coord.ID, role.ID, "great work on the edge cases")
	testutil.NoError(t, err)
	testutil.Equal(t, len(sender.calls), 1)
	testutil.Contains(t, sender.calls[0].body, "great work on the edge cases")
}

func TestAcceptRole_EmptyMessageSendsDefaultBodyOnly(t *testing.T) {
	d, err := db.OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })

	_, _, role := seedRecycleRole(t, d, db.HeraKindWorker, "orch", "worker-1", "/wt/w1", "argus/w1", "mission")
	_, _, coord := seedRecycleRole(t, d, db.HeraKindCoordinator, "orch", "coord", "/wt/coord", "argus/coord", "coordinate")

	sender := &fakeAcceptSender{}
	_, err = AcceptRole(d, sender, coord.ID, role.ID, "")
	testutil.NoError(t, err)
	testutil.Equal(t, sender.calls[0].body, acceptDefaultBody)
}

// TestAcceptRole_DefaultBodyRequiresAReply pins the closed-loop check-in
// wording: the message must explicitly instruct the recipient to reply with
// one of the three outcomes, and must state that the reply never
// auto-reopens the task, so this can't silently regress back into a one-way
// notice.
func TestAcceptRole_DefaultBodyRequiresAReply(t *testing.T) {
	d, err := db.OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })

	_, _, role := seedRecycleRole(t, d, db.HeraKindWorker, "orch", "worker-1", "/wt/w1", "argus/w1", "mission")
	_, _, coord := seedRecycleRole(t, d, db.HeraKindCoordinator, "orch", "coord", "/wt/coord", "argus/coord", "coordinate")

	sender := &fakeAcceptSender{}
	_, err = AcceptRole(d, sender, coord.ID, role.ID, "")
	testutil.NoError(t, err)

	body := sender.calls[0].body
	testutil.Contains(t, body, "reply")
	testutil.Contains(t, body, "winding down")
	testutil.Contains(t, body, "more work to do")
	testutil.Contains(t, body, "question")
	testutil.Contains(t, body, "does not automatically reopen")
	testutil.Contains(t, sender.calls[0].tldr, "confirm")
}

func TestAcceptRole_SendFailurePropagatesButStatusStands(t *testing.T) {
	d, err := db.OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })

	task, _, role := seedRecycleRole(t, d, db.HeraKindWorker, "orch", "worker-1", "/wt/w1", "argus/w1", "mission")
	_, _, coord := seedRecycleRole(t, d, db.HeraKindCoordinator, "orch", "coord", "/wt/coord", "argus/coord", "coordinate")

	sender := &fakeAcceptSender{err: errors.New("delivery boom")}
	flipped, err := AcceptRole(d, sender, coord.ID, role.ID, "")
	if err == nil {
		t.Fatal("expected the send failure to propagate")
	}
	testutil.Equal(t, flipped, true)

	got, err := d.Get(task.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, got.Status, model.StatusComplete)
}

func TestAcceptRole_NoLiveBindingErrors(t *testing.T) {
	d, err := db.OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })

	orch, err := d.CreateHeraOrchestrator("orch", "")
	testutil.NoError(t, err)
	planned, err := d.CreateHeraPlannedRole(db.CreateHeraRoleInput{
		OrchestratorID: orch.ID, Name: "planned-1", Kind: db.HeraKindWorker, ArgusProject: "proj", Prompt: "later",
	})
	testutil.NoError(t, err)

	sender := &fakeAcceptSender{}
	_, err = AcceptRole(d, sender, 999, planned.ID, "")
	if err == nil {
		t.Fatal("expected an error resolving a role with no live binding")
	}
	testutil.Equal(t, len(sender.calls), 0)
}
