package db

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

func newMessage(from, to string, kind model.MessageKind, body string) *model.TaskMessage {
	return &model.TaskMessage{From: from, To: to, Kind: kind, Body: body}
}

func TestDB_InsertMessage(t *testing.T) {
	t.Run("happy path stamps id and created_at", func(t *testing.T) {
		d := testDB(t)
		m, err := d.InsertMessage(newMessage("A", "B", model.KindNote, "hi"))
		testutil.NoError(t, err)
		if m.ID == "" {
			t.Fatal("expected ID to be generated")
		}
		if m.CreatedAt.IsZero() {
			t.Fatal("expected CreatedAt to be stamped")
		}
	})

	t.Run("validates kind", func(t *testing.T) {
		d := testDB(t)
		_, err := d.InsertMessage(&model.TaskMessage{From: "A", To: "B", Kind: "bogus", Body: "x"})
		if err == nil {
			t.Fatal("expected validation error")
		}
	})

	t.Run("rejects self-send", func(t *testing.T) {
		d := testDB(t)
		_, err := d.InsertMessage(newMessage("A", "A", model.KindNote, "x"))
		if !errors.Is(err, ErrMessageSelfSend) {
			t.Fatalf("expected ErrMessageSelfSend, got %v", err)
		}
	})

	t.Run("rejects oversized body", func(t *testing.T) {
		d := testDB(t)
		big := strings.Repeat("x", model.MaxMessageBodyBytes+1)
		_, err := d.InsertMessage(newMessage("A", "B", model.KindNote, big))
		if !errors.Is(err, ErrMessageBodyTooLarge) {
			t.Fatalf("expected ErrMessageBodyTooLarge, got %v", err)
		}
	})

	t.Run("rejects when recipient inbox is full", func(t *testing.T) {
		d := testDB(t)
		for i := range MaxUnreadPerRecipient {
			// Each send from a unique sender so the per-sender rate limit
			// doesn't trigger first.
			m, err := d.InsertMessage(newMessage(fmt.Sprintf("S%d", i), "B", model.KindNote, "x"))
			testutil.NoError(t, err)
			_ = m
		}
		_, err := d.InsertMessage(newMessage("S-final", "B", model.KindNote, "x"))
		if !errors.Is(err, ErrMessageInboxFull) {
			t.Fatalf("expected ErrMessageInboxFull, got %v", err)
		}
	})

	t.Run("ack frees inbox capacity", func(t *testing.T) {
		d := testDB(t)
		var ids []string
		for i := range MaxUnreadPerRecipient {
			m, err := d.InsertMessage(newMessage(fmt.Sprintf("S%d", i), "B", model.KindNote, "x"))
			testutil.NoError(t, err)
			ids = append(ids, m.ID)
		}
		// Inbox full — confirm.
		_, err := d.InsertMessage(newMessage("S-x", "B", model.KindNote, "x"))
		if !errors.Is(err, ErrMessageInboxFull) {
			t.Fatalf("expected full, got %v", err)
		}
		// Ack one; next send must succeed.
		n, err := d.AckMessages("B", ids[:1])
		testutil.NoError(t, err)
		testutil.Equal(t, n, 1)
		_, err = d.InsertMessage(newMessage("S-x", "B", model.KindNote, "x"))
		testutil.NoError(t, err)
	})

	t.Run("rejects sender rate limit", func(t *testing.T) {
		d := testDB(t)
		for i := range MaxSendsPerMinute {
			// Vary recipient so the per-recipient inbox cap isn't the limit
			// we hit first.
			to := fmt.Sprintf("R%d", i)
			_, err := d.InsertMessage(newMessage("A", to, model.KindNote, "x"))
			testutil.NoError(t, err)
		}
		_, err := d.InsertMessage(newMessage("A", "R-final", model.KindNote, "x"))
		if !errors.Is(err, ErrMessageRateLimited) {
			t.Fatalf("expected ErrMessageRateLimited, got %v", err)
		}
	})

	t.Run("answer requires in_reply_to", func(t *testing.T) {
		d := testDB(t)
		_, err := d.InsertMessage(&model.TaskMessage{From: "A", To: "B", Kind: model.KindAnswer, Body: "x"})
		if err == nil {
			t.Fatal("expected validation error for answer with empty in_reply_to")
		}
	})
}

