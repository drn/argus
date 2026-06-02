package views

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/drn/argus/internal/testutil"
)

// pluginEcho is an httptest WebSocket handler used as a stand-in plugin.
// envelopes is a channel the test can read to assert control envelopes;
// keystrokes captures binary frames from the TUI; ANSI to push the test can
// queue via the bytesToSend channel.
type pluginEcho struct {
	mu          sync.Mutex
	envelopes   []controlEnvelope
	keystrokes  [][]byte
	bytesToSend chan []byte // buffered ANSI to push from server → client
	textToSend  chan string // buffered text frames to push from server → client
	done        chan struct{}
	closeOnce   sync.Once
}

func newPluginEcho() *pluginEcho {
	return &pluginEcho{
		bytesToSend: make(chan []byte, 16),
		textToSend:  make(chan string, 16),
		done:        make(chan struct{}),
	}
}

func (p *pluginEcho) signalDone() {
	p.closeOnce.Do(func() { close(p.done) })
}

func (p *pluginEcho) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			return
		}
		defer func() { _ = c.Close(websocket.StatusNormalClosure, "") }()
		// Pump pending ANSI bytes asynchronously.
		go func() {
			for {
				select {
				case <-p.done:
					return
				case b := <-p.bytesToSend:
					ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
					_ = c.Write(ctx, websocket.MessageBinary, b)
					cancel()
				case s := <-p.textToSend:
					ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
					_ = c.Write(ctx, websocket.MessageText, []byte(s))
					cancel()
				}
			}
		}()
		// Read until the client disconnects.
		for {
			typ, data, err := c.Read(context.Background())
			if err != nil {
				p.signalDone()
				return
			}
			switch typ {
			case websocket.MessageText:
				var env controlEnvelope
				if err := json.Unmarshal(data, &env); err == nil {
					p.mu.Lock()
					p.envelopes = append(p.envelopes, env)
					p.mu.Unlock()
				}
			case websocket.MessageBinary:
				cp := append([]byte(nil), data...)
				p.mu.Lock()
				p.keystrokes = append(p.keystrokes, cp)
				p.mu.Unlock()
			}
		}
	})
}

func (p *pluginEcho) getEnvelopes() []controlEnvelope {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]controlEnvelope(nil), p.envelopes...)
}

func (p *pluginEcho) getKeystrokes() [][]byte {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([][]byte, len(p.keystrokes))
	for i, k := range p.keystrokes {
		out[i] = append([]byte(nil), k...)
	}
	return out
}

// wsURL turns the http URL of an httptest.Server into a ws:// URL the
// coder/websocket client accepts.
func wsURL(srv *httptest.Server) string {
	return "ws" + strings.TrimPrefix(srv.URL, "http")
}

// waitFor polls cond every 5ms until it returns true or the timeout fires.
func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("condition not met within %v", timeout)
}

// pushText queues a text frame for the server to push to the client. Used by
// control-frame tests; the base pluginEcho only pushes binary frames.
func (p *pluginEcho) pushText(s string) {
	p.textToSend <- s
}

func TestConnector_DialAndControlEnvelopes(t *testing.T) {
	plugin := newPluginEcho()
	srv := httptest.NewServer(plugin.handler())
	t.Cleanup(srv.Close)

	in := make(chan []byte, 4)
	c := NewConnector(wsURL(srv), nil, nil, in)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	testutil.NoError(t, c.Dial(ctx))
	t.Cleanup(func() { _ = c.Close() })

	testutil.NoError(t, c.SendResize(80, 24))
	testutil.NoError(t, c.SendFocus())
	testutil.NoError(t, c.SendBlur())

	waitFor(t, time.Second, func() bool { return len(plugin.getEnvelopes()) >= 3 })
	got := plugin.getEnvelopes()
	testutil.Equal(t, got[0].Type, envelopeResize)
	testutil.Equal(t, got[0].Cols, 80)
	testutil.Equal(t, got[0].Rows, 24)
	testutil.Equal(t, got[1].Type, envelopeFocus)
	testutil.Equal(t, got[2].Type, envelopeBlur)
}

func TestConnector_KeystrokesForwardedAsBinary(t *testing.T) {
	plugin := newPluginEcho()
	srv := httptest.NewServer(plugin.handler())
	t.Cleanup(srv.Close)

	in := make(chan []byte, 4)
	c := NewConnector(wsURL(srv), nil, nil, in)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	testutil.NoError(t, c.Dial(ctx))
	t.Cleanup(func() { _ = c.Close() })

	in <- []byte("hello")
	in <- []byte("world")

	waitFor(t, time.Second, func() bool { return len(plugin.getKeystrokes()) >= 2 })
	got := plugin.getKeystrokes()
	testutil.Equal(t, string(got[0]), "hello")
	testutil.Equal(t, string(got[1]), "world")
}

