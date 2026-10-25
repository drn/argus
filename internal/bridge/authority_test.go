package bridge

import (
	"testing"
	"time"

	"github.com/drn/argus/internal/testutil"
)

func TestAuthority(t *testing.T) {
	t.Parallel()
	h := NewHub()
	h.Ingest("t", []Event{{Type: TypeTurnStart, Boot: "b", Seq: 1, SessionID: "s", Generation: "g"}}, t0)
	for _, tc := range []struct {
		name, generation string
		now              time.Time
		want             bool
	}{
		{"current", "g", t0, true}, {"restart", "new", t0, false}, {"legacy", "", t0, false}, {"stale", "g", t0.Add(LiveWindow), false},
	} {
		t.Run(tc.name, func(t *testing.T) { _, ok := h.Authority("t", tc.generation, tc.now); testutil.Equal(t, ok, tc.want) })
	}
}

func TestSessionChangeClearsMeasurement(t *testing.T) {
	t.Parallel()
	h := NewHub()
	cost := 1.0
	h.Ingest("t", []Event{{Type: TypeMeasure, Seq: 1, Boot: "b", SessionID: "old", CostUSD: &cost}, {Type: TypeSessionStart, Seq: 2, Boot: "b", SessionID: "new"}}, t0)
	st, _ := h.Get("t")
	testutil.Nil(t, st.CostUSD)
	testutil.True(t, st.LastMeasureAt.IsZero())
}

func TestRetiredBootCannotReturn(t *testing.T) {
	t.Parallel()
	h := NewHub()
	h.Ingest("t", []Event{{Type: TypeTurnStart, Boot: "old", Seq: 1}, {Type: TypeTurnComplete, Boot: "new", Seq: 1}, {Type: TypeTurnStart, Boot: "old", Seq: 2}}, t0)
	st, _ := h.Get("t")
	testutil.Equal(t, st.Boot, "new")
	testutil.False(t, st.Busy)
}

func TestHeartbeatRehydratesBusyAndPermissionKind(t *testing.T) {
	t.Parallel()
	h := NewHub()
	busy, needs := true, true
	h.Ingest("t", []Event{{Type: TypeHeartbeat, Boot: "b", Seq: 1, Generation: "g", SessionID: "s", Busy: &busy, NeedsInput: &needs, Kind: KindPermission}}, t0)
	st, ok := h.Authority("t", "g", t0)
	testutil.True(t, ok)
	testutil.True(t, st.Busy)
	testutil.True(t, st.NeedsInput)
	testutil.True(t, UncertainPermission(st))
}

func TestLegacyOrIncompleteReportCannotInheritAuthority(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		event Event
	}{
		{"legacy", Event{Type: TypeHeartbeat, Boot: "legacy", Seq: 1, SessionID: "s"}},
		{"new generation without session", Event{Type: TypeHeartbeat, Boot: "new", Seq: 1, Generation: "new"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := NewHub()
			h.Ingest("t", []Event{{Type: TypeTurnStart, Boot: "b", Seq: 1, Generation: "g", SessionID: "s"}, tc.event}, t0)
			_, ok := h.Authority("t", tc.event.Generation, t0)
			testutil.False(t, ok)
		})
	}
}
