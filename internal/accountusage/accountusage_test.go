package accountusage

import (
	"math"
	"testing"
	"time"

	"github.com/drn/argus/internal/testutil"
)

var t0 = time.Date(2026, 10, 30, 16, 0, 0, 0, time.UTC)

func newTestLedger(now *time.Time) *Ledger {
	l := NewLedger()
	l.now = func() time.Time { return *now }
	return l
}

func TestLedger_RecordAndSnapshot(t *testing.T) {
	now := t0
	l := newTestLedger(&now)
	reset := t0.Add(time.Hour)

	l.Record("", WindowFiveHour, 100, reset, t0)
	l.Record("personal", WindowWeekly, 20, t0.Add(48*time.Hour), t0)

	snap := l.Snapshot()
	testutil.NotNil(t, snap["default"].FiveHour)
	testutil.Equal(t, snap["default"].FiveHour.Pct, 100.0)
	testutil.Nil(t, snap["default"].Weekly)
	testutil.Equal(t, snap["personal"].Weekly.Pct, 20.0)
	testutil.Nil(t, snap["personal"].FiveHour)
}

func TestLedger_KeepsNewest(t *testing.T) {
	now := t0
	l := newTestLedger(&now)
	reset := t0.Add(time.Hour)
	l.Record("default", WindowFiveHour, 50, reset, t0.Add(time.Minute))
	l.Record("default", WindowFiveHour, 40, reset, t0) // older: ignored
	testutil.Equal(t, l.Snapshot()["default"].FiveHour.Pct, 50.0)
	l.Record("default", WindowFiveHour, 60, reset, t0.Add(2*time.Minute))
	testutil.Equal(t, l.Snapshot()["default"].FiveHour.Pct, 60.0)
}

func TestLedger_ExpiresAtReset(t *testing.T) {
	now := t0
	l := newTestLedger(&now)
	l.Record("default", WindowFiveHour, 100, t0.Add(time.Hour), t0)
	now = t0.Add(59 * time.Minute)
	testutil.NotNil(t, l.Snapshot()["default"].FiveHour)
	now = t0.Add(time.Hour)
	_, ok := l.Snapshot()["default"]
	testutil.Equal(t, ok, false)
}

func TestLedger_IgnoresInvalid(t *testing.T) {
	now := t0
	l := newTestLedger(&now)
	future := t0.Add(time.Hour)
	l.Record("a", WindowFiveHour, -1, future, t0)
	l.Record("a", WindowFiveHour, 101, future, t0)
	l.Record("a", WindowFiveHour, math.NaN(), future, t0)
	l.Record("a", WindowFiveHour, math.Inf(1), future, t0)
	l.Record("a", WindowFiveHour, 10, t0, t0)          // reset not in the future
	l.Record("a", WindowFiveHour, 10, time.Time{}, t0) // no reset
	l.Record("a", "monthly", 10, future, t0)           // unknown window
	testutil.Equal(t, len(l.Snapshot()), 0)
}

func TestLedger_NilSafe(t *testing.T) {
	var l *Ledger
	l.Record("a", WindowFiveHour, 1, t0.Add(time.Hour), t0)
	testutil.Equal(t, len(l.Snapshot()), 0)
}

func reading(pct float64) *Reading { return &Reading{Pct: pct, ResetsAt: t0.Add(time.Hour)} }

func TestPick(t *testing.T) {
	th := Thresholds{FiveHour: 90, Weekly: 90}
	cands := []string{"default", "personal"}
	tests := []struct {
		name     string
		snap     map[string]Usage
		exclude  []string
		want     string
		wantOver bool
	}{
		{"capped account skipped", map[string]Usage{
			"default":  {FiveHour: reading(100), Weekly: reading(75)},
			"personal": {FiveHour: reading(0), Weekly: reading(0)},
		}, nil, "personal", false},
		{"weekly window counts", map[string]Usage{
			"default":  {FiveHour: reading(10), Weekly: reading(88)},
			"personal": {FiveHour: reading(40), Weekly: reading(20)},
		}, nil, "personal", false},
		{"unknown counts as available", map[string]Usage{
			"default": {FiveHour: reading(60)},
		}, nil, "personal", false},
		{"all unknown picks first", nil, nil, "default", false},
		{"tie goes to candidate order", map[string]Usage{
			"default":  {FiveHour: reading(30)},
			"personal": {FiveHour: reading(30)},
		}, nil, "default", false},
		{"all over threshold picks least over", map[string]Usage{
			"default":  {FiveHour: reading(99)},
			"personal": {FiveHour: reading(95)},
		}, nil, "personal", true},
		{"exclude current account", map[string]Usage{
			"personal": {FiveHour: reading(80)},
		}, []string{"default"}, "personal", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := Pick(th, cands, tc.snap, tc.exclude...)
			testutil.Equal(t, ok, true)
			testutil.Equal(t, got.Account, tc.want)
			testutil.Equal(t, got.OverThreshold, tc.wantOver)
			testutil.Equal(t, len(got.Scores), len(cands)-len(tc.exclude))
		})
	}
}

func TestPick_Scores(t *testing.T) {
	got, _ := Pick(Thresholds{FiveHour: 90, Weekly: 90}, []string{"default", "personal"}, map[string]Usage{
		"default":  {FiveHour: reading(10), Weekly: reading(88)},
		"personal": {FiveHour: reading(40), Weekly: reading(20)},
	})
	testutil.DeepEqual(t, got.Scores, []Score{{Account: "default", Score: 2}, {Account: "personal", Score: 50}})
}

func TestPick_NoCandidates(t *testing.T) {
	_, ok := Pick(Thresholds{FiveHour: 90, Weekly: 90}, []string{"default"}, nil, "default")
	testutil.Equal(t, ok, false)
	_, ok = Pick(Thresholds{FiveHour: 90, Weekly: 90}, nil, nil)
	testutil.Equal(t, ok, false)
}

func TestNormalizeAccount(t *testing.T) {
	testutil.Equal(t, NormalizeAccount(""), "default")
	testutil.Equal(t, NormalizeAccount("default"), "default")
	testutil.Equal(t, NormalizeAccount("personal"), "personal")
}

func TestChoice_String(t *testing.T) {
	c := Choice{Account: "personal", Scores: []Score{{"default", -10}, {"personal", 90}}}
	testutil.Equal(t, c.String(), "personal (scores: default=-10 personal=90)")
}
