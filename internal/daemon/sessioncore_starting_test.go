package daemon

import (
	"sync"
	"testing"
	"time"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/app/agentview"
	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/testutil"
)

// startGapSession records the calls the handlers deliver once they find it.
type startGapSession struct {
	agent.SessionHandle
	mu      sync.Mutex
	wrote   []byte
	resized bool
}

func (s *startGapSession) WriteInput(p []byte, _ agentview.InputOrigin) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.wrote = append(s.wrote, p...)
	return len(p), nil
}

func (s *startGapSession) Resize(_, _ uint16) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.resized = true
	return nil
}

// startingRunner mimics agent.Runner's start gap: Get is nil until the session
// is published, GetStarting waits for it.
type startingRunner struct {
	fakeSupClient
	sess        *startGapSession
	publishedAt time.Time
}

func (r *startingRunner) published() bool { return time.Now().After(r.publishedAt) }

func (r *startingRunner) Get(string) agent.SessionHandle {
	if r.published() {
		return r.sess
	}
	return nil
}

func (r *startingRunner) GetStarting(string) agent.SessionHandle {
	for !r.published() {
		time.Sleep(time.Millisecond)
	}
	return r.sess
}

// TestSessionCore_InputRidesOutStartGap pins that WriteInput and Resize use
// the runner's GetStarting when it has one, and plain Get otherwise.
func TestSessionCore_InputRidesOutStartGap(t *testing.T) {
	newCore := func(r agent.SessionRunner) *sessionCore {
		return newSessionCore(r, config.DefaultConfig, make(chan struct{}))
	}
	t.Run("write waits for the session", func(t *testing.T) {
		r := &startingRunner{sess: &startGapSession{}, publishedAt: time.Now().Add(50 * time.Millisecond)}
		var resp StatusResp
		testutil.NoError(t, newCore(r).WriteInput(&WriteReq{TaskID: "t", Data: []byte("hi")}, &resp))
		testutil.Equal(t, resp.Error, "")
		testutil.Equal(t, string(r.sess.wrote), "hi")
	})
	t.Run("resize waits for the session", func(t *testing.T) {
		r := &startingRunner{sess: &startGapSession{}, publishedAt: time.Now().Add(50 * time.Millisecond)}
		var resp StatusResp
		testutil.NoError(t, newCore(r).Resize(&ResizeReq{TaskID: "t", Rows: 24, Cols: 80}, &resp))
		testutil.Equal(t, resp.Error, "")
		testutil.True(t, r.sess.resized)
	})
	t.Run("runner without GetStarting falls back to Get", func(t *testing.T) {
		f := &fakeSupClient{} // Get returns nil, no GetStarting
		c := newCore(f)
		var resp StatusResp
		testutil.NoError(t, c.WriteInput(&WriteReq{TaskID: "t", Data: []byte("x")}, &resp))
		testutil.Equal(t, resp.Error, "session not found")
		resp = StatusResp{}
		testutil.NoError(t, c.Resize(&ResizeReq{TaskID: "t"}, &resp))
		testutil.Equal(t, resp.Error, "session not found")
		testutil.Equal(t, len(f.getCalls), 2)
	})
}
