package hotkey

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/drn/argus/internal/testutil"
)

type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

func waitEvents(t *testing.T, ch <-chan Event, n int) []Event {
	t.Helper()
	var got []Event
	deadline := time.After(5 * time.Second)
	for len(got) < n {
		select {
		case e := <-ch:
			got = append(got, e)
		case <-deadline:
			t.Fatalf("timed out after %v", got)
		}
	}
	return got
}

func TestCore_HoldViaTicker(t *testing.T) {
	out := make(chan Event, 8)
	c := newCore(mustCombo(t, "right_option"), func(e Event) { out <- e })
	clk := &clock{now: time.Unix(0, 0)}
	c.now = clk.Now
	c.tick = time.Millisecond
	c.start()
	defer c.stop()

	c.handle(cgEventFlagsChanged, FlagOption|FlagRightOption)
	clk.Advance(DefaultArmDelay)
	testutil.DeepEqual(t, waitEvents(t, out, 1), []Event{Began})
	clk.Advance(time.Second)
	c.handle(cgEventFlagsChanged, 0)
	testutil.DeepEqual(t, waitEvents(t, out, 1), []Event{Ended})
}

func TestCore_KeyDownCancels(t *testing.T) {
	out := make(chan Event, 8)
	c := newCore(mustCombo(t, "right_option"), func(e Event) { out <- e })
	c.start()
	defer c.stop()
	c.handle(cgEventFlagsChanged, FlagOption|FlagRightOption)
	c.handle(cgEventKeyDown, 0)
	testutil.DeepEqual(t, waitEvents(t, out, 1), []Event{Cancelled})
}

func TestCore_ReenableOnDisabled(t *testing.T) {
	for _, typ := range []uint32{cgEventTapDisabledByTimeout, cgEventTapDisabledByUserInput} {
		t.Run("type", func(t *testing.T) {
			var n atomic.Int32
			c := newCore(mustCombo(t, "right_option"), nil)
			c.reenable = func() { n.Add(1) }
			c.handle(typ, 0)
			testutil.Equal(t, n.Load(), int32(1))
		})
	}
	t.Run("nil reenable", func(t *testing.T) {
		c := newCore(mustCombo(t, "right_option"), nil)
		c.handle(cgEventTapDisabledByTimeout, 0)
	})
}

func TestCore_IgnoresOtherTypes(t *testing.T) {
	c := newCore(mustCombo(t, "right_option"), nil)
	c.handle(1, FlagOption|FlagRightOption)
	testutil.False(t, c.tracker.Armed())
}

func TestCore_QueueFullDrops(t *testing.T) {
	c := newCore(mustCombo(t, "right_option"), nil)
	for range eventBufferSize + 3 {
		c.enqueue(Began)
	}
	testutil.Equal(t, len(c.events), eventBufferSize)
}

func TestCore_NilOnEventAndStopIdempotent(t *testing.T) {
	c := newCore(mustCombo(t, "right_option"), nil)
	c.start()
	c.enqueue(Began)
	c.stop()
	c.stop()
}
