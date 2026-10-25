package db

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/drn/argus/internal/testutil"
)

func TestHeraBoardLifecycle(t *testing.T) {
	d := heraTestDB(t)
	o := mkOrch(t, d, "board")
	c := mkRole(t, d, o.ID, "coord", HeraKindCoordinator)
	other := mkRole(t, d, o.ID, "other", HeraKindCoordinator)
	w := mkRole(t, d, o.ID, "worker", HeraKindWorker)
	_, err := d.PostHeraBoardItem(w.ID, "fyi", "bad", "body")
	testutil.ErrorIs(t, err, ErrHeraBoardInvalid)
	item, err := d.PostHeraBoardItem(c.ID, "decision", "Choose", "Details")
	testutil.NoError(t, err)
	testutil.Equal(t, item.ID > 0, true)
	testutil.NoError(t, d.ReadHeraBoardItem(c.ID, item.ID))
	same, err := d.UpdateHeraBoardItem(c.ID, item.ID, "decision", "Choose", "Details")
	testutil.NoError(t, err)
	testutil.Equal(t, same.Read, true)
	testutil.Equal(t, same.UpdatedAt, item.UpdatedAt)
	changed, err := d.UpdateHeraBoardItem(c.ID, item.ID, "decision", "Choose", "New details")
	testutil.NoError(t, err)
	testutil.Equal(t, changed.Read, false)
	testutil.Equal(t, changed.CreatedAt, item.CreatedAt)
	testutil.NoError(t, d.ReadHeraBoardItemAt(c.ID, item.ID, item.UpdatedAt))
	afterRace, err := d.ListHeraBoardItems(c.ID, false)
	testutil.NoError(t, err)
	testutil.Equal(t, afterRace[0].Read, false)
	testutil.NoError(t, d.ReadHeraBoardItemAt(c.ID, item.ID, changed.UpdatedAt))
	afterRead, err := d.ListHeraBoardItems(c.ID, false)
	testutil.NoError(t, err)
	testutil.Equal(t, afterRead[0].Read, true)
	_, err = d.UpdateHeraBoardItem(other.ID, item.ID, "fyi", "Stolen", "x")
	testutil.ErrorIs(t, err, ErrHeraBoardNotFound)
	testutil.ErrorIs(t, d.ReadHeraBoardItem(other.ID, item.ID), ErrHeraBoardNotFound)
	testutil.ErrorIs(t, d.ResolveHeraBoardItem(other.ID, item.ID, "agent", ""), ErrHeraBoardNotFound)
	testutil.NoError(t, d.ResolveHeraBoardItem(c.ID, item.ID, "human", "Dropped"))
	testutil.NoError(t, d.ResolveHeraBoardItem(c.ID, item.ID, "human", "Dropped again"))
	open, err := d.ListHeraBoardItems(c.ID, false)
	testutil.NoError(t, err)
	testutil.Equal(t, len(open), 0)
	all, err := d.ListHeraBoardItems(c.ID, true)
	testutil.NoError(t, err)
	testutil.Equal(t, all[0].ResolvedBy, "human")
	testutil.Equal(t, all[0].ResolutionNote, "Dropped")
	msgs, err := d.HeraInbox(c.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, len(msgs), 1)
	testutil.Contains(t, msgs[0].Body, "dropped")
	_, err = d.UpdateHeraBoardItem(c.ID, item.ID, "fyi", "Changed", "x")
	testutil.ErrorIs(t, err, ErrHeraBoardNotFound)
}

func TestHeraBoardValidation(t *testing.T) {
	for _, tc := range []struct{ name, kind, subject string }{
		{"kind", "other", "title"}, {"empty", "fyi", " "}, {"multiline", "decision", "a\nb"},
		{"control", "fyi", "a\x1bb"}, {"long", "fyi", strings.Repeat("x", 241)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := heraTestDB(t)
			o := mkOrch(t, d, "board")
			c := mkRole(t, d, o.ID, "coord", HeraKindCoordinator)
			_, err := d.PostHeraBoardItem(c.ID, tc.kind, tc.subject, "body")
			testutil.ErrorIs(t, err, ErrHeraBoardInvalid)
		})
	}
}

func TestHeraBoardDismissRollback(t *testing.T) {
	d := heraTestDB(t)
	o := mkOrch(t, d, "board")
	c := mkRole(t, d, o.ID, "coord", HeraKindCoordinator)
	i, err := d.PostHeraBoardItem(c.ID, "decision", "Choose", "body")
	testutil.NoError(t, err)
	_, err = d.conn.Exec(`CREATE TRIGGER reject_board_notice BEFORE INSERT ON hera_messages BEGIN SELECT RAISE(ABORT, 'test notice failure'); END`)
	testutil.NoError(t, err)
	testutil.Error(t, d.ResolveHeraBoardItem(c.ID, i.ID, "human", ""))
	items, err := d.ListHeraBoardItems(c.ID, false)
	testutil.NoError(t, err)
	testutil.Equal(t, len(items), 1)
	testutil.Equal(t, items[0].Resolved, false)
	msgs, err := d.HeraInbox(c.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, len(msgs), 0)
}

func TestHeraBoardReopenAndMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.sql")
	d, err := Open(path)
	testutil.NoError(t, err)
	o := mkOrch(t, d, "board")
	c := mkRole(t, d, o.ID, "coord", HeraKindCoordinator)
	i, err := d.PostHeraBoardItem(c.ID, "fyi", "Persist", "body")
	testutil.NoError(t, err)
	_, err = d.conn.Exec(`UPDATE schema_version SET version=1`)
	testutil.NoError(t, err)
	testutil.NoError(t, d.Close())
	d, err = Open(path)
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })
	items, err := d.ListHeraBoardItems(c.ID, false)
	testutil.NoError(t, err)
	testutil.Equal(t, len(items), 1)
	testutil.Equal(t, items[0].ID, i.ID)
	testutil.Equal(t, items[0].Read, false)
	var version int
	testutil.NoError(t, d.conn.QueryRow(`SELECT version FROM schema_version`).Scan(&version))
	testutil.Equal(t, version, 2)
}

func TestHeraBoardErrorsAndNonDecisionResolution(t *testing.T) {
	d := heraTestDB(t)
	o := mkOrch(t, d, "board")
	c := mkRole(t, d, o.ID, "coord", HeraKindCoordinator)
	_, err := d.PostHeraBoardItem(c.ID, "fyi", "body too long", strings.Repeat("x", 65537))
	testutil.ErrorIs(t, err, ErrHeraBoardInvalid)
	_, err = d.UpdateHeraBoardItem(c.ID, 999, "other", "s", "b")
	testutil.ErrorIs(t, err, ErrHeraBoardInvalid)
	testutil.ErrorIs(t, d.ReadHeraBoardItem(c.ID, 999), ErrHeraBoardNotFound)
	testutil.ErrorIs(t, d.ResolveHeraBoardItem(c.ID, 999, "human", ""), ErrHeraBoardNotFound)
	testutil.ErrorIs(t, d.ResolveHeraBoardItem(c.ID, 999, "other", ""), ErrHeraBoardInvalid)
	i, err := d.PostHeraBoardItem(c.ID, "fyi", "FYI", "body")
	testutil.NoError(t, err)
	testutil.NoError(t, d.ResolveHeraBoardItem(c.ID, i.ID, "human", "Seen"))
	msgs, err := d.HeraInbox(c.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, len(msgs), 0)
	testutil.NoError(t, d.Close())
	_, err = d.PostHeraBoardItem(c.ID, "fyi", "x", "b")
	testutil.Error(t, err)
	_, err = d.UpdateHeraBoardItem(c.ID, i.ID, "fyi", "x", "b")
	testutil.Error(t, err)
	_, err = d.ListHeraBoardItems(c.ID, false)
	testutil.Error(t, err)
	testutil.Error(t, d.ReadHeraBoardItem(c.ID, i.ID))
	testutil.Error(t, d.ResolveHeraBoardItem(c.ID, i.ID, "agent", ""))
}

func TestHeraBoardOutboxBindingAndRead(t *testing.T) {
	d := heraTestDB(t)
	o := mkOrch(t, d, "board")
	c := mkRole(t, d, o.ID, "coord", HeraKindCoordinator)
	i, err := d.PostHeraBoardItem(c.ID, "decision", "Choose", "body")
	testutil.NoError(t, err)
	testutil.NoError(t, d.ResolveHeraBoardItem(c.ID, i.ID, "human", ""))
	msgs, err := d.PendingHeraBoardNotices()
	testutil.NoError(t, err)
	testutil.Equal(t, len(msgs), 0)
	_, err = d.CreateHeraBinding(CreateHeraBindingInput{RoleID: c.ID, ArgusTaskID: "board-task", WorktreePath: "/wt/board"})
	testutil.NoError(t, err)
	msgs, err = d.PendingHeraBoardNotices()
	testutil.NoError(t, err)
	testutil.Equal(t, len(msgs), 1)
	testutil.Equal(t, msgs[0].SenderName("coord"), "Aaron (board)")
	testutil.Equal(t, msgs[0].DeliveryMode, HeraDeliveryPending)
	noticeID := msgs[0].ID
	testutil.NoError(t, d.MarkHeraMessageDelivered(noticeID, HeraDeliveryIdleSubmit))
	msgs, err = d.PendingHeraBoardNotices()
	testutil.NoError(t, err)
	testutil.Equal(t, len(msgs), 0)
	testutil.NoError(t, d.MarkHeraMessageDelivered(noticeID, HeraDeliveryQueuedNoBinding))
	msgs, err = d.PendingHeraBoardNotices()
	testutil.NoError(t, err)
	testutil.Equal(t, len(msgs), 1)
	_, err = d.MarkHeraMessagesRead(c.ID, []int64{msgs[0].ID})
	testutil.NoError(t, err)
	msgs, err = d.PendingHeraBoardNotices()
	testutil.NoError(t, err)
	testutil.Equal(t, len(msgs), 0)
}

func TestHeraBoardUnreadMissingTable(t *testing.T) {
	d := heraTestDB(t)
	_, err := d.conn.Exec(`DROP TABLE hera_board_items`)
	testutil.NoError(t, err)
	n, subject, err := d.HeraBoardUnread(1)
	testutil.NoError(t, err)
	testutil.Equal(t, n, 0)
	testutil.Equal(t, subject, "")
	testutil.NoError(t, d.Close())
	_, _, err = d.HeraBoardUnread(1)
	testutil.Error(t, err)
	_, err = d.PendingHeraBoardNotices()
	testutil.Error(t, err)
}

func TestHeraBoardNoticeSenderName(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		from, to int64
		want     string
	}{
		{"board", 1, 1, "Aaron (board)"},
		{"role", 2, 1, "worker"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			msg := &HeraMessage{FromRoleID: tc.from, ToRoleID: tc.to}
			tldr := HeraMessageTLDR{FromRoleID: tc.from, ToRoleID: tc.to}
			testutil.Equal(t, msg.SenderName("worker"), tc.want)
			testutil.Equal(t, tldr.SenderName("worker"), tc.want)
		})
	}
}
