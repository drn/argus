package tui

import (
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/drn/argus/internal/testutil"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

type cacheTestTTY struct {
	tcell.Tty
	mu        sync.Mutex
	size      tcell.WindowSize
	err       error
	calls     int
	cb        func()
	readDone  chan struct{}
	drain     sync.Once
	lifecycle func() error
}

func newCacheTestTTY() *cacheTestTTY {
	return &cacheTestTTY{size: tcell.WindowSize{Width: 80, Height: 24}, readDone: make(chan struct{})}
}
func (f *cacheTestTTY) WindowSize() (tcell.WindowSize, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return f.size, f.err
}
func (f *cacheTestTTY) NotifyResize(cb func()) { f.mu.Lock(); defer f.mu.Unlock(); f.cb = cb }
func (f *cacheTestTTY) resize(w int) {
	f.mu.Lock()
	f.size.Width = w
	cb := f.cb
	f.mu.Unlock()
	if cb != nil {
		cb()
	}
}
func (f *cacheTestTTY) count() int { f.mu.Lock(); defer f.mu.Unlock(); return f.calls }
func (f *cacheTestTTY) Start() error {
	if f.lifecycle != nil {
		return f.lifecycle()
	}
	return nil
}
func (f *cacheTestTTY) Stop() error {
	if f.lifecycle != nil {
		return f.lifecycle()
	}
	return nil
}
func (f *cacheTestTTY) Close() error                { return nil }
func (f *cacheTestTTY) Drain() error                { f.drain.Do(func() { close(f.readDone) }); return nil }
func (f *cacheTestTTY) Read([]byte) (int, error)    { <-f.readDone; return 0, io.EOF }
func (f *cacheTestTTY) Write(b []byte) (int, error) { return len(b), nil }

func TestTTYCacheShowsAndExpiry(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	f := newCacheTestTTY()
	var c *cachedTTY
	screen, err := cachedTerminalScreen(func() (tcell.Tty, error) { return f, nil }, func(tty tcell.Tty) (tcell.Screen, error) {
		c = tty.(*cachedTTY)
		return tcell.NewTerminfoScreenFromTty(tty)
	})
	testutil.NoError(t, err)
	start := time.Now()
	var elapsed atomic.Int64
	c.now = func() time.Time { return start.Add(time.Duration(elapsed.Load())) }
	testutil.NoError(t, screen.Init())
	defer screen.Fini()
	c.invalidate()
	f.mu.Lock()
	f.calls = 0
	f.mu.Unlock()
	for range 20 {
		screen.Show()
	}
	testutil.Equal(t, f.count(), 1)
	f.resize(100) // no callback if resize registration is broken: assertion catches it
	screen.Show()
	testutil.Equal(t, f.count(), 2)
	w, _ := screen.Size()
	testutil.Equal(t, w, 100)
	elapsed.Add(int64(time.Second - time.Nanosecond))
	screen.Show()
	testutil.Equal(t, f.count(), 2)
	elapsed.Add(int64(time.Nanosecond))
	f.mu.Lock()
	f.size.Width = 120
	f.mu.Unlock() // missed SIGWINCH
	screen.Show()
	testutil.Equal(t, f.count(), 3)
	w, _ = screen.Size()
	testutil.Equal(t, w, 120)
}

func TestTTYCacheInvalidatesBeforeResizeCallback(t *testing.T) {
	f := newCacheTestTTY()
	c := newCachedTTY(f)
	_, err := c.WindowSize()
	testutil.NoError(t, err)
	var got tcell.WindowSize
	c.NotifyResize(func() { got, err = c.WindowSize() })
	f.resize(101)
	testutil.NoError(t, err)
	testutil.Equal(t, got.Width, 101)
	testutil.Equal(t, f.count(), 2)
	c.NotifyResize(nil)
	f.mu.Lock()
	testutil.Equal(t, f.cb == nil, true)
	f.mu.Unlock()
}

func TestTTYCacheErrorsAndLifecycle(t *testing.T) {
	for _, op := range []string{"start", "resume", "stop"} {
		t.Run(op, func(t *testing.T) {
			f := newCacheTestTTY()
			c := newCachedTTY(f)
			if op == "resume" {
				testutil.NoError(t, c.Start())
				testutil.NoError(t, c.Stop())
			}
			_, err := c.WindowSize()
			testutil.NoError(t, err)
			if op == "stop" {
				testutil.NoError(t, c.Stop())
			} else {
				testutil.NoError(t, c.Start())
			}
			f.mu.Lock()
			f.err = io.ErrUnexpectedEOF
			f.size.Width = 102
			f.mu.Unlock()
			for range 2 {
				size, err := c.WindowSize()
				testutil.ErrorIs(t, err, io.ErrUnexpectedEOF)
				testutil.Equal(t, size.Width, 102)
			}
			testutil.Equal(t, f.count(), 3)
			f.mu.Lock()
			f.err = nil
			f.mu.Unlock()
			size, err := c.WindowSize()
			testutil.NoError(t, err)
			testutil.Equal(t, size.Width, 102)
			_, err = c.WindowSize()
			testutil.NoError(t, err)
			testutil.Equal(t, f.count(), 4)
		})
	}
}

func TestTTYCacheConcurrentResize(t *testing.T) {
	f := newCacheTestTTY()
	c := newCachedTTY(f)
	c.NotifyResize(func() { _, _ = c.WindowSize() })
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 100 {
				_, _ = c.WindowSize()
			}
		})
	}
	for range 2 {
		wg.Go(func() {
			for range 100 {
				f.resize(90)
			}
		})
	}
	wg.Wait()
	f.resize(123)
	size, err := c.WindowSize()
	testutil.NoError(t, err)
	testutil.Equal(t, size.Width, 123)
}

