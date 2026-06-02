package db

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

func TestDB_InsertEvent(t *testing.T) {
	t.Run("stamps id and at", func(t *testing.T) {
		d := testDB(t)
		got, err := d.InsertEvent(&model.Event{Type: model.EventTypeTaskCreated, TaskID: "abc"})
		testutil.NoError(t, err)
		if got.ID <= 0 {
			t.Fatalf("expected positive id, got %d", got.ID)
		}
		if got.At.IsZero() {
			t.Errorf("expected At to be stamped")
		}
		testutil.Equal(t, got.Type, model.EventTypeTaskCreated)
		testutil.Equal(t, got.TaskID, "abc")
	})

	t.Run("preserves provided payload bytes", func(t *testing.T) {
		d := testDB(t)
		raw := json.RawMessage(`{"from":"pending","to":"in_progress"}`)
		got, err := d.InsertEvent(&model.Event{
			Type:    model.EventTypeTaskStatusChanged,
			TaskID:  "t-1",
			Payload: raw,
		})
		testutil.NoError(t, err)
		testutil.Equal(t, string(got.Payload), string(raw))
	})

	t.Run("rejects empty type", func(t *testing.T) {
		d := testDB(t)
		_, err := d.InsertEvent(&model.Event{Type: ""})
		testutil.Error(t, err)
	})

	t.Run("monotonic ids", func(t *testing.T) {
		d := testDB(t)
		a, err := d.InsertEvent(&model.Event{Type: "x"})
		testutil.NoError(t, err)
		b, err := d.InsertEvent(&model.Event{Type: "x"})
		testutil.NoError(t, err)
		if b.ID <= a.ID {
			t.Errorf("expected b.ID > a.ID, got %d <= %d", b.ID, a.ID)
		}
	})
}

func TestDB_EventsSince(t *testing.T) {
	d := testDB(t)

	var ids []int64
	for i := range 5 {
		ev, err := d.InsertEvent(&model.Event{
			Type:    "test",
			TaskID:  fmt.Sprintf("t-%d", i),
			Payload: json.RawMessage(fmt.Sprintf(`{"i":%d}`, i)),
		})
		testutil.NoError(t, err)
		ids = append(ids, ev.ID)
	}

	t.Run("returns all when cursor zero", func(t *testing.T) {
		got, err := d.EventsSince(0, 0)
		testutil.NoError(t, err)
		testutil.Equal(t, len(got), 5)
		// Ascending by id.
		for i := 1; i < len(got); i++ {
			if got[i].ID <= got[i-1].ID {
				t.Errorf("not ascending: %d <= %d", got[i].ID, got[i-1].ID)
			}
		}
	})

	t.Run("strict cursor inequality", func(t *testing.T) {
		got, err := d.EventsSince(ids[2], 0)
		testutil.NoError(t, err)
		testutil.Equal(t, len(got), 2)
		testutil.Equal(t, got[0].ID, ids[3])
		testutil.Equal(t, got[1].ID, ids[4])
	})

	t.Run("limit clamps result", func(t *testing.T) {
		got, err := d.EventsSince(0, 2)
		testutil.NoError(t, err)
		testutil.Equal(t, len(got), 2)
		testutil.Equal(t, got[0].ID, ids[0])
		testutil.Equal(t, got[1].ID, ids[1])
	})

	t.Run("cursor past latest returns empty", func(t *testing.T) {
		got, err := d.EventsSince(ids[4]+100, 0)
		testutil.NoError(t, err)
		testutil.Equal(t, len(got), 0)
	})
}

func TestDB_OldestLatestEventID(t *testing.T) {
	d := testDB(t)

	t.Run("zero on empty table", func(t *testing.T) {
		oldest, err := d.OldestEventID()
		testutil.NoError(t, err)
		testutil.Equal(t, oldest, int64(0))

		latest, err := d.LatestEventID()
		testutil.NoError(t, err)
		testutil.Equal(t, latest, int64(0))
	})

	t.Run("matches inserted range", func(t *testing.T) {
		a, err := d.InsertEvent(&model.Event{Type: "x"})
		testutil.NoError(t, err)
		b, err := d.InsertEvent(&model.Event{Type: "y"})
		testutil.NoError(t, err)

		oldest, err := d.OldestEventID()
		testutil.NoError(t, err)
		testutil.Equal(t, oldest, a.ID)

		latest, err := d.LatestEventID()
		testutil.NoError(t, err)
		testutil.Equal(t, latest, b.ID)
	})
}

func TestDB_InsertEvent_RingEviction(t *testing.T) {
	d := testDB(t)
	// Temporarily lower the cap so the test stays fast. eventsCapForTest
	// is the runtime override used by tests; production uses the default
	// MaxEventsRetained = 10000.
	old := eventsCapForTest
	eventsCapForTest = 5
	t.Cleanup(func() { eventsCapForTest = old })

	for i := range 8 {
		_, err := d.InsertEvent(&model.Event{
			Type:    "x",
			TaskID:  fmt.Sprintf("t-%d", i),
			Payload: json.RawMessage(fmt.Sprintf(`{"i":%d}`, i)),
		})
		testutil.NoError(t, err)
	}

	all, err := d.EventsSince(0, 0)
	testutil.NoError(t, err)
	testutil.Equal(t, len(all), 5)
	// Oldest three rows were evicted; remaining rows are i=3..7.
	testutil.Equal(t, all[0].TaskID, "t-3")
	testutil.Equal(t, all[4].TaskID, "t-7")
}

func TestDB_DefaultEventCap(t *testing.T) {
	// Production default must match the plan's 10,000 floor.
	testutil.Equal(t, MaxEventsRetained, 10000)
}

func TestSetEventsCapForTest_RoundTrips(t *testing.T) {
	prev := SetEventsCapForTest(42)
	t.Cleanup(func() { SetEventsCapForTest(prev) })
	testutil.Equal(t, eventsCapForTest, 42)
	// Reset returns the value we just installed.
	restored := SetEventsCapForTest(prev)
	testutil.Equal(t, restored, 42)
	testutil.Equal(t, eventsCapForTest, prev)
}
