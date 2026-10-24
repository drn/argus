package bridge

import (
	"testing"
	"time"

	"github.com/drn/argus/internal/testutil"
)

var t0 = time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)

func ev(typ string, seq int64) Event { return Event{Type: typ, Seq: seq, Boot: "b1", SessionID: "s1"} }

func TestHub_StateMachine(t *testing.T) {
	cost := 1.5
	tokens := int64(1000)
	tests := []struct {
		name   string
		events []Event
		check  func(t *testing.T, st State)
	}{
		{"turn start is busy", []Event{ev(TypeTurnStart, 1)}, func(t *testing.T, st State) {
			testutil.True(t, st.Busy)
		}},
		{"turn complete is idle", []Event{ev(TypeTurnStart, 1), ev(TypeTurnComplete, 2)}, func(t *testing.T, st State) {
			testutil.False(t, st.Busy)
		}},
		{"needs input then cleared", []Event{
			ev(TypeTurnStart, 1),
			{Type: TypeNeedsInput, Seq: 2, Boot: "b1", Kind: KindQuestion},
		}, func(t *testing.T, st State) {
			testutil.True(t, st.NeedsInput)
			testutil.Equal(t, st.NeedsInputKind, KindQuestion)
		}},
		{"cleared drops needs input", []Event{
			{Type: TypeNeedsInput, Seq: 1, Boot: "b1", Kind: KindPlan},
			ev(TypeCleared, 2),
		}, func(t *testing.T, st State) {
			testutil.False(t, st.NeedsInput)
			testutil.Equal(t, st.NeedsInputKind, "")
		}},
		{"turn complete clears a stuck dialog", []Event{
			ev(TypeTurnStart, 1),
			{Type: TypeNeedsInput, Seq: 2, Boot: "b1", Kind: KindPermission},
			ev(TypeTurnComplete, 3),
		}, func(t *testing.T, st State) {
			testutil.False(t, st.NeedsInput)
		}},
		{"session start records id model cwd", []Event{
			{Type: TypeSessionStart, Seq: 1, Boot: "b1", SessionID: "abc", Model: "opus", Cwd: "/w"},
		}, func(t *testing.T, st State) {
			testutil.Equal(t, st.SessionID, "abc")
			testutil.Equal(t, st.Model, "opus")
			testutil.Equal(t, st.Cwd, "/w")
		}},
		{"session id follows the newest event", []Event{
			{Type: TypeSessionStart, Seq: 1, Boot: "b1", SessionID: "old"},
			{Type: TypeSessionStart, Seq: 2, Boot: "b1", SessionID: "new"},
		}, func(t *testing.T, st State) {
			testutil.Equal(t, st.SessionID, "new")
		}},
		{"measure stored", []Event{
			{Type: TypeMeasure, Seq: 1, Boot: "b1", Context: &Context{Tokens: &tokens, Window: 200000}, CostUSD: &cost,
				RateLimits: []RateLimit{{Kind: "five_hour", PercentUsed: 40}}},
		}, func(t *testing.T, st State) {
			testutil.Equal(t, *st.Context.Tokens, int64(1000))
			testutil.Equal(t, *st.CostUSD, 1.5)
			testutil.Equal(t, len(st.RateLimits), 1)
		}},
		{"heartbeat stamps", []Event{ev(TypeHeartbeat, 1)}, func(t *testing.T, st State) {
			testutil.False(t, st.LastHeartbeatAt.IsZero())
			testutil.False(t, st.Busy)
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := NewHub()
			testutil.Equal(t, h.Ingest("t", tt.events, t0), len(tt.events))
			st, ok := h.Get("t")
			testutil.True(t, ok)
			tt.check(t, st)
		})
	}
}

func TestHub_Dedupe(t *testing.T) {
	h := NewHub()
	testutil.Equal(t, h.Ingest("t", []Event{ev(TypeTurnStart, 5)}, t0), 1)
	// Duplicate and lower seq in the same boot are dropped.
	testutil.Equal(t, h.Ingest("t", []Event{ev(TypeTurnComplete, 5), ev(TypeTurnComplete, 4)}, t0), 0)
	st, _ := h.Get("t")
	testutil.True(t, st.Busy)
	// A new boot (hot reload) restarts the seq baseline.
	e := ev(TypeTurnComplete, 1)
	e.Boot = "b2"
	testutil.Equal(t, h.Ingest("t", []Event{e}, t0), 1)
	st, _ = h.Get("t")
	testutil.False(t, st.Busy)
	testutil.Equal(t, st.Boot, "b2")
}

