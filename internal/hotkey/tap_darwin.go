//go:build darwin

package hotkey

import (
	"errors"
	"fmt"
	"runtime"
	"sync"
	"time"
	"unsafe"

	"github.com/ebitengine/purego"

	"github.com/drn/argus/internal/uxlog"
)

const (
	coreGraphicsPath   = "/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics"
	coreFoundationPath = "/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation"

	kCGSessionEventTap          uint32 = 1
	kCGHeadInsertEventTap       uint32 = 0
	kCGEventTapOptionListenOnly uint32 = 1

	tapEventMask = uint64(1)<<cgEventFlagsChanged | uint64(1)<<cgEventKeyDown

	startTimeout    = 5 * time.Second
	stopPollEvery   = 100 * time.Millisecond
	stopMaxAttempts = 20
)

// ErrTapCreate is returned when CGEventTapCreate yields NULL, which almost
// always means Input Monitoring has not been granted to the responsible process.
var ErrTapCreate = errors.New("hotkey: CGEventTapCreate returned NULL (grant Input Monitoring in System Settings > Privacy & Security)")

// ErrTapTimeout is returned when the tap's run-loop thread fails to come up.
var ErrTapTimeout = errors.New("hotkey: timed out starting event tap")

var (
	loadOnce sync.Once
	loadErr  error

	cgEventTapCreate              func(tap, place, options uint32, mask uint64, cb, refcon uintptr) uintptr
	cgEventTapEnable              func(port uintptr, enable bool)
	cgEventGetFlags               func(event uintptr) uint64
	cgPreflightListenEventAccess  func() bool
	cgRequestListenEventAccess    func() bool
	cfMachPortCreateRunLoopSource func(alloc, port uintptr, order int) uintptr
	cfMachPortInvalidate          func(port uintptr)
	cfRunLoopGetCurrent           func() uintptr
	cfRunLoopAddSource            func(rl, src, mode uintptr)
	cfRunLoopRun                  func()
	cfRunLoopStop                 func(rl uintptr)
	cfRelease                     func(ref uintptr)
	kCFRunLoopCommonModes         uintptr
	tapCallbackPtr                uintptr
)

func loadFrameworks() error {
	loadOnce.Do(func() {
		defer func() {
			if r := recover(); r != nil {
				loadErr = fmt.Errorf("hotkey: load frameworks: %v", r)
			}
		}()
		cg, err := purego.Dlopen(coreGraphicsPath, purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			loadErr = fmt.Errorf("hotkey: dlopen CoreGraphics: %w", err)
			return
		}
		cf, err := purego.Dlopen(coreFoundationPath, purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			loadErr = fmt.Errorf("hotkey: dlopen CoreFoundation: %w", err)
			return
		}
		purego.RegisterLibFunc(&cgEventTapCreate, cg, "CGEventTapCreate")
		purego.RegisterLibFunc(&cgEventTapEnable, cg, "CGEventTapEnable")
		purego.RegisterLibFunc(&cgEventGetFlags, cg, "CGEventGetFlags")
		purego.RegisterLibFunc(&cgPreflightListenEventAccess, cg, "CGPreflightListenEventAccess")
		purego.RegisterLibFunc(&cgRequestListenEventAccess, cg, "CGRequestListenEventAccess")
		purego.RegisterLibFunc(&cfMachPortCreateRunLoopSource, cf, "CFMachPortCreateRunLoopSource")
		purego.RegisterLibFunc(&cfMachPortInvalidate, cf, "CFMachPortInvalidate")
		purego.RegisterLibFunc(&cfRunLoopGetCurrent, cf, "CFRunLoopGetCurrent")
		purego.RegisterLibFunc(&cfRunLoopAddSource, cf, "CFRunLoopAddSource")
		purego.RegisterLibFunc(&cfRunLoopRun, cf, "CFRunLoopRun")
		purego.RegisterLibFunc(&cfRunLoopStop, cf, "CFRunLoopStop")
		purego.RegisterLibFunc(&cfRelease, cf, "CFRelease")
		sym, err := purego.Dlsym(cf, "kCFRunLoopCommonModes")
		if err != nil {
			loadErr = fmt.Errorf("hotkey: dlsym kCFRunLoopCommonModes: %w", err)
			return
		}
		kCFRunLoopCommonModes = **(**uintptr)(unsafe.Pointer(&sym)) //nolint:gosec // sym is the address of a CFStringRef global; dereference it.
		// purego callbacks are never freed, so create exactly one per process.
		tapCallbackPtr = purego.NewCallback(tapCallback)
	})
	return loadErr
}

var (
	registryMu sync.Mutex
	registry   = map[uintptr]*Tap{}
	nextTapID  uintptr
)

func registerTap(t *Tap) uintptr {
	registryMu.Lock()
	defer registryMu.Unlock()
	nextTapID++
	registry[nextTapID] = t
	return nextTapID
}

