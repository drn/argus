package bridge

import (
	"fmt"
	"testing"
	"time"

	"github.com/drn/argus/internal/testutil"
)

type logSink struct{ lines []string }

func (l *logSink) logf(format string, args ...any) {
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
}

func newShadow(t *testing.T) (*Shadow, *logSink) {
	t.Helper()
	sink := &logSink{}
	s := NewShadow(NewHub(), 10*time.Second)
	s.logf = sink.logf
	return s, sink
}

func set(ids ...string) map[string]bool {
	m := map[string]bool{}
	for _, id := range ids {
		m[id] = true
	}
	return m
}

func TestShadow_NoBridgeNeverDisagrees(t *testing.T) {
	s, sink := newShadow(t)
	s.Observe(t0, []string{"a"}, set("a"), nil)
	st := s.Stats(t0)
	testutil.Equal(t, st.Signals[SignalIdle].NoBridge, int64(1))
	testutil.Equal(t, st.Signals[SignalNeedsInput].NoBridge, int64(1))
	testutil.Equal(t, st.Signals[SignalIdle].Disagree, int64(0))
	testutil.Equal(t, len(sink.lines), 0)
}

func TestShadow_StaleBridgeIsNoBridge(t *testing.T) {
	s, _ := newShadow(t)
	s.Hub().Ingest("a", []Event{ev(TypeHeartbeat, 1)}, t0)
	s.Observe(t0.Add(LiveWindow+time.Second), []string{"a"}, nil, nil)
	testutil.Equal(t, s.Stats(t0).Signals[SignalIdle].NoBridge, int64(1))
}

func TestShadow_Agreement(t *testing.T) {
	s, _ := newShadow(t)
	s.Hub().Ingest("a", []Event{ev(TypeTurnStart, 1)}, t0)
	// Busy bridge + scraper not idle, no needs-input: agree on both.
	s.Observe(t0, []string{"a"}, nil, nil)
	st := s.Stats(t0)
	testutil.Equal(t, st.Signals[SignalIdle].Agree, int64(1))
	testutil.Equal(t, st.Signals[SignalNeedsInput].Agree, int64(1))
}

func TestShadow_IdleTreatsPendingDialogAsQuiescent(t *testing.T) {
	s, _ := newShadow(t)
	s.Hub().Ingest("a", []Event{ev(TypeTurnStart, 1), {Type: TypeNeedsInput, Seq: 2, Boot: "b1", Kind: KindQuestion}}, t0)
	// Turn is busy but a dialog is parked: screen is quiescent, scraper idle agrees.
	s.Observe(t0, []string{"a"}, set("a"), set("a"))
	st := s.Stats(t0)
	testutil.Equal(t, st.Signals[SignalIdle].Agree, int64(1))
	testutil.Equal(t, st.Signals[SignalNeedsInput].Agree, int64(1))
}

func TestShadow_TransientLagDoesNotLog(t *testing.T) {
	s, sink := newShadow(t)
	s.Hub().Ingest("a", []Event{ev(TypeTurnComplete, 1)}, t0)
	// Bridge idle, scraper still busy for one tick (3s lag), then catches up.
	s.Observe(t0, []string{"a"}, nil, nil)
	s.Observe(t0.Add(5*time.Second), []string{"a"}, set("a"), nil)
	st := s.Stats(t0)
	testutil.Equal(t, st.Signals[SignalIdle].Settling, int64(1))
	testutil.Equal(t, st.Signals[SignalIdle].Disagree, int64(0))
	testutil.Equal(t, st.Signals[SignalIdle].Agree, int64(1))
	testutil.Equal(t, len(sink.lines), 0)
	testutil.Equal(t, len(st.Open), 0)
}

func TestShadow_PersistentDisagreementLogsOnceThenResolves(t *testing.T) {
	s, sink := newShadow(t)
	s.Hub().Ingest("a", []Event{ev(TypeTurnComplete, 1)}, t0)
	for i := 0; i <= 3; i++ { // t0, +5, +10, +15: scraper never goes idle
		now := t0.Add(time.Duration(i) * 5 * time.Second)
		s.Hub().Ingest("a", []Event{ev(TypeHeartbeat, int64(10+i))}, now) // keep bridge live
		s.Observe(now, []string{"a"}, nil, nil)
	}
	st := s.Stats(t0.Add(15 * time.Second))
	testutil.Equal(t, st.Signals[SignalIdle].Settling, int64(2))
	testutil.Equal(t, st.Signals[SignalIdle].Disagree, int64(2))
	testutil.Equal(t, len(sink.lines), 1)
	testutil.Contains(t, sink.lines[0], "[bridge] disagree signal=idle task=a scraper=false bridge=true")
	testutil.Equal(t, len(st.Open), 1)
	testutil.True(t, st.Open[0].Settled)

	now := t0.Add(20 * time.Second)
	s.Hub().Ingest("a", []Event{ev(TypeHeartbeat, 99)}, now)
	s.Observe(now, []string{"a"}, set("a"), nil)
	testutil.Equal(t, len(sink.lines), 2)
	testutil.Contains(t, sink.lines[1], "[bridge] resolved signal=idle task=a after=20s")
	testutil.Equal(t, len(s.Stats(now).Open), 0)
}

