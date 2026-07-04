package daemon

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

// TestDetectSuspendGap exercises the pure detector: a normal-cadence gap never
// fires, a gap at/over the threshold fires, and a zero/negative gap never fires.
func TestDetectSuspendGap(t *testing.T) {
	base := time.Now().Round(0)
	cases := []struct {
		name string
		prev time.Time
		now  time.Time
		want bool
	}{
		{"normal cadence tick", base, base.Add(hostSuspendInterval), false},
		{"just under threshold", base, base.Add(hostSuspendThreshold - time.Second), false},
		{"exactly at threshold", base, base.Add(hostSuspendThreshold), true},
		{"long sleep", base, base.Add(45 * time.Minute), true},
		{"multi-hour hibernate", base, base.Add(6 * time.Hour), true},
		{"zero gap", base, base, false},
		{"negative gap (clock stepped back)", base, base.Add(-time.Hour), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gap, suspended := detectSuspendGap(c.prev, c.now, hostSuspendThreshold)
			testutil.Equal(t, suspended, c.want)
			testutil.Equal(t, gap, c.now.Sub(c.prev))
		})
	}
}

// TestHostSuspendBody: the JSON body carries the type marker, both gap forms, and
// a non-empty human-readable note.
func TestHostSuspendBody(t *testing.T) {
	body := hostSuspendBody(42 * time.Minute)

	var p hostSuspendPayload
	testutil.NoError(t, json.Unmarshal([]byte(body), &p))
	testutil.Equal(t, p.Type, hostSuspendMessageType)
	testutil.Equal(t, p.ApproxGapSeconds, int64((42 * time.Minute).Seconds()))
	testutil.Equal(t, p.ApproxGap, (42 * time.Minute).String())
	testutil.Equal(t, len(p.Note) > 0, true)
	testutil.Contains(t, p.Note, "42m0s")
}

// TestSendHostSuspendSignals mirrors bounce_test.go: notes land for live tasks,
// archived/missing tasks are skipped, and an empty ID list is a no-op.
func TestSendHostSuspendSignals(t *testing.T) {
	t.Run("posts note to a live task", func(t *testing.T) {
		d := openBounceTestDB(t)
		task := &model.Task{Name: "coord", Status: model.StatusInProgress}
		testutil.NoError(t, d.Add(task))

		sent := sendHostSuspendSignals(d, []string{task.ID}, 40*time.Minute)
		testutil.Equal(t, sent, 1)

		msgs, err := d.Inbox(task.ID, db.InboxFilter{})
		testutil.NoError(t, err)
		testutil.Equal(t, len(msgs), 1)
		testutil.Equal(t, msgs[0].From, SystemTaskID)
		testutil.Equal(t, msgs[0].Kind, model.KindNote)
		var p hostSuspendPayload
		testutil.NoError(t, json.Unmarshal([]byte(msgs[0].Body), &p))
		testutil.Equal(t, p.Type, hostSuspendMessageType)
		testutil.Equal(t, p.ApproxGapSeconds, int64((40 * time.Minute).Seconds()))
	})

	t.Run("skips archived and missing, notifies the rest", func(t *testing.T) {
		d := openBounceTestDB(t)
		live := &model.Task{Name: "live", Status: model.StatusInProgress}
		testutil.NoError(t, d.Add(live))
		archived := &model.Task{Name: "archived", Status: model.StatusInProgress}
		testutil.NoError(t, d.Add(archived))
		testutil.NoError(t, d.SetArchived(archived.ID, true))

		sent := sendHostSuspendSignals(d, []string{live.ID, archived.ID, "ghost-99999"}, time.Hour)
		testutil.Equal(t, sent, 1)

		liveMsgs, err := d.Inbox(live.ID, db.InboxFilter{})
		testutil.NoError(t, err)
		testutil.Equal(t, len(liveMsgs), 1)

		archMsgs, err := d.Inbox(archived.ID, db.InboxFilter{})
		testutil.NoError(t, err)
		testutil.Equal(t, len(archMsgs), 0)
	})

	t.Run("empty id list is a no-op", func(t *testing.T) {
		d := openBounceTestDB(t)
		testutil.Equal(t, sendHostSuspendSignals(d, nil, time.Hour), 0)
	})

	t.Run("notifies multiple running tasks once each", func(t *testing.T) {
		d := openBounceTestDB(t)
		var ids []string
		for _, name := range []string{"a", "b", "c"} {
			task := &model.Task{Name: name, Status: model.StatusInProgress}
			testutil.NoError(t, d.Add(task))
			ids = append(ids, task.ID)
		}
		sent := sendHostSuspendSignals(d, ids, time.Hour)
		testutil.Equal(t, sent, 3)
		for _, id := range ids {
			msgs, err := d.Inbox(id, db.InboxFilter{})
			testutil.NoError(t, err)
			testutil.Equal(t, len(msgs), 1)
		}
	})
}

// runningInboxCount counts inbox messages for each of the given task IDs.
func runningInboxCount(t *testing.T, d *db.DB, ids []string) int {
	t.Helper()
	total := 0
	for _, id := range ids {
		msgs, err := d.Inbox(id, db.InboxFilter{})
		testutil.NoError(t, err)
		total += len(msgs)
	}
	return total
}

// TestHostSuspendTick drives the per-tick logic end-to-end through a Daemon whose
// in-process runner reports two running tasks (via SetPendingRestartForTest, the
// same trick bounce_test.go uses to populate Running() without real PTYs). It
// proves: a first normal-cadence tick posts nothing; a large gap posts exactly one
// note per running task; the following normal-cadence tick posts nothing (one-shot,
// no dedup state).
func TestHostSuspendTick(t *testing.T) {
	d, _ := testDaemon(t)

	runner, ok := d.runner.(*agent.Runner)
	testutil.Equal(t, ok, true)

	// Two running tasks with matching DB rows so the broadcast finds them.
	var ids []string
	for _, name := range []string{"coord", "worker-1a"} {
		task := &model.Task{Name: name, Status: model.StatusInProgress}
		testutil.NoError(t, d.db.Add(task))
		runner.SetPendingRestartForTest(task.ID, true)
		ids = append(ids, task.ID)
	}
	t.Cleanup(func() {
		for _, id := range ids {
			runner.SetPendingRestartForTest(id, false)
		}
	})

	base := time.Now().Round(0)

	// First tick after start: baseline just stamped, normal cadence → no note.
	next := d.hostSuspendTick(base, base.Add(hostSuspendInterval))
	testutil.Equal(t, runningInboxCount(t, d.db, ids), 0)

	// A large wall-clock gap (host was asleep) → one note per running task.
	afterSleep := next.Add(50 * time.Minute)
	next = d.hostSuspendTick(next, afterSleep)
	testutil.Equal(t, runningInboxCount(t, d.db, ids), len(ids))

	// The very next tick is normal cadence again (baseline advanced) → one-shot,
	// no further notes despite no dedup bookkeeping.
	d.hostSuspendTick(next, next.Add(hostSuspendInterval))
	testutil.Equal(t, runningInboxCount(t, d.db, ids), len(ids))
}