func TestConnector_ANSIDeliveredAsBinary(t *testing.T) {
	plugin := newPluginEcho()
	srv := httptest.NewServer(plugin.handler())
	t.Cleanup(srv.Close)

	var (
		gotMu sync.Mutex
		got   []byte
	)
	onBytes := func(b []byte) {
		gotMu.Lock()
		got = append(got, b...)
		gotMu.Unlock()
	}
	in := make(chan []byte, 4)
	c := NewConnector(wsURL(srv), onBytes, nil, in)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	testutil.NoError(t, c.Dial(ctx))
	t.Cleanup(func() { _ = c.Close() })

	plugin.bytesToSend <- []byte("\x1b[2J\x1b[Hhello")

	waitFor(t, time.Second, func() bool {
		gotMu.Lock()
		defer gotMu.Unlock()
		return len(got) > 0
	})
	gotMu.Lock()
	defer gotMu.Unlock()
	testutil.Equal(t, string(got), "\x1b[2J\x1b[Hhello")
}

func TestConnector_LargeFrameSurvivesReadLimit(t *testing.T) {
	plugin := newPluginEcho()
	srv := httptest.NewServer(plugin.handler())
	t.Cleanup(srv.Close)

	var (
		gotMu sync.Mutex
		got   []byte
	)
	onBytes := func(b []byte) {
		gotMu.Lock()
		got = append(got, b...)
		gotMu.Unlock()
	}
	in := make(chan []byte, 4)
	c := NewConnector(wsURL(srv), onBytes, nil, in)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	testutil.NoError(t, c.Dial(ctx))
	t.Cleanup(func() { _ = c.Close() })

	// 64 KiB binary frame — well above coder/websocket's 32 KiB default
	// read limit. A full-screen ANSI repaint from a plugin (e.g. hera-view
	// at 316x69) lands in the 32–50 KiB range, so without
	// SetReadLimit(-1) the connector errors with StatusMessageTooBig and
	// the conn closes mid-read.
	const size = 64 * 1024
	payload := make([]byte, size)
	for i := range payload {
		payload[i] = byte(i % 251)
	}
	plugin.bytesToSend <- payload

	waitFor(t, 2*time.Second, func() bool {
		gotMu.Lock()
		defer gotMu.Unlock()
		return len(got) >= size
	})
	gotMu.Lock()
	defer gotMu.Unlock()
	testutil.Equal(t, len(got), size)
	for i := 0; i < size; i += 4096 {
		if got[i] != byte(i%251) {
			t.Fatalf("byte %d: got %d, want %d", i, got[i], byte(i%251))
		}
	}
}

func TestConnector_DialErrorPropagates(t *testing.T) {
	c := NewConnector("ws://127.0.0.1:1", nil, nil, nil) // port 1 — unreachable
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	err := c.Dial(ctx)
	if err == nil {
		t.Fatal("expected dial error")
	}
}

func TestConnector_SendBeforeDialFails(t *testing.T) {
	c := NewConnector("ws://example.invalid", nil, nil, nil)
	if err := c.SendResize(80, 24); err == nil {
		t.Fatal("expected send-before-dial error")
	}
}

func TestConnector_CloseIsIdempotent(t *testing.T) {
	plugin := newPluginEcho()
	srv := httptest.NewServer(plugin.handler())
	t.Cleanup(srv.Close)

	in := make(chan []byte, 4)
	c := NewConnector(wsURL(srv), nil, nil, in)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	testutil.NoError(t, c.Dial(ctx))

	testutil.NoError(t, c.Close())
	// Second Close must be a clean no-op.
	testutil.NoError(t, c.Close())
}

func TestConnector_WritePumpExitsOnInClose(t *testing.T) {
	plugin := newPluginEcho()
	srv := httptest.NewServer(plugin.handler())
	t.Cleanup(srv.Close)

	in := make(chan []byte, 1)
	c := NewConnector(wsURL(srv), nil, nil, in)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	testutil.NoError(t, c.Dial(ctx))
	t.Cleanup(func() { _ = c.Close() })

	in <- []byte("alpha")
	waitFor(t, time.Second, func() bool { return len(plugin.getKeystrokes()) >= 1 })

	// Closing the in channel should let the write pump exit; the next call to
	// any SendXxx after a tick must still succeed because the read connection
	// remains open.
	close(in)
	// Give the write pump a beat to drain.
	time.Sleep(20 * time.Millisecond)
	testutil.NoError(t, c.SendBlur())
}