func TestShadow_NeedsInputDisagreement(t *testing.T) {
	s, sink := newShadow(t)
	s.Hub().Ingest("a", []Event{ev(TypeTurnStart, 1), {Type: TypeNeedsInput, Seq: 2, Boot: "b1", Kind: KindQuestion}}, t0)
	for i := 0; i < 4; i++ {
		now := t0.Add(time.Duration(i) * 5 * time.Second)
		s.Hub().Ingest("a", []Event{ev(TypeHeartbeat, int64(10+i))}, now)
		s.Observe(now, []string{"a"}, set("a"), nil) // scraper misses the dialog
	}
	st := s.Stats(t0)
	testutil.Equal(t, st.Signals[SignalNeedsInput].Disagree, int64(2))
	testutil.Equal(t, st.Signals[SignalIdle].Agree, int64(4))
	testutil.Equal(t, len(sink.lines), 1)
	testutil.Contains(t, sink.lines[0], "signal=needs_input")
}

func TestShadow_StoppedTaskClosesOpenDisagreement(t *testing.T) {
	s, sink := newShadow(t)
	s.Hub().Ingest("a", []Event{ev(TypeTurnComplete, 1)}, t0)
	for i := 0; i < 4; i++ {
		now := t0.Add(time.Duration(i) * 5 * time.Second)
		s.Hub().Ingest("a", []Event{ev(TypeHeartbeat, int64(10+i))}, now)
		s.Observe(now, []string{"a"}, nil, nil)
	}
	testutil.Equal(t, len(sink.lines), 1)
	s.Observe(t0.Add(20*time.Second), nil, nil, nil) // task no longer running
	testutil.Equal(t, len(sink.lines), 2)
	testutil.Contains(t, sink.lines[1], "task stopped")
	testutil.Equal(t, len(s.Stats(t0).Open), 0)
}

func TestShadow_BridgeLostDropsOpenSilently(t *testing.T) {
	s, sink := newShadow(t)
	s.Hub().Ingest("a", []Event{ev(TypeTurnComplete, 1)}, t0)
	s.Observe(t0, []string{"a"}, nil, nil) // opens (settling)
	s.Observe(t0.Add(LiveWindow+time.Minute), []string{"a"}, nil, nil)
	testutil.Equal(t, len(s.Stats(t0).Open), 0)
	testutil.Equal(t, len(sink.lines), 0)
}

func TestNewShadow_DefaultSettle(t *testing.T) {
	s := NewShadow(NewHub(), 0)
	testutil.Equal(t, s.Stats(t0).SettleSeconds, DefaultSettle.Seconds())
}

func TestDefaultLogf_DoesNotPanic(t *testing.T) {
	defaultLogf("[bridge] test %d", 1)
}

func TestShadow_PermissionKindCountedSeparately(t *testing.T) {
	s, sink := newShadow(t)
	s.Hub().Ingest("a", []Event{ev(TypeTurnStart, 1),
		{Type: TypeNeedsInput, Seq: 2, Boot: "b1", Kind: KindPermission, Confidence: ConfidencePendingOrRunning}}, t0)
	for i := 0; i < 4; i++ { // approved tool still running: bridge says needs-input, scraper does not
		now := t0.Add(time.Duration(i) * 5 * time.Second)
		s.Hub().Ingest("a", []Event{ev(TypeHeartbeat, int64(10+i))}, now)
		s.Observe(now, []string{"a"}, nil, nil)
	}
	st := s.Stats(t0)
	testutil.Equal(t, st.Signals[SignalNeedsInputPermission].Disagree, int64(0))
	testutil.Equal(t, st.Signals[SignalNeedsInputPermission].Agree, int64(4))
	testutil.Equal(t, st.Signals[SignalIdle].Agree, int64(4))
	testutil.Equal(t, st.Signals[SignalNeedsInput].Disagree, int64(0))
	testutil.Equal(t, st.Signals[SignalNeedsInput].Agree, int64(0))
	testutil.Equal(t, len(sink.lines), 0)

	// The dialog clears: the tick now counts under the headline signal and the
	// open permission disagreement is dropped, not left dangling.
	now := t0.Add(20 * time.Second)
	s.Hub().Ingest("a", []Event{ev(TypeTurnComplete, 50)}, now)
	s.Observe(now, []string{"a"}, set("a"), nil)
	testutil.Equal(t, s.Stats(now).Signals[SignalNeedsInput].Agree, int64(1))
	testutil.Equal(t, len(s.Stats(now).Open), 0)
}

func TestShadow_ExactPermissionStaysHeadline(t *testing.T) {
	s, _ := newShadow(t)
	s.Hub().Ingest("a", []Event{ev(TypeTurnStart, 1),
		{Type: TypeNeedsInput, Seq: 2, Boot: "b1", Kind: KindPermission, Confidence: ConfidenceExact}}, t0)
	s.Observe(t0, []string{"a"}, nil, nil)
	st := s.Stats(t0)
	testutil.Equal(t, st.Signals[SignalNeedsInput].Settling, int64(1))
	testutil.Equal(t, st.Signals[SignalIdle].Settling, int64(1))
	testutil.Equal(t, st.Signals[SignalNeedsInputPermission], Counters{})
}

func TestShadow_QuestionKindStaysHeadline(t *testing.T) {
	s, _ := newShadow(t)
	s.Hub().Ingest("a", []Event{ev(TypeTurnStart, 1),
		{Type: TypeNeedsInput, Seq: 2, Boot: "b1", Kind: KindQuestion, Confidence: ConfidenceExact}}, t0)
	s.Observe(t0, []string{"a"}, set("a"), set("a"))
	st := s.Stats(t0)
	testutil.Equal(t, st.Signals[SignalNeedsInput].Agree, int64(1))
	testutil.Equal(t, st.Signals[SignalNeedsInputPermission].Agree, int64(0))
}
