package db

import (
	"fmt"
	"strings"
	"testing"

	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

// mkMsg is a test helper that sends a message and fails the test on error.
func mkMsg(t *testing.T, d *DB, from, to int64, body, tldr string, inReplyTo *int64) *HeraMessage {
	t.Helper()
	m, err := d.SendHeraMessage(from, to, body, tldr, inReplyTo)
	testutil.NoError(t, err)
	return m
}

func TestHeraMessageSend(t *testing.T) {
	t.Run("happy path", func(t *testing.T) {
		d := heraTestDB(t)
		o := mkOrch(t, d, "orch")
		r1 := mkRole(t, d, o.ID, "coord", HeraKindCoordinator)
		r2 := mkRole(t, d, o.ID, "worker", HeraKindWorker)

		m := mkMsg(t, d, r1.ID, r2.ID, "hello", "hello summary", nil)
		testutil.Equal(t, m.FromRoleID, r1.ID)
		testutil.Equal(t, m.ToRoleID, r2.ID)
		testutil.Equal(t, m.Body, "hello")
		testutil.Equal(t, m.Tldr, "hello summary")
		testutil.Nil(t, m.InReplyTo)
		testutil.Equal(t, m.DeliveryMode, HeraDeliveryPending)
		testutil.Nil(t, m.ReadAt)
		testutil.Nil(t, m.DeliveredAt)
		if m.ID == 0 {
			t.Fatal("expected non-zero ID")
		}
		if m.SentAt.IsZero() {
			t.Fatal("expected non-zero SentAt")
		}
	})

	t.Run("with in_reply_to", func(t *testing.T) {
		d := heraTestDB(t)
		o := mkOrch(t, d, "orch")
		r1 := mkRole(t, d, o.ID, "coord", HeraKindCoordinator)
		r2 := mkRole(t, d, o.ID, "worker", HeraKindWorker)

		parent := mkMsg(t, d, r1.ID, r2.ID, "question", "q tldr", nil)
		reply := mkMsg(t, d, r2.ID, r1.ID, "answer", "a tldr", &parent.ID)
		testutil.Equal(t, *reply.InReplyTo, parent.ID)
	})

	t.Run("self-send rejected", func(t *testing.T) {
		d := heraTestDB(t)
		o := mkOrch(t, d, "orch")
		r := mkRole(t, d, o.ID, "coord", HeraKindCoordinator)

		_, err := d.SendHeraMessage(r.ID, r.ID, "hi", "hi", nil)
		testutil.ErrorIs(t, err, ErrHeraMessageSelfSend)
	})

	t.Run("body too large", func(t *testing.T) {
		d := heraTestDB(t)
		o := mkOrch(t, d, "orch")
		r1 := mkRole(t, d, o.ID, "coord", HeraKindCoordinator)
		r2 := mkRole(t, d, o.ID, "worker", HeraKindWorker)

		big := strings.Repeat("x", model.MaxMessageBodyBytes+1)
		_, err := d.SendHeraMessage(r1.ID, r2.ID, big, "summary", nil)
		testutil.ErrorIs(t, err, ErrHeraMessageBodyTooLarge)
	})

	t.Run("tldr required", func(t *testing.T) {
		d := heraTestDB(t)
		o := mkOrch(t, d, "orch")
		r1 := mkRole(t, d, o.ID, "coord", HeraKindCoordinator)
		r2 := mkRole(t, d, o.ID, "worker", HeraKindWorker)

		_, err := d.SendHeraMessage(r1.ID, r2.ID, "body", "", nil)
		testutil.ErrorIs(t, err, ErrHeraMessageTldrRequired)
	})

	t.Run("tldr too long", func(t *testing.T) {
		d := heraTestDB(t)
		o := mkOrch(t, d, "orch")
		r1 := mkRole(t, d, o.ID, "coord", HeraKindCoordinator)
		r2 := mkRole(t, d, o.ID, "worker", HeraKindWorker)

		long := strings.Repeat("a", HeraMaxTldrLen+1)
		_, err := d.SendHeraMessage(r1.ID, r2.ID, "body", long, nil)
		testutil.ErrorIs(t, err, ErrHeraMessageTldrTooLong)
	})

	t.Run("tldr multiline rejected", func(t *testing.T) {
		d := heraTestDB(t)
		o := mkOrch(t, d, "orch")
		r1 := mkRole(t, d, o.ID, "coord", HeraKindCoordinator)
		r2 := mkRole(t, d, o.ID, "worker", HeraKindWorker)

		_, err := d.SendHeraMessage(r1.ID, r2.ID, "body", "line1\nline2", nil)
		testutil.ErrorIs(t, err, ErrHeraMessageTldrMultiline)
	})

	t.Run("recipient missing", func(t *testing.T) {
		d := heraTestDB(t)
		o := mkOrch(t, d, "orch")
		r1 := mkRole(t, d, o.ID, "coord", HeraKindCoordinator)

		_, err := d.SendHeraMessage(r1.ID, 99999, "body", "tldr", nil)
		testutil.ErrorIs(t, err, ErrHeraMessageRecipientInvalid)
	})

	t.Run("recipient archived rejected", func(t *testing.T) {
		d := heraTestDB(t)
		o := mkOrch(t, d, "orch")
		r1 := mkRole(t, d, o.ID, "coord", HeraKindCoordinator)
		r2 := mkRole(t, d, o.ID, "worker", HeraKindWorker)

		testutil.NoError(t, d.ArchiveHeraRole(r2.ID))
		_, err := d.SendHeraMessage(r1.ID, r2.ID, "body", "tldr", nil)
		testutil.ErrorIs(t, err, ErrHeraMessageRecipientInvalid)
	})

	t.Run("invalid in_reply_to", func(t *testing.T) {
		d := heraTestDB(t)
		o := mkOrch(t, d, "orch")
		r1 := mkRole(t, d, o.ID, "coord", HeraKindCoordinator)
		r2 := mkRole(t, d, o.ID, "worker", HeraKindWorker)

		bad := int64(99999)
		_, err := d.SendHeraMessage(r1.ID, r2.ID, "body", "tldr", &bad)
		testutil.ErrorIs(t, err, ErrHeraMessageInvalidReplyTo)
	})

	t.Run("inbox full blocks sender", func(t *testing.T) {
		d := heraTestDB(t)
		o := mkOrch(t, d, "orch")
		r1 := mkRole(t, d, o.ID, "coord", HeraKindCoordinator)
		r2 := mkRole(t, d, o.ID, "worker", HeraKindWorker)

		// Use 10 senders to fill 500-msg inbox without hitting per-sender
		// rate limit (50/min × 10 senders = 500 messages total).
		const senderCount = 10
		senders := make([]*HeraRole, senderCount)
		for i := 0; i < senderCount; i++ {
			senders[i] = mkRole(t, d, o.ID, fmt.Sprintf("filler%d", i), HeraKindWorker)
		}
		for i := 0; i < HeraMaxUnreadPerRole; i++ {
			_, err := d.SendHeraMessage(senders[i%senderCount].ID, r2.ID, "body", "tldr", nil)
			testutil.NoError(t, err)
		}

		_, err := d.SendHeraMessage(r1.ID, r2.ID, "body", "tldr", nil)
		testutil.ErrorIs(t, err, ErrHeraMessageInboxFull)
	})

	t.Run("sender rate limited", func(t *testing.T) {
		d := heraTestDB(t)
		o := mkOrch(t, d, "orch")
		roles := make([]*HeraRole, HeraMaxSendsPerMinute+2)
		for i := range roles {
			roles[i] = mkRole(t, d, o.ID, fmt.Sprintf("role%d", i), HeraKindWorker)
		}
		sender := roles[0]

		for i := 1; i <= HeraMaxSendsPerMinute; i++ {
			_, err := d.SendHeraMessage(sender.ID, roles[i].ID, "body", "tldr", nil)
			testutil.NoError(t, err)
		}

		_, err := d.SendHeraMessage(sender.ID, roles[HeraMaxSendsPerMinute+1].ID, "body", "tldr", nil)
		testutil.ErrorIs(t, err, ErrHeraMessageRateLimited)
	})
}

func TestHeraInbox(t *testing.T) {
	t.Run("returns unread oldest first", func(t *testing.T) {
		d := heraTestDB(t)
		o := mkOrch(t, d, "orch")
		r1 := mkRole(t, d, o.ID, "coord", HeraKindCoordinator)
		r2 := mkRole(t, d, o.ID, "worker", HeraKindWorker)

		m1 := mkMsg(t, d, r1.ID, r2.ID, "first", "first tldr", nil)
		m2 := mkMsg(t, d, r1.ID, r2.ID, "second", "second tldr", nil)

		msgs, err := d.HeraInbox(r2.ID)
		testutil.NoError(t, err)
		testutil.Equal(t, len(msgs), 2)
		testutil.Equal(t, msgs[0].ID, m1.ID)
		testutil.Equal(t, msgs[1].ID, m2.ID)
	})

	t.Run("does not return messages to other roles", func(t *testing.T) {
		d := heraTestDB(t)
		o := mkOrch(t, d, "orch")
		r1 := mkRole(t, d, o.ID, "coord", HeraKindCoordinator)
		r2 := mkRole(t, d, o.ID, "worker", HeraKindWorker)
		r3 := mkRole(t, d, o.ID, "worker2", HeraKindWorker)

		mkMsg(t, d, r1.ID, r3.ID, "for r3", "tldr", nil)

		msgs, err := d.HeraInbox(r2.ID)
		testutil.NoError(t, err)
		testutil.Equal(t, len(msgs), 0)
	})

	t.Run("excludes read messages", func(t *testing.T) {
		d := heraTestDB(t)
		o := mkOrch(t, d, "orch")
		r1 := mkRole(t, d, o.ID, "coord", HeraKindCoordinator)
		r2 := mkRole(t, d, o.ID, "worker", HeraKindWorker)

		m := mkMsg(t, d, r1.ID, r2.ID, "msg", "tldr", nil)
		n, err := d.MarkHeraMessagesRead(r2.ID, []int64{m.ID})
		testutil.NoError(t, err)
		testutil.Equal(t, n, 1)

		msgs, err := d.HeraInbox(r2.ID)
		testutil.NoError(t, err)
		testutil.Equal(t, len(msgs), 0)
	})
}

func TestHeraMarkMessagesRead(t *testing.T) {
	t.Run("marks only recipient's messages", func(t *testing.T) {
		d := heraTestDB(t)
		o := mkOrch(t, d, "orch")
		r1 := mkRole(t, d, o.ID, "coord", HeraKindCoordinator)
		r2 := mkRole(t, d, o.ID, "worker", HeraKindWorker)
		r3 := mkRole(t, d, o.ID, "worker2", HeraKindWorker)

		m1 := mkMsg(t, d, r1.ID, r2.ID, "for r2", "tldr", nil)
		m2 := mkMsg(t, d, r1.ID, r3.ID, "for r3", "tldr", nil)

		// r2 tries to mark both — only its own message should be marked.
		n, err := d.MarkHeraMessagesRead(r2.ID, []int64{m1.ID, m2.ID})
		testutil.NoError(t, err)
		testutil.Equal(t, n, 1)

		// r3's message still unread.
		msgs, err := d.HeraInbox(r3.ID)
		testutil.NoError(t, err)
		testutil.Equal(t, len(msgs), 1)
		testutil.Equal(t, msgs[0].ID, m2.ID)
	})

	t.Run("idempotent re-mark", func(t *testing.T) {
		d := heraTestDB(t)
		o := mkOrch(t, d, "orch")
		r1 := mkRole(t, d, o.ID, "coord", HeraKindCoordinator)
		r2 := mkRole(t, d, o.ID, "worker", HeraKindWorker)

		m := mkMsg(t, d, r1.ID, r2.ID, "msg", "tldr", nil)
		n1, err := d.MarkHeraMessagesRead(r2.ID, []int64{m.ID})
		testutil.NoError(t, err)
		testutil.Equal(t, n1, 1)

		n2, err := d.MarkHeraMessagesRead(r2.ID, []int64{m.ID})
		testutil.NoError(t, err)
		testutil.Equal(t, n2, 0) // already read
	})

	t.Run("empty ids is no-op", func(t *testing.T) {
		d := heraTestDB(t)
		o := mkOrch(t, d, "orch")
		r := mkRole(t, d, o.ID, "coord", HeraKindCoordinator)

		n, err := d.MarkHeraMessagesRead(r.ID, nil)
		testutil.NoError(t, err)
		testutil.Equal(t, n, 0)
	})
}

func TestHeraMessagesByIDs(t *testing.T) {
	t.Run("loads requested ids", func(t *testing.T) {
		d := heraTestDB(t)
		o := mkOrch(t, d, "orch")
		r1 := mkRole(t, d, o.ID, "coord", HeraKindCoordinator)
		r2 := mkRole(t, d, o.ID, "worker", HeraKindWorker)

		m1 := mkMsg(t, d, r1.ID, r2.ID, "one", "one tldr", nil)
		m2 := mkMsg(t, d, r1.ID, r2.ID, "two", "two tldr", nil)

		got, err := d.HeraMessagesByIDs([]int64{m1.ID, m2.ID})
		testutil.NoError(t, err)
		testutil.Equal(t, len(got), 2)
	})

	t.Run("missing ids silently skipped", func(t *testing.T) {
		d := heraTestDB(t)
		o := mkOrch(t, d, "orch")
		r1 := mkRole(t, d, o.ID, "coord", HeraKindCoordinator)
		r2 := mkRole(t, d, o.ID, "worker", HeraKindWorker)

		m := mkMsg(t, d, r1.ID, r2.ID, "one", "tldr", nil)
		got, err := d.HeraMessagesByIDs([]int64{m.ID, 99999})
		testutil.NoError(t, err)
		testutil.Equal(t, len(got), 1)
		testutil.Equal(t, got[0].ID, m.ID)
	})

	t.Run("empty slice returns nil", func(t *testing.T) {
		d := heraTestDB(t)
		got, err := d.HeraMessagesByIDs(nil)
		testutil.NoError(t, err)
		testutil.Nil(t, got)
	})
}

func TestHeraMarkMessageDelivered(t *testing.T) {
	t.Run("stamps mode and delivered_at", func(t *testing.T) {
		d := heraTestDB(t)
		o := mkOrch(t, d, "orch")
		r1 := mkRole(t, d, o.ID, "coord", HeraKindCoordinator)
		r2 := mkRole(t, d, o.ID, "worker", HeraKindWorker)

		m := mkMsg(t, d, r1.ID, r2.ID, "body", "tldr", nil)
		testutil.Equal(t, m.DeliveryMode, HeraDeliveryPending)

		err := d.MarkHeraMessageDelivered(m.ID, HeraDeliveryIdleSubmit)
		testutil.NoError(t, err)

		got, err := d.HeraMessagesByIDs([]int64{m.ID})
		testutil.NoError(t, err)
		testutil.Equal(t, len(got), 1)
		testutil.Equal(t, got[0].DeliveryMode, HeraDeliveryIdleSubmit)
		if got[0].DeliveredAt == nil {
			t.Fatal("expected non-nil DeliveredAt")
		}
	})

	t.Run("idempotent re-stamp", func(t *testing.T) {
		d := heraTestDB(t)
		o := mkOrch(t, d, "orch")
		r1 := mkRole(t, d, o.ID, "coord", HeraKindCoordinator)
		r2 := mkRole(t, d, o.ID, "worker", HeraKindWorker)

		m := mkMsg(t, d, r1.ID, r2.ID, "body", "tldr", nil)
		testutil.NoError(t, d.MarkHeraMessageDelivered(m.ID, HeraDeliveryIdleSubmit))
		testutil.NoError(t, d.MarkHeraMessageDelivered(m.ID, HeraDeliveryQueuedNoBinding))
	})
}

func TestHeraMessageFKCascade(t *testing.T) {
	t.Run("role delete cascades messages", func(t *testing.T) {
		d := heraTestDB(t)
		o := mkOrch(t, d, "orch")
		r1 := mkRole(t, d, o.ID, "coord", HeraKindCoordinator)
		r2 := mkRole(t, d, o.ID, "worker", HeraKindWorker)

		mkMsg(t, d, r1.ID, r2.ID, "body", "tldr", nil)

		err := d.DeleteHeraRole(r2.ID)
		testutil.NoError(t, err)

		// Messages to r2 should be gone.
		msgs, err := d.HeraInbox(r2.ID)
		testutil.NoError(t, err)
		testutil.Equal(t, len(msgs), 0)
	})

	t.Run("in_reply_to SET NULL on parent delete", func(t *testing.T) {
		d := heraTestDB(t)
		o := mkOrch(t, d, "orch")
		r1 := mkRole(t, d, o.ID, "coord", HeraKindCoordinator)
		r2 := mkRole(t, d, o.ID, "worker", HeraKindWorker)

		parent := mkMsg(t, d, r1.ID, r2.ID, "parent", "parent tldr", nil)
		reply := mkMsg(t, d, r2.ID, r1.ID, "reply", "reply tldr", &parent.ID)
		testutil.Equal(t, *reply.InReplyTo, parent.ID)

		// Delete the parent message by deleting and recreating all messages.
		// We simulate parent deletion by directly removing it from DB.
		d.mu.Lock()
		_, err := d.conn.Exec(`DELETE FROM hera_messages WHERE id=?`, parent.ID)
		d.mu.Unlock()
		testutil.NoError(t, err)

		// Reply should still exist but with in_reply_to = NULL.
		got, err := d.HeraMessagesByIDs([]int64{reply.ID})
		testutil.NoError(t, err)
		testutil.Equal(t, len(got), 1)
		testutil.Nil(t, got[0].InReplyTo)
	})
}

func TestHeraInboxAfterFreeCapacity(t *testing.T) {
	t.Run("marking read frees inbox capacity", func(t *testing.T) {
		d := heraTestDB(t)
		o := mkOrch(t, d, "orch")
		recipient := mkRole(t, d, o.ID, "recipient", HeraKindWorker)
		blocker := mkRole(t, d, o.ID, "blocker", HeraKindCoordinator)

		// Create enough senders to fill the inbox without hitting the per-sender
		// rate limit (50/min). HeraMaxUnreadPerRole=500, HeraMaxSendsPerMinute=50
		// → need 10 distinct senders.
		const senderCount = 10
		senders := make([]*HeraRole, senderCount)
		for i := 0; i < senderCount; i++ {
			senders[i] = mkRole(t, d, o.ID, fmt.Sprintf("sender%d", i), HeraKindWorker)
		}

		ids := make([]int64, 0, HeraMaxUnreadPerRole)
		for i := 0; i < HeraMaxUnreadPerRole; i++ {
			sender := senders[i%senderCount]
			m, err := d.SendHeraMessage(sender.ID, recipient.ID, "body", "tldr", nil)
			testutil.NoError(t, err)
			ids = append(ids, m.ID)
		}

		// Inbox full — even a fresh sender is blocked.
		_, err := d.SendHeraMessage(blocker.ID, recipient.ID, "new", "new tldr", nil)
		testutil.ErrorIs(t, err, ErrHeraMessageInboxFull)

		// Mark all read → frees capacity.
		n, err := d.MarkHeraMessagesRead(recipient.ID, ids)
		testutil.NoError(t, err)
		testutil.Equal(t, n, HeraMaxUnreadPerRole)

		// Now the blocker can send.
		m, err := d.SendHeraMessage(blocker.ID, recipient.ID, "new", "new tldr", nil)
		testutil.NoError(t, err)
		if m.ID == 0 {
			t.Fatal("expected non-zero ID after inbox freed")
		}
	})
}