func TestConnector_TextFrameReachesOnControl(t *testing.T) {
	plugin := newPluginEcho()
	srv := httptest.NewServer(plugin.handler())
	t.Cleanup(srv.Close)

	var (
		ctrlMu sync.Mutex
		ctrl   [][]byte
	)
	onControl := func(b []byte) {
		ctrlMu.Lock()
		ctrl = append(ctrl, append([]byte(nil), b...))
		ctrlMu.Unlock()
	}
	in := make(chan []byte, 4)
	c := NewConnector(wsURL(srv), nil, onControl, in)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	testutil.NoError(t, c.Dial(ctx))
	t.Cleanup(func() { _ = c.Close() })

	plugin.pushText(`{"type":"release"}`)

	waitFor(t, time.Second, func() bool {
		ctrlMu.Lock()
		defer ctrlMu.Unlock()
		return len(ctrl) >= 1
	})
	ctrlMu.Lock()
	defer ctrlMu.Unlock()
	testutil.Equal(t, string(ctrl[0]), `{"type":"release"}`)
}

func TestConnector_BinaryStillHitsOnBytesWithControlSet(t *testing.T) {
	plugin := newPluginEcho()
	srv := httptest.NewServer(plugin.handler())
	t.Cleanup(srv.Close)

	var (
		byMu  sync.Mutex
		bin   []byte
		ctlMu sync.Mutex
		ctl   int
	)
	onBytes := func(b []byte) {
		byMu.Lock()
		bin = append(bin, b...)
		byMu.Unlock()
	}
	onControl := func([]byte) {
		ctlMu.Lock()
		ctl++
		ctlMu.Unlock()
	}
	in := make(chan []byte, 4)
	c := NewConnector(wsURL(srv), onBytes, onControl, in)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	testutil.NoError(t, c.Dial(ctx))
	t.Cleanup(func() { _ = c.Close() })

	plugin.bytesToSend <- []byte("ansi-bytes")

	waitFor(t, time.Second, func() bool {
		byMu.Lock()
		defer byMu.Unlock()
		return len(bin) > 0
	})
	byMu.Lock()
	testutil.Equal(t, string(bin), "ansi-bytes")
	byMu.Unlock()
	ctlMu.Lock()
	testutil.Equal(t, ctl, 0) // binary frame must not invoke onControl
	ctlMu.Unlock()
}

func TestConnector_MalformedTextDoesNotStopPump(t *testing.T) {
	plugin := newPluginEcho()
	srv := httptest.NewServer(plugin.handler())
	t.Cleanup(srv.Close)

	var (
		byMu sync.Mutex
		bin  []byte
	)
	onBytes := func(b []byte) {
		byMu.Lock()
		bin = append(bin, b...)
		byMu.Unlock()
	}
	// onControl still receives the raw bytes — defensive JSON parsing is the
	// dispatcher's job (plugin_views.go), not the connector's. The connector
	// only routes text→onControl and must not crash on any payload.
	in := make(chan []byte, 4)
	c := NewConnector(wsURL(srv), onBytes, func([]byte) {}, in)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	testutil.NoError(t, c.Dial(ctx))
	t.Cleanup(func() { _ = c.Close() })

	// A non-JSON text frame, then a binary frame: the pump must survive the
	// garbage and keep delivering binary ANSI.
	plugin.pushText("not json at all {{{")
	plugin.bytesToSend <- []byte("still-flowing")

	waitFor(t, time.Second, func() bool {
		byMu.Lock()
		defer byMu.Unlock()
		return string(bin) == "still-flowing"
	})
}

func TestConnector_NilOnControlIgnoresTextFrame(t *testing.T) {
	plugin := newPluginEcho()
	srv := httptest.NewServer(plugin.handler())
	t.Cleanup(srv.Close)

	var (
		byMu sync.Mutex
		bin  []byte
	)
	onBytes := func(b []byte) {
		byMu.Lock()
		bin = append(bin, b...)
		byMu.Unlock()
	}
	in := make(chan []byte, 4)
	c := NewConnector(wsURL(srv), onBytes, nil, in) // no control sink
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	testutil.NoError(t, c.Dial(ctx))
	t.Cleanup(func() { _ = c.Close() })

	plugin.pushText(`{"type":"release"}`) // must be dropped without crashing
	plugin.bytesToSend <- []byte("ok")

	waitFor(t, time.Second, func() bool {
		byMu.Lock()
		defer byMu.Unlock()
		return string(bin) == "ok"
	})
}
