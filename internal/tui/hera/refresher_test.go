package hera

import (
	"testing"
	"time"

	"github.com/drn/argus/internal/testutil"
)

func TestRefresher_FirstScheduleFiresImmediately(t *testing.T) {
	n := 0
	r := NewRefresher(100*time.Millisecond, func() { n++ })
	base := time.Unix(1000, 0)
	r.SetNow(func() time.Time { return base })

	testutil.Equal(t, r.Schedule(), true) // first ever request fires
	testutil.Equal(t, n, 1)
}

func TestRefresher_CoalescesWithinWindow(t *testing.T) {
	n := 0
	r := NewRefresher(100*time.Millisecond, func() { n++ })
	now := time.Unix(1000, 0)
	r.SetNow(func() time.Time { return now })

	r.Schedule() // fires, n=1, lastRun=1000
	testutil.Equal(t, n, 1)

	now = now.Add(40 * time.Millisecond)
	testutil.Equal(t, r.Schedule(), false) // within window, held pending
	testutil.Equal(t, n, 1)

	now = now.Add(40 * time.Millisecond) // total 80ms < 100ms
	testutil.Equal(t, r.Schedule(), false)
	testutil.Equal(t, n, 1)

	now = now.Add(40 * time.Millisecond)  // total 120ms ≥ 100ms
	testutil.Equal(t, r.Schedule(), true) // window elapsed → fires
	testutil.Equal(t, n, 2)
}

func TestRefresher_FlushForcesPending(t *testing.T) {
	n := 0
	r := NewRefresher(100*time.Millisecond, func() { n++ })
	now := time.Unix(1000, 0)
	r.SetNow(func() time.Time { return now })

	r.Schedule() // fires, n=1
	now = now.Add(10 * time.Millisecond)
	testutil.Equal(t, r.Schedule(), false) // pending, not due
	testutil.Equal(t, n, 1)

	testutil.Equal(t, r.Flush(), true) // forces the pending rebuild
	testutil.Equal(t, n, 2)

	// Nothing pending now → Flush is a no-op.
	testutil.Equal(t, r.Flush(), false)
	testutil.Equal(t, n, 2)
}

func TestRefresher_DefaultDebounceWhenNonPositive(t *testing.T) {
	r := NewRefresher(0, func() {})
	testutil.Equal(t, r.debounce, DefaultRefreshDebounce)
}