func TestTTYCacheLifecycleDelegation(t *testing.T) {
	for _, operation := range []string{"start", "stop"} {
		t.Run(operation, func(t *testing.T) {
			f := newCacheTestTTY()
			c := newCachedTTY(f)
			_, err := c.WindowSize()
			testutil.NoError(t, err)
			f.lifecycle = func() error {
				_, err := c.WindowSize()
				testutil.NoError(t, err)
				testutil.Equal(t, f.count(), 2) // invalidated BEFORE delegate
				return io.ErrClosedPipe
			}
			if operation == "start" {
				err = c.Start()
			} else {
				err = c.Stop()
			}
			testutil.ErrorIs(t, err, io.ErrClosedPipe)
			_, err = c.WindowSize()
			testutil.NoError(t, err)
			testutil.Equal(t, f.count(), 3) // delegate's query must not survive either
		})
	}
}

func TestCachedScreenFactory(t *testing.T) {
	sentinel := errors.New("screen setup failed")
	t.Run("open error", func(t *testing.T) {
		_, err := cachedTerminalScreen(func() (tcell.Tty, error) { return nil, sentinel }, func(tcell.Tty) (tcell.Screen, error) { return tcell.NewSimulationScreen("UTF-8"), nil })
		testutil.ErrorIs(t, err, sentinel)
	})
	t.Run("screen error", func(t *testing.T) {
		_, err := cachedTerminalScreen(func() (tcell.Tty, error) {
			t.Fatal("must not open a device after screen construction fails")
			return nil, nil
		}, func(tty tcell.Tty) (tcell.Screen, error) {
			_, ok := tty.(*cachedTTY)
			testutil.Equal(t, ok, true)
			return nil, sentinel
		})
		testutil.ErrorIs(t, err, sentinel)
	})
}

// Observe real setup calls too: injected paste events alone bypass the
// terminal's paste mode and cannot detect EnablePaste-before-SetScreen.
type terminalSetupScreen struct {
	tcell.Screen
	initialized, mouse, paste bool
}

func (s *terminalSetupScreen) Init() error {
	err := s.Screen.Init()
	s.initialized = err == nil
	return err
}
func (s *terminalSetupScreen) EnableMouse(flags ...tcell.MouseFlags) {
	s.mouse = s.initialized
	s.Screen.EnableMouse(flags...)
}
func (s *terminalSetupScreen) EnablePaste() {
	s.paste = s.initialized
	s.Screen.EnablePaste()
}

func TestSmoke_CachedScreenSetup(t *testing.T) {
	f := newCacheTestTTY()
	sim := tcell.NewSimulationScreen("UTF-8")
	observed := &terminalSetupScreen{Screen: sim}
	raw, err := cachedTerminalScreen(func() (tcell.Tty, error) { return f, nil }, func(tty tcell.Tty) (tcell.Screen, error) {
		_, ok := tty.(*cachedTTY)
		testutil.Equal(t, ok, true)
		return observed, nil
	})
	testutil.NoError(t, err)
	tracker := newTypingTracker(true)
	var flushes atomic.Int64
	tracker.now = func() time.Time { flushes.Add(1); return time.Now() }
	app := &App{tapp: tview.NewApplication(), typing: tracker}
	app.setTerminalScreen(raw)
	testutil.Equal(t, observed.mouse, true)
	testutil.Equal(t, observed.paste, true)
	field := &pasteCapture{Box: tview.NewBox()}
	app.tapp.SetRoot(field, true).SetFocus(field)
	stop := runApp(t, app.tapp)
	defer stop()
	app.tapp.QueueEvent(tcell.NewEventPaste(true))
	app.tapp.QueueEvent(tcell.NewEventKey(tcell.KeyRune, 'x', 0))
	app.tapp.QueueEvent(tcell.NewEventPaste(false))
	syncUI(t, app.tapp)
	readUI(t, app.tapp, func() {
		testutil.Equal(t, field.getPasted(), "x")
		testutil.Equal(t, flushes.Load() > 0, true)
		sim.SetSize(100, 30)
	})
	app.tapp.QueueEvent(tcell.NewEventResize(100, 30))
	syncUI(t, app.tapp)
	readUI(t, app.tapp, func() { _, _, w, h := field.GetRect(); testutil.Equal(t, w, 100); testutil.Equal(t, h, 30) })
}
