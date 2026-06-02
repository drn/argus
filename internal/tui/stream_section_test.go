package tui

import (
	"testing"
	"time"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/testutil"
)

func newAppForStreamTest(t *testing.T) (*App, *fakePluginConnector) {
	t.Helper()
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, true)
	fake := &fakePluginConnector{}
	app.pluginConnFactory = func(url string, onBytes func([]byte), _ func([]byte), _ <-chan []byte) pluginConnector {
		fake.onBytes = onBytes
		return fake
	}
	return app, fake
}

func TestApp_OpenStreamSection_DialsAndPushesBytes(t *testing.T) {
	app, fake := newAppForStreamTest(t)
	fake.bytesToReceive = [][]byte{[]byte("hello")}

	bytesIn := make(chan []byte, 4)
	keysOut := make(chan []byte, 4)
	app.openStreamSection("ludwig", "Live", "ws://x", bytesIn, keysOut)

	// The factory was invoked synchronously; Dial runs in a background
	// goroutine. Wait briefly for it to fire and push the seeded bytes.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if fake.dialed.Load() {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !fake.dialed.Load() {
		t.Fatal("connector did not dial within deadline")
	}

	select {
	case got := <-bytesIn:
		testutil.Equal(t, string(got), "hello")
	case <-time.After(time.Second):
		t.Fatal("bytes never arrived on bytesIn channel")
	}

	if fake.focusedCount.Load() != 1 {
		t.Fatalf("expected 1 focus call, got %d", fake.focusedCount.Load())
	}
}

func TestApp_CloseStreamSection_BlursAndCloses(t *testing.T) {
	app, fake := newAppForStreamTest(t)
	bytesIn := make(chan []byte, 4)
	keysOut := make(chan []byte, 4)

	app.openStreamSection("ludwig", "Live", "ws://x", bytesIn, keysOut)
	// Wait for the dial goroutine to register the connector. Without this,
	// closeStreamSection might race ahead and see no entry.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		app.streamConnsMu.Lock()
		n := len(app.streamConns)
		app.streamConnsMu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	app.closeStreamSection("ludwig", "Live")
	if fake.blurredCount.Load() != 1 {
		t.Fatalf("expected 1 blur, got %d", fake.blurredCount.Load())
	}
	if fake.closedCount.Load() != 1 {
		t.Fatalf("expected 1 close, got %d", fake.closedCount.Load())
	}
	app.streamConnsMu.Lock()
	n := len(app.streamConns)
	app.streamConnsMu.Unlock()
	testutil.Equal(t, n, 0)
}

func TestApp_CloseStreamSection_NoopWhenNotOpen(t *testing.T) {
	app, fake := newAppForStreamTest(t)
	// Close without open — no connector to find. Should not panic, should
	// not call blur/close on anything.
	app.closeStreamSection("ludwig", "Missing")
	testutil.Equal(t, fake.blurredCount.Load(), int32(0))
	testutil.Equal(t, fake.closedCount.Load(), int32(0))
}

func TestApp_OpenStreamSection_ReplacesExisting(t *testing.T) {
	app, fake1 := newAppForStreamTest(t)
	bytesIn := make(chan []byte, 4)
	keysOut := make(chan []byte, 4)
	app.openStreamSection("ludwig", "Live", "ws://x", bytesIn, keysOut)

	// Swap the factory so the second open builds a different connector.
	fake2 := &fakePluginConnector{}
	app.pluginConnFactory = func(url string, onBytes func([]byte), _ func([]byte), _ <-chan []byte) pluginConnector {
		fake2.onBytes = onBytes
		return fake2
	}
	app.openStreamSection("ludwig", "Live", "ws://x", bytesIn, keysOut)

	// fake1 was the stale connector; openStreamSection closed it defensively.
	if fake1.closedCount.Load() != 1 {
		t.Fatalf("expected stale connector closed, got %d closes", fake1.closedCount.Load())
	}
}