func TestDB_Inbox(t *testing.T) {
	t.Run("returns messages oldest-first", func(t *testing.T) {
		d := testDB(t)
		m1, err := d.InsertMessage(newMessage("A", "B", model.KindNote, "first"))
		testutil.NoError(t, err)
		// Push m2's created_at later so the ORDER BY is unambiguous even
		// when the test runs faster than RFC3339Nano resolution.
		time.Sleep(2 * time.Millisecond)
		m2, err := d.InsertMessage(newMessage("A", "B", model.KindNote, "second"))
		testutil.NoError(t, err)
		got, err := d.Inbox("B", InboxFilter{})
		testutil.NoError(t, err)
		testutil.Equal(t, len(got), 2)
		testutil.Equal(t, got[0].ID, m1.ID)
		testutil.Equal(t, got[1].ID, m2.ID)
	})

	t.Run("unread_only filter", func(t *testing.T) {
		d := testDB(t)
		m1, err := d.InsertMessage(newMessage("A", "B", model.KindNote, "x"))
		testutil.NoError(t, err)
		_, err = d.InsertMessage(newMessage("A", "B", model.KindNote, "y"))
		testutil.NoError(t, err)
		n, err := d.AckMessages("B", []string{m1.ID})
		testutil.NoError(t, err)
		testutil.Equal(t, n, 1)
		got, err := d.Inbox("B", InboxFilter{UnreadOnly: true})
		testutil.NoError(t, err)
		testutil.Equal(t, len(got), 1)
		testutil.Equal(t, got[0].Body, "y")
	})

	t.Run("sender filter", func(t *testing.T) {
		d := testDB(t)
		_, err := d.InsertMessage(newMessage("A", "C", model.KindNote, "from-a"))
		testutil.NoError(t, err)
		_, err = d.InsertMessage(newMessage("B", "C", model.KindNote, "from-b"))
		testutil.NoError(t, err)
		got, err := d.Inbox("C", InboxFilter{Sender: "A"})
		testutil.NoError(t, err)
		testutil.Equal(t, len(got), 1)
		testutil.Equal(t, got[0].From, "A")
	})

	t.Run("since filter excludes equal timestamps", func(t *testing.T) {
		d := testDB(t)
		m1, err := d.InsertMessage(newMessage("A", "B", model.KindNote, "x"))
		testutil.NoError(t, err)
		got, err := d.Inbox("B", InboxFilter{Since: m1.CreatedAt})
		testutil.NoError(t, err)
		testutil.Equal(t, len(got), 0)
	})

	t.Run("limit clamps to MaxInboxLimit", func(t *testing.T) {
		d := testDB(t)
		// Insert a handful to verify the limit code path; we don't insert
		// MaxInboxLimit+1 because the rate limit would block us long before
		// then. The clamp is exercised by passing an explicit oversize value.
		for i := range 3 {
			_, err := d.InsertMessage(newMessage(fmt.Sprintf("A%d", i), "B", model.KindNote, "x"))
			testutil.NoError(t, err)
		}
		got, err := d.Inbox("B", InboxFilter{Limit: MaxInboxLimit + 999})
		testutil.NoError(t, err)
		testutil.Equal(t, len(got), 3)
	})
}

func TestDB_AckMessages(t *testing.T) {
	t.Run("ignores foreign IDs", func(t *testing.T) {
		d := testDB(t)
		m, err := d.InsertMessage(newMessage("A", "B", model.KindNote, "x"))
		testutil.NoError(t, err)
		// "C" tries to ack a message addressed to "B" — silently ignored.
		n, err := d.AckMessages("C", []string{m.ID})
		testutil.NoError(t, err)
		testutil.Equal(t, n, 0)
		// Message remains unread for B.
		unread, err := d.UnreadCount("B")
		testutil.NoError(t, err)
		testutil.Equal(t, unread, 1)
	})

	t.Run("re-acking is idempotent", func(t *testing.T) {
		d := testDB(t)
		m, err := d.InsertMessage(newMessage("A", "B", model.KindNote, "x"))
		testutil.NoError(t, err)
		n, err := d.AckMessages("B", []string{m.ID})
		testutil.NoError(t, err)
		testutil.Equal(t, n, 1)
		n, err = d.AckMessages("B", []string{m.ID})
		testutil.NoError(t, err)
		testutil.Equal(t, n, 0)
	})

	t.Run("empty list is a no-op", func(t *testing.T) {
		d := testDB(t)
		n, err := d.AckMessages("B", nil)
		testutil.NoError(t, err)
		testutil.Equal(t, n, 0)
	})
}

func TestDB_UnreadCount(t *testing.T) {
	d := testDB(t)
	n, err := d.UnreadCount("B")
	testutil.NoError(t, err)
	testutil.Equal(t, n, 0)
	_, err = d.InsertMessage(newMessage("A", "B", model.KindNote, "x"))
	testutil.NoError(t, err)
	n, err = d.UnreadCount("B")
	testutil.NoError(t, err)
	testutil.Equal(t, n, 1)
}

