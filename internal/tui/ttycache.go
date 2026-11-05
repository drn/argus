package tui

import (
	"sync"
	"time"

	"github.com/drn/argus/internal/uxlog"
	"github.com/gdamore/tcell/v2"
)

const terminalSizeTTL = time.Second

// tcell queries WindowSize on every Show. Cache only that ioctl, preserving
// the normal Clear/Draw/Show path and immediate SIGWINCH invalidation.
type cachedTTY struct {
	tcell.Tty
	mu   sync.Mutex
	size tcell.WindowSize
	at   time.Time
	now  func() time.Time
}

func newCachedTTY(tty tcell.Tty) *cachedTTY {
	return &cachedTTY{Tty: tty, now: time.Now}
}

func (c *cachedTTY) WindowSize() (tcell.WindowSize, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.at.IsZero() && c.now().Sub(c.at) < terminalSizeTTL {
		return c.size, nil
	}
	size, err := c.Tty.WindowSize()
	if err != nil {
		c.at = time.Time{}
		uxlog.Log("[terminal-size] query failed: %v", err)
		return size, err
	}
	c.size = size
	c.at = c.now()
	return size, nil
}

func (c *cachedTTY) invalidate() {
	c.mu.Lock()
	c.at = time.Time{}
	c.mu.Unlock()
}

func (c *cachedTTY) NotifyResize(cb func()) {
	if cb == nil {
		c.Tty.NotifyResize(nil)
		return
	}
	c.Tty.NotifyResize(func() { c.invalidate(); cb() })
}

func (c *cachedTTY) Start() error {
	c.invalidate()
	defer c.invalidate()
	return c.Tty.Start()
}

func (c *cachedTTY) Stop() error {
	c.invalidate()
	defer c.invalidate()
	return c.Tty.Stop()
}

func cachedTerminalScreen(open func() (tcell.Tty, error), create func(tcell.Tty) (tcell.Screen, error)) (tcell.Screen, error) {
	// Resolve terminfo before opening the device: stock Tty.Close cannot
	// release the original device handle before Start has run.
	cached := newCachedTTY(nil)
	screen, err := create(cached)
	if err != nil {
		return nil, err
	}
	tty, err := open()
	if err != nil {
		return nil, err
	}
	cached.Tty = tty // Init and concurrent access begin only after return
	uxlog.Log("[terminal-size] query cache enabled ttl=%s", terminalSizeTTL)
	return screen, nil
}

// Keep this setup shared with SimulationScreen tests: SetScreen must precede
// mouse/paste enablement because tview otherwise only stores those flags.
func (a *App) setTerminalScreen(raw tcell.Screen) {
	a.screen = &lazyScreen{Screen: raw}
	if a.typing != nil {
		a.screen.onShow = a.typing.flushed
	}
	a.tapp.SetScreen(a.screen)
	a.tapp.EnableMouse(true)
	a.tapp.EnablePaste(true)
}