func TestHub_UnknownTypeRejected(t *testing.T) {
	h := NewHub()
	testutil.Equal(t, h.Ingest("t", []Event{ev("future.thing", 1)}, t0), 0)
	_, ok := h.Get("t")
	testutil.False(t, ok)
}

func TestHub_LiveAndPrune(t *testing.T) {
	h := NewHub()
	h.Ingest("t", []Event{ev(TypeHeartbeat, 1)}, t0)
	_, live := h.Live("t", t0.Add(LiveWindow))
	testutil.True(t, live)
	_, live = h.Live("t", t0.Add(LiveWindow+time.Second))
	testutil.False(t, live)
	_, live = h.Live("missing", t0)
	testutil.False(t, live)
	testutil.Equal(t, h.Prune(t0.Add(30*time.Minute)), 0)
	testutil.Equal(t, h.Prune(t0.Add(2*time.Hour)), 1)
	testutil.Equal(t, h.Len(), 0)
}

func TestHub_GetReturnsCopy(t *testing.T) {
	h := NewHub()
	cost := 2.0
	h.Ingest("t", []Event{{Type: TypeMeasure, Seq: 1, Boot: "b", CostUSD: &cost, Context: &Context{Window: 1},
		RateLimits: []RateLimit{{Kind: "x"}}}}, t0)
	st, _ := h.Get("t")
	*st.CostUSD = 99
	st.Context.Window = 99
	st.RateLimits[0].Kind = "mutated"
	again, _ := h.Get("t")
	testutil.Equal(t, *again.CostUSD, 2.0)
	testutil.Equal(t, again.Context.Window, int64(1))
	testutil.Equal(t, again.RateLimits[0].Kind, "x")
}

func TestHub_TransitionStamp(t *testing.T) {
	h := NewHub()
	h.Ingest("t", []Event{ev(TypeTurnStart, 1)}, t0)
	st, _ := h.Get("t")
	testutil.True(t, st.LastTransitionAt.Equal(t0))
	// A repeated turn.start is not a transition.
	h.Ingest("t", []Event{ev(TypeTurnStart, 2)}, t0.Add(time.Second))
	st, _ = h.Get("t")
	testutil.True(t, st.LastTransitionAt.Equal(t0))
}

func TestHub_NewBootResetsLiveState(t *testing.T) {
	h := NewHub()
	h.Ingest("t", []Event{
		ev(TypeTurnStart, 1),
		{Type: TypeNeedsInput, Seq: 2, Boot: "b1", Kind: KindPermission, Confidence: ConfidencePendingOrRunning},
	}, t0)
	st, _ := h.Get("t")
	testutil.True(t, st.Busy)
	testutil.True(t, st.NeedsInput)
	testutil.Equal(t, st.NeedsInputConfidence, ConfidencePendingOrRunning)

	// The mod restarts mid-turn; its first event (a session.start) must not
	// resurrect the old busy/dialog state.
	reload := Event{Type: TypeSessionStart, Seq: 1, Boot: "b2", SessionID: "s1"}
	testutil.Equal(t, h.Ingest("t", []Event{reload}, t0.Add(time.Minute)), 1)
	st, _ = h.Get("t")
	testutil.False(t, st.Busy)
	testutil.False(t, st.NeedsInput)
	testutil.Equal(t, st.NeedsInputKind, "")
	testutil.Equal(t, st.NeedsInputConfidence, "")
	testutil.Equal(t, st.Boot, "b2")

	// Same boot continues normally afterwards.
	h.Ingest("t", []Event{{Type: TypeTurnStart, Seq: 2, Boot: "b2"}}, t0.Add(time.Minute))
	st, _ = h.Get("t")
	testutil.True(t, st.Busy)
}