func unregisterTap(id uintptr) {
	registryMu.Lock()
	delete(registry, id)
	registryMu.Unlock()
}

func lookupTap(id uintptr) *Tap {
	registryMu.Lock()
	defer registryMu.Unlock()
	return registry[id]
}

func tapCallback(proxy, typ, event, refcon uintptr) uintptr {
	t := lookupTap(refcon)
	if t == nil {
		return event
	}
	et := uint32(typ) //nolint:gosec // CGEventType is a 32-bit C enum; upper register bits are undefined.
	var flags uint64
	if et == cgEventFlagsChanged && event != 0 {
		flags = cgEventGetFlags(event)
	}
	t.c.handle(et, flags)
	return event
}

// Tap is a running listen-only global keyboard event tap.
type Tap struct {
	c         *core
	id        uintptr
	done      chan struct{}
	closeOnce sync.Once

	// port is written once on the run-loop thread before the loop runs and
	// read only from callbacks on that same thread.
	port uintptr

	mu        sync.Mutex
	runLoop   uintptr
	abandoned bool
}

// InputMonitoringGranted reports whether this process may listen to global
// keyboard events (the Input Monitoring TCC permission).
func InputMonitoringGranted() bool {
	if loadFrameworks() != nil {
		return false
	}
	return cgPreflightListenEventAccess()
}

// RequestInputMonitoring prompts for (or registers the process under) Input
// Monitoring and reports whether access is currently granted.
func RequestInputMonitoring() bool {
	if loadFrameworks() != nil {
		return false
	}
	return cgRequestListenEventAccess()
}

// StartTap installs a global event tap that reports combo hold transitions to
// onEvent on a dedicated goroutine. It returns an error, never blocks
// indefinitely, when the tap cannot be created.
func StartTap(combo Combo, onEvent func(Event)) (*Tap, error) {
	if combo.IsZero() {
		return nil, ErrEmptyCombo
	}
	if err := loadFrameworks(); err != nil {
		uxlog.Log("[hotkey] tap start failed: %v", err)
		return nil, err
	}
	t := &Tap{c: newCore(combo, onEvent), done: make(chan struct{})}
	t.c.reenable = func() {
		if t.port != 0 {
			cgEventTapEnable(t.port, true)
		}
	}
	t.id = registerTap(t)
	ready := make(chan error, 1)
	go t.run(ready)
	select {
	case err := <-ready:
		if err != nil {
			unregisterTap(t.id)
			uxlog.Log("[hotkey] tap start failed: %v", err)
			return nil, err
		}
	case <-time.After(startTimeout):
		t.stopRunLoop()
		unregisterTap(t.id)
		uxlog.Log("[hotkey] tap start failed: %v", ErrTapTimeout)
		return nil, ErrTapTimeout
	}
	t.c.start()
	uxlog.Log("[hotkey] tap started combo=%s", combo)
	return t, nil
}

func (t *Tap) run(ready chan<- error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer close(t.done)

	port := cgEventTapCreate(kCGSessionEventTap, kCGHeadInsertEventTap, kCGEventTapOptionListenOnly, tapEventMask, tapCallbackPtr, t.id)
	if port == 0 {
		ready <- ErrTapCreate
		return
	}
	src := cfMachPortCreateRunLoopSource(0, port, 0)
	if src == 0 {
		cfMachPortInvalidate(port)
		cfRelease(port)
		ready <- errors.New("hotkey: CFMachPortCreateRunLoopSource returned NULL")
		return
	}
	defer func() {
		cfMachPortInvalidate(port)
		cfRelease(src)
		cfRelease(port)
	}()
	t.port = port
	rl := cfRunLoopGetCurrent()
	cfRunLoopAddSource(rl, src, kCFRunLoopCommonModes)

	t.mu.Lock()
	if t.abandoned {
		t.mu.Unlock()
		return
	}
	t.runLoop = rl
	t.mu.Unlock()
	ready <- nil
	cfRunLoopRun()
}

// stopRunLoop stops the tap thread. CFRunLoopStop issued before CFRunLoopRun
// starts is discarded by CoreFoundation, so it is retried until the thread exits.
func (t *Tap) stopRunLoop() {
	t.mu.Lock()
	t.abandoned = true
	rl := t.runLoop
	t.mu.Unlock()
	if rl == 0 {
		return
	}
	for range stopMaxAttempts {
		cfRunLoopStop(rl)
		select {
		case <-t.done:
			return
		case <-time.After(stopPollEvery):
		}
	}
	uxlog.Log("[hotkey] tap run loop did not stop after %d attempts", stopMaxAttempts)
}

// Close stops the tap and its event delivery. It is safe to call more than
// once and on a nil Tap.
func (t *Tap) Close() {
	if t == nil {
		return
	}
	t.closeOnce.Do(func() {
		t.stopRunLoop()
		t.c.stop()
		unregisterTap(t.id)
		uxlog.Log("[hotkey] tap closed")
	})
}