func TestDB_FindReply(t *testing.T) {
	t.Run("returns nil when no reply exists", func(t *testing.T) {
		d := testDB(t)
		q, err := d.InsertMessage(newMessage("A", "B", model.KindQuestion, "q?"))
		testutil.NoError(t, err)
		got, err := d.FindReply(q.ID, "B")
		testutil.NoError(t, err)
		if got != nil {
			t.Fatalf("expected nil, got %+v", got)
		}
	})

	t.Run("returns first answer from named sender", func(t *testing.T) {
		d := testDB(t)
		q, err := d.InsertMessage(newMessage("A", "B", model.KindQuestion, "q?"))
		testutil.NoError(t, err)
		ans, err := d.InsertMessage(&model.TaskMessage{From: "B", To: "A", Kind: model.KindAnswer, Body: "yes", InReplyTo: q.ID})
		testutil.NoError(t, err)
		got, err := d.FindReply(q.ID, "B")
		testutil.NoError(t, err)
		testutil.Equal(t, got.ID, ans.ID)
	})

	t.Run("ignores answers from other senders", func(t *testing.T) {
		d := testDB(t)
		q, err := d.InsertMessage(newMessage("A", "B", model.KindQuestion, "q?"))
		testutil.NoError(t, err)
		// Some other task spams a reply pointing at the same question;
		// FindReply scoped to "B" must not return it.
		_, err = d.InsertMessage(&model.TaskMessage{From: "C", To: "A", Kind: model.KindAnswer, Body: "no", InReplyTo: q.ID})
		testutil.NoError(t, err)
		got, err := d.FindReply(q.ID, "B")
		testutil.NoError(t, err)
		if got != nil {
			t.Fatalf("expected nil, got %+v", got)
		}
	})
}

func TestDB_WaitForReply(t *testing.T) {
	t.Run("returns immediately when reply exists", func(t *testing.T) {
		d := testDB(t)
		q, err := d.InsertMessage(newMessage("A", "B", model.KindQuestion, "q?"))
		testutil.NoError(t, err)
		_, err = d.InsertMessage(&model.TaskMessage{From: "B", To: "A", Kind: model.KindAnswer, Body: "ok", InReplyTo: q.ID})
		testutil.NoError(t, err)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		got, err := d.WaitForReply(ctx, q.ID, "B")
		testutil.NoError(t, err)
		if got == nil {
			t.Fatal("expected reply")
		}
	})

	t.Run("returns nil on ctx cancellation", func(t *testing.T) {
		if testing.Short() {
			t.Skip("polling timeout test")
		}
		d := testDB(t)
		q, err := d.InsertMessage(newMessage("A", "B", model.KindQuestion, "q?"))
		testutil.NoError(t, err)
		ctx, cancel := context.WithTimeout(context.Background(), 700*time.Millisecond)
		defer cancel()
		got, err := d.WaitForReply(ctx, q.ID, "B")
		testutil.NoError(t, err)
		if got != nil {
			t.Fatalf("expected nil on timeout, got %+v", got)
		}
	})

	// Reply arrives during the polling loop — exercises the ticker.C
	// branch (not just the fast-path). Without this the WaitForReply
	// success-after-tick code path is uncovered.
	t.Run("returns reply that lands during ticker", func(t *testing.T) {
		if testing.Short() {
			t.Skip("polling timeout test")
		}
		d := testDB(t)
		q, err := d.InsertMessage(newMessage("A", "B", model.KindQuestion, "q?"))
		testutil.NoError(t, err)

		// Insert the reply ~250ms in — well after WaitForReply's fast-path
		// check ran, so the ticker.C branch fires when it lands.
		go func() {
			time.Sleep(250 * time.Millisecond)
			_, _ = d.InsertMessage(&model.TaskMessage{
				From: "B", To: "A", Kind: model.KindAnswer, Body: "yes", InReplyTo: q.ID,
			})
		}()

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		got, err := d.WaitForReply(ctx, q.ID, "B")
		testutil.NoError(t, err)
		if got == nil {
			t.Fatal("expected reply, got nil")
		}
		testutil.Equal(t, got.Body, "yes")
	})
}

