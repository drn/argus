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
	t.Cleanup(func() { _ = d.Close() })
	m, err := New(d)
	testutil.NoError(t, err)
	return m, d
}

// throttleEntry mirrors the production read path but exposes the lookup to
// tests so they don't have to reach into the unexported mutex inline.
// Lives in _test.go to keep the production binary surface unchanged.
func (m *Manager) throttleEntry(key string) (time.Time, bool) {
	m.muThrottle.Lock()
	defer m.muThrottle.Unlock()
	t, ok := m.lastSent[key]
	return t, ok
}

func TestNotify_NoSubsDoesNotSetThrottle(t *testing.T) {
	m, _ := newManager(t)
	// No subscriptions registered.
	m.Notify("idle:t1", "title", "body", "t1")
	if _, set := m.throttleEntry("idle:t1"); set {
		t.Fatalf("throttle was set despite zero subscriptions; would suppress real pushes for %s", throttleDuration)
	}
}

func TestSetSubject_PersistsAndUpdates(t *testing.T) {
	m, d := newManager(t)
	testutil.Equal(t, m.Subject(), "")

	testutil.NoError(t, m.SetSubject("https://host.tailnet.ts.net"))
	testutil.Equal(t, m.Subject(), "https://host.tailnet.ts.net")

	got, err := d.GetConfigValue(keySubject)
	testutil.NoError(t, err)
	testutil.Equal(t, got, "https://host.tailnet.ts.net")
}

func TestSetSubject_EmptyAndNilNoOp(t *testing.T) {
	m, _ := newManager(t)
	testutil.NoError(t, m.SetSubject("https://a.example"))
	testutil.NoError(t, m.SetSubject(""))
	testutil.Equal(t, m.Subject(), "https://a.example")

	var nilM *Manager
	testutil.NoError(t, nilM.SetSubject("https://b.example")) // must not panic
	testutil.Equal(t, nilM.Subject(), "")
}

func TestNew_ClearsLegacyBadSubject(t *testing.T) {
	d, err := db.OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })
	testutil.NoError(t, d.SetConfigValue(keySubject, legacyBadSubject))

	m, err := New(d)
	testutil.NoError(t, err)
	testutil.Equal(t, m.Subject(), "")

	got, err := d.GetConfigValue(keySubject)
	testutil.NoError(t, err)
	testutil.Equal(t, got, "")
}
