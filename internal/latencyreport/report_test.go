package latencyreport

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/drn/argus/internal/testutil"
)

func TestReport(t *testing.T) {
	t.Parallel()
	log := "2026/11/04 12:00:00.000 [typing] task=a pane=tasks total_ms=40 queue_ms=20 arrive_ms=5 paint_ms=15 stall_ms=20 quiet_ms=100 overlap=0\n" +
		"2026/11/04 12:00:01.000 [typing] task=a pane=tasks total_ms=100 queue_ms=0 arrive_ms=5 paint_ms=95 stall_ms=0 quiet_ms=10 overlap=1\n" +
		"2026/11/04 12:00:02.000 [typing] task=a pane=hera-coord quiet_ms=0 overlap=0 outcome=no-echo\n" +
		"2026/11/04 12:00:02.000 [typing] malformed\n"
	samples, err := Parse(strings.NewReader(log), time.Time{}, time.Time{})
	testutil.NoError(t, err)
	testutil.Equal(t, len(samples), 3)
	r := Report(samples)
	testutil.Contains(t, r, "tasks all total_ms 2 40.000 100.000 100.000 100.000")
	testutil.Contains(t, r, "tasks quiet total_ms 1 40.000 40.000 40.000 40.000")
	testutil.Contains(t, r, "hera-coord no-echo=1")
	samples, err = Parse(strings.NewReader(log), time.Date(2026, 11, 4, 12, 0, 1, 0, time.Local), time.Date(2026, 11, 4, 12, 0, 1, 0, time.Local))
	testutil.NoError(t, err)
	testutil.Equal(t, len(samples), 1)
}

func TestParseMalformedLines(t *testing.T) {
	t.Parallel()
	valid := "2026/11/04 12:00:00.000 [typing] task=a pane=tasks total_ms=40 queue_ms=20 arrive_ms=5 paint_ms=15 stall_ms=20 quiet_ms=100 overlap=0"
	for _, tc := range []struct{ name, line string }{
		{"unrelated", "ordinary UX log line"},
		{"timestamp", strings.Replace(valid, "2026/11/04", "bad-date", 1)},
		{"pane", strings.Replace(valid, "pane=tasks", "unknown=value", 1)},
		{"number", strings.Replace(valid, "total_ms=40", "total_ms=no", 1)},
		{"nan", strings.Replace(valid, "total_ms=40", "total_ms=NaN", 1)},
		{"infinity", strings.Replace(valid, "total_ms=40", "total_ms=+Inf", 1)},
		{"negative", strings.Replace(valid, "total_ms=40", "total_ms=-1", 1)},
		{"overlap", strings.Replace(valid, "overlap=0", "overlap=broken", 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			samples, err := Parse(strings.NewReader(tc.line), time.Time{}, time.Time{})
			testutil.NoError(t, err)
			testutil.Equal(t, len(samples), 0)
		})
	}
	samples, err := Parse(strings.NewReader(valid), time.Time{}, time.Date(2026, 11, 4, 11, 0, 0, 0, time.Local))
	testutil.NoError(t, err)
	testutil.Equal(t, len(samples), 0)
	_, err = Parse(failingReader{}, time.Time{}, time.Time{})
	testutil.Equal(t, err != nil, true)
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("read failed") }

func TestStallRuns(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, dump string
		want       bool
	}{
		{"main", "goroutine 1 [running]:\ngithub.com/drn/argus/internal/tui.(*App).onTick.func1()\n\ngoroutine 2 [running]:\nother()", true},
		{"other", "goroutine 1 [running]:\nother()\n\ngoroutine 2 [running]:\nrefreshTasksWithIDs()", false},
		{"missing", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) { testutil.Equal(t, MainStalled(tc.dump), tc.want) })
	}
	var runs StallRuns
	base := time.Unix(100, 0)
	runs.Observe(base, true)
	runs.Observe(base.Add(5*time.Millisecond), true)
	runs.Observe(base.Add(10*time.Millisecond), false)
	runs.Observe(base.Add(20*time.Millisecond), true)
	runs.Finish(base.Add(30 * time.Millisecond))
	testutil.Equal(t, len(runs.Runs), 2)
	testutil.Contains(t, runs.Report(30*time.Millisecond), "count=2 median_ms=10.000 p90_ms=10.000 max_ms=10.000 stalled_pct=66.67")
}
