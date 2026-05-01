package push

import (
	"testing"
	"time"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/testutil"
)

func newManager(t *testing.T) (*Manager, *db.DB) {
	t.Helper()
	d, err := db.OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { d.Close() })
	m, err := New(d)
	testutil.NoError(t, err)
	return m, d
}

func TestNotify_NoSubsDoesNotSetThrottle(t *testing.T) {
	m, _ := newManager(t)
	// No subscriptions registered.
	m.Notify("idle:t1", "title", "body", "t1")
	m.muThrottle.Lock()
	_, set := m.lastSent["idle:t1"]
	m.muThrottle.Unlock()
	if set {
		t.Fatalf("throttle was set despite zero subscriptions; would suppress real pushes for 5 min")
	}
}

func TestResetThrottle(t *testing.T) {
	m, _ := newManager(t)
	m.muThrottle.Lock()
	m.lastSent["idle:t1"] = time.Now()
	m.muThrottle.Unlock()

	m.ResetThrottle("idle:t1")

	m.muThrottle.Lock()
	_, set := m.lastSent["idle:t1"]
	m.muThrottle.Unlock()
	if set {
		t.Fatalf("ResetThrottle did not clear the entry")
	}
}

func TestResetThrottle_EmptyKeyNoOp(t *testing.T) {
	m, _ := newManager(t)
	m.muThrottle.Lock()
	m.lastSent["idle:t1"] = time.Now()
	m.muThrottle.Unlock()

	m.ResetThrottle("")

	m.muThrottle.Lock()
	_, set := m.lastSent["idle:t1"]
	m.muThrottle.Unlock()
	if !set {
		t.Fatalf("empty-key reset should not affect other entries")
	}
}

func TestResetThrottle_NilManager(t *testing.T) {
	var m *Manager
	m.ResetThrottle("idle:x") // must not panic
}

func TestForgetTask_ClearsThrottle(t *testing.T) {
	m, _ := newManager(t)
	m.muThrottle.Lock()
	m.lastSent["idle:t1"] = time.Now()
	m.muThrottle.Unlock()

	m.ForgetTask("t1")

	m.muThrottle.Lock()
	_, set := m.lastSent["idle:t1"]
	m.muThrottle.Unlock()
	if set {
		t.Fatalf("ForgetTask did not clear throttle")
	}
}