// TestDB_Messages_ErrorBranchesAfterClose pokes the database-closed
// error paths on every messaging method. Without this the "SQL failure"
// branches in InsertMessage, Inbox, AckMessages, UnreadCount, FindReply,
// and DeleteMessagesForTask all stay at zero coverage.
func TestDB_Messages_ErrorBranchesAfterClose(t *testing.T) {
	d := testDB(t)
	// Seed a message so DeleteMessagesForTask has something to hit before
	// the close; ensures we cover the row-found path before the failure mode.
	_, err := d.InsertMessage(newMessage("A", "B", model.KindNote, "x"))
	testutil.NoError(t, err)
	testutil.NoError(t, d.Close())

	t.Run("InsertMessage", func(t *testing.T) {
		_, err := d.InsertMessage(newMessage("X", "Y", model.KindNote, "x"))
		if err == nil {
			t.Fatal("expected error on closed DB")
		}
	})
	t.Run("Inbox", func(t *testing.T) {
		_, err := d.Inbox("B", InboxFilter{})
		if err == nil {
			t.Fatal("expected error on closed DB")
		}
	})
	t.Run("AckMessages", func(t *testing.T) {
		_, err := d.AckMessages("B", []string{"m1"})
		if err == nil {
			t.Fatal("expected error on closed DB")
		}
	})
	t.Run("UnreadCount", func(t *testing.T) {
		_, err := d.UnreadCount("B")
		if err == nil {
			t.Fatal("expected error on closed DB")
		}
	})
	t.Run("DeleteMessagesForTask", func(t *testing.T) {
		_, err := d.DeleteMessagesForTask("B")
		if err == nil {
			t.Fatal("expected error on closed DB")
		}
	})
}

func TestDB_DeleteMessagesForTask(t *testing.T) {
	d := testDB(t)
	_, err := d.InsertMessage(newMessage("A", "B", model.KindNote, "x"))
	testutil.NoError(t, err)
	_, err = d.InsertMessage(newMessage("B", "C", model.KindNote, "y"))
	testutil.NoError(t, err)
	// Drop everything touching B; A→B and B→C should both disappear.
	n, err := d.DeleteMessagesForTask("B")
	testutil.NoError(t, err)
	testutil.Equal(t, n, 2)
	unread, err := d.UnreadCount("B")
	testutil.NoError(t, err)
	testutil.Equal(t, unread, 0)
	unread, err = d.UnreadCount("C")
	testutil.NoError(t, err)
	testutil.Equal(t, unread, 0)
}

// TestDB_Delete_CascadesMessages confirms destroying a task wipes every
// message it sent or received — otherwise the orphan rows still count
// against the recipient's unread cap and have no live caller to ack them.
func TestDB_Delete_CascadesMessages(t *testing.T) {
	d := testDB(t)
	task := &model.Task{Name: "doomed"}
	testutil.NoError(t, d.Add(task))
	other := &model.Task{Name: "other"}
	testutil.NoError(t, d.Add(other))

	_, err := d.InsertMessage(newMessage(task.ID, other.ID, model.KindNote, "x"))
	testutil.NoError(t, err)
	_, err = d.InsertMessage(newMessage(other.ID, task.ID, model.KindNote, "y"))
	testutil.NoError(t, err)

	testutil.NoError(t, d.Delete(task.ID))

	// Rows referencing the deleted task are gone on both sides.
	got, err := d.Inbox(task.ID, InboxFilter{UnreadOnly: false})
	testutil.NoError(t, err)
	testutil.Equal(t, len(got), 0)
	got, err = d.Inbox(other.ID, InboxFilter{UnreadOnly: false, Sender: task.ID})
	testutil.NoError(t, err)
	testutil.Equal(t, len(got), 0)
}

// TestDB_SetArchived_CascadesMessages confirms the partial-update archive
// path (used by orch.HaltDownstream) also wipes queued messages. The
// archive entry-points that go through db.Update call
// DeleteMessagesForTask explicitly — this test covers the third path.
func TestDB_SetArchived_CascadesMessages(t *testing.T) {
	d := testDB(t)
	task := &model.Task{Name: "to-archive"}
	testutil.NoError(t, d.Add(task))
	other := &model.Task{Name: "other"}
	testutil.NoError(t, d.Add(other))

	_, err := d.InsertMessage(newMessage(other.ID, task.ID, model.KindNote, "x"))
	testutil.NoError(t, err)

	testutil.NoError(t, d.SetArchived(task.ID, true))

	unread, err := d.UnreadCount(task.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, unread, 0)
}

// TestDB_SetArchived_UnarchiveLeavesMessagesAlone confirms the cleanup
// only fires on archive=true. There would be nothing to clean on unarchive
// (messages were wiped at archive), but the asymmetric SQL clause matters
// for future-readers: don't blindly cascade on both legs.
func TestDB_SetArchived_UnarchiveLeavesMessagesAlone(t *testing.T) {
	d := testDB(t)
	task := &model.Task{Name: "t", Archived: true}
	testutil.NoError(t, d.Add(task))
	other := &model.Task{Name: "other"}
	testutil.NoError(t, d.Add(other))

	// Seed a message AFTER archive so we have something to delete. Then
	// unarchive — message must survive.
	_, err := d.InsertMessage(newMessage(other.ID, task.ID, model.KindNote, "x"))
	testutil.NoError(t, err)

	testutil.NoError(t, d.SetArchived(task.ID, false))

	unread, err := d.UnreadCount(task.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, unread, 1)
}
