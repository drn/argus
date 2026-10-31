package hotkey

import (
	"errors"
	"sync"
	"time"

	"github.com/drn/argus/internal/uxlog"
)

// CGEventType values the tap listens for or must react to.
const (
	cgEventKeyDown                uint32 = 10
	cgEventFlagsChanged           uint32 = 12
	cgEventTapDisabledByTimeout   uint32 = 0xFFFFFFFE
	cgEventTapDisabledByUserInput uint32 = 0xFFFFFFFF
)

// ErrUnsupported is returned by StartTap on platforms without a global
// event tap.
var ErrUnsupported = errors.New("hotkey: global hotkey tap is only supported on macOS")

const (
	tickInterval    = 50 * time.Millisecond
	eventBufferSize = 64
)

// core is the platform-independent half of a Tap: it feeds the Tracker,
// ticks it while armed, and delivers events to onEvent on its own goroutine
// so a slow handler can never stall the OS event tap thread.
type core struct {
	tracker  *Tracker
	onEvent  func(Event)
	events   chan Event
	quit     chan struct{}
	wg       sync.WaitGroup
	now      func() time.Time
	tick     time.Duration
	reenable func()
	stopOnce sync.Once
}

func newCore(combo Combo, onEvent func(Event)) *core {
	c := &core{
		onEvent: onEvent,
		events:  make(chan Event, eventBufferSize),
		quit:    make(chan struct{}),
		now:     time.Now,
		tick:    tickInterval,
	}
	c.tracker = NewTracker(combo, c.enqueue)
	return c
}

func (c *core) enqueue(e Event) {
	select {
	case c.events <- e:
	default:
		uxlog.Log("[hotkey] event queue full, dropped %s", e)
	}
}

func (c *core) start() {
	c.wg.Add(2)
	go c.dispatchLoop()
	go c.tickLoop()
}

func (c *core) stop() {
	c.stopOnce.Do(func() {
		close(c.quit)
		c.wg.Wait()
	})
}

func (c *core) dispatchLoop() {
	defer c.wg.Done()
	for {
		select {
		case <-c.quit:
			return
		case e := <-c.events:
			if c.onEvent != nil {
				c.onEvent(e)
			}
		}
	}
}

func (c *core) tickLoop() {
	defer c.wg.Done()
	t := time.NewTicker(c.tick)
	defer t.Stop()
	for {
		select {
		case <-c.quit:
			return
		case <-t.C:
			if c.tracker.Armed() {
				c.tracker.Tick(c.now())
			}
		}
	}
}

// handle processes one tap callback. flags is only meaningful for
// cgEventFlagsChanged.
func (c *core) handle(typ uint32, flags uint64) {
	switch typ {
	case cgEventTapDisabledByTimeout, cgEventTapDisabledByUserInput:
		uxlog.Log("[hotkey] event tap disabled by system (type=%#x), re-enabling", typ)
		if c.reenable != nil {
			c.reenable()
		}
	case cgEventFlagsChanged:
		c.tracker.FlagsChanged(flags, c.now())
	case cgEventKeyDown:
		c.tracker.KeyDown(c.now())
	}
}
