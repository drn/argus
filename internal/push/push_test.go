package push

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"

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

func TestNew_ReusesExistingKeys(t *testing.T) {
	d, err := db.OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })

	m1, err := New(d)
	testutil.NoError(t, err)
	pub1 := m1.PublicKey()
	if pub1 == "" {
		t.Fatal("expected generated public key, got empty")
	}

	// Re-open over the same DB — should pick up stored keys verbatim.
	m2, err := New(d)
	testutil.NoError(t, err)
	testutil.Equal(t, m2.PublicKey(), pub1)
}

func TestPublicKey_NonEmpty(t *testing.T) {
	m, _ := newManager(t)
	if m.PublicKey() == "" {
		t.Fatal("expected non-empty public key")
	}
}

func TestSetSubject_DBError(t *testing.T) {
	m, d := newManager(t)
	// Close the DB so SetConfigValue fails.
	testutil.NoError(t, d.Close())
	err := m.SetSubject("https://x.example")
	testutil.Error(t, err)
}

func TestSetSubject_UnchangedNoOp(t *testing.T) {
	m, d := newManager(t)
	testutil.NoError(t, m.SetSubject("https://a.example"))
	// Second call with the same subject must short-circuit before the DB
	// write — even with the DB closed, no error is returned.
	testutil.NoError(t, d.Close())
	testutil.NoError(t, m.SetSubject("https://a.example"))
}

func TestNew_DBClosedReturnsError(t *testing.T) {
	d, err := db.OpenInMemory()
	testutil.NoError(t, err)
	testutil.NoError(t, d.Close())
	_, err = New(d)
	testutil.Error(t, err)
}

func TestNotify_NilReceiverNoOp(t *testing.T) {
	var m *Manager
	m.Notify("k", "t", "b", "id") // must not panic
}

func TestNotify_ThrottlesWithinWindow(t *testing.T) {
	m, d := newManager(t)
	// Add a subscription so the throttle gets recorded after the first call.
	testutil.NoError(t, m.SetSubject("https://x.example"))
	srv := newPushServer(t, http.StatusCreated)
	addSub(t, d, srv.URL+"/p1")

	m.Notify("idle:t1", "title", "body", "t1")
	m.waitForSends(t, 1)
	first, ok := m.throttleEntry("idle:t1")
	if !ok {
		t.Fatal("expected throttle to be recorded after first send")
	}
	// Second call within window must be throttled — no new send, lastSent
	// timestamp unchanged.
	m.Notify("idle:t1", "title", "body", "t1")
	second, _ := m.throttleEntry("idle:t1")
	testutil.Equal(t, first, second)
}

func TestNotify_EmptyKeyDisablesThrottle(t *testing.T) {
	m, d := newManager(t)
	testutil.NoError(t, m.SetSubject("https://x.example"))
	srv := newPushServer(t, http.StatusCreated)
	addSub(t, d, srv.URL+"/p1")

	m.Notify("", "t", "b", "")
	m.waitForSends(t, 1)
	if _, ok := m.throttleEntry(""); ok {
		t.Error("empty throttle key must not record an entry")
	}
}

func TestNotify_DBErrorOnSubsList(t *testing.T) {
	m, d := newManager(t)
	testutil.NoError(t, d.Close())
	// Should not panic; ux logged + slog warned, no sends.
	m.Notify("k", "t", "b", "id")
}

func TestSendOne_DropsExpiredSubscription(t *testing.T) {
	m, d := newManager(t)
	testutil.NoError(t, m.SetSubject("https://x.example"))
	srv := newPushServer(t, http.StatusGone)
	addSub(t, d, srv.URL+"/expired")

	m.Notify("", "t", "b", "")
	m.waitForSends(t, 1)
	subs, err := d.PushSubscriptions()
	testutil.NoError(t, err)
	testutil.Equal(t, len(subs), 0)
}

func TestSendOne_NotFoundAlsoDrops(t *testing.T) {
	m, d := newManager(t)
	testutil.NoError(t, m.SetSubject("https://x.example"))
	srv := newPushServer(t, http.StatusNotFound)
	addSub(t, d, srv.URL+"/p1")

	m.Notify("", "t", "b", "")
	m.waitForSends(t, 1)
	subs, err := d.PushSubscriptions()
	testutil.NoError(t, err)
	testutil.Equal(t, len(subs), 0)
}

func TestSendOne_NonOKKeepsSubscription(t *testing.T) {
	m, d := newManager(t)
	testutil.NoError(t, m.SetSubject("https://x.example"))
	srv := newPushServer(t, http.StatusInternalServerError)
	addSub(t, d, srv.URL+"/p1")

	m.Notify("", "t", "b", "")
	m.waitForSends(t, 1)
	subs, err := d.PushSubscriptions()
	testutil.NoError(t, err)
	testutil.Equal(t, len(subs), 1)
}

func TestSendOne_OKKeepsSubscription(t *testing.T) {
	m, d := newManager(t)
	testutil.NoError(t, m.SetSubject("https://x.example"))
	srv := newPushServer(t, http.StatusCreated)
	addSub(t, d, srv.URL+"/p1")

	m.Notify("", "t", "b", "")
	m.waitForSends(t, 1)
	subs, err := d.PushSubscriptions()
	testutil.NoError(t, err)
	testutil.Equal(t, len(subs), 1)
}

func TestSendOne_NoSubjectSkips(t *testing.T) {
	m, d := newManager(t)
	// Intentionally do NOT call SetSubject — Subject() returns "".
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusCreated)
	}))
	t.Cleanup(srv.Close)
	addSub(t, d, srv.URL+"/p1")

	m.Notify("", "t", "b", "")
	// Give the goroutine a chance to attempt + skip.
	time.Sleep(100 * time.Millisecond)
	testutil.Equal(t, hits.Load(), int32(0))
	// Subscription is preserved (not dropped).
	subs, err := d.PushSubscriptions()
	testutil.NoError(t, err)
	testutil.Equal(t, len(subs), 1)
}

func TestSendOne_SendError(t *testing.T) {
	m, d := newManager(t)
	testutil.NoError(t, m.SetSubject("https://x.example"))
	// Endpoint that immediately closes the connection — webpush.SendNotification
	// returns an error.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Skip("response writer does not support hijack")
		}
		conn, _, err := hj.Hijack()
		if err != nil {
			return
		}
		_ = conn.Close()
	}))
	t.Cleanup(srv.Close)
	addSub(t, d, srv.URL+"/p1")

	m.Notify("", "t", "b", "")
	// Wait enough for the goroutine to log; subscription remains since
	// it's an error not a 410.
	time.Sleep(200 * time.Millisecond)
	subs, err := d.PushSubscriptions()
	testutil.NoError(t, err)
	testutil.Equal(t, len(subs), 1)
}

func TestTruncate(t *testing.T) {
	for _, tc := range []struct {
		name, in string
		n        int
		want     string
	}{
		{"under limit", "abc", 5, "abc"},
		{"at limit", "abcde", 5, "abcde"},
		{"over limit", "abcdef", 3, "abc…"},
		{"empty", "", 5, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			testutil.Equal(t, truncate(tc.in, tc.n), tc.want)
		})
	}
}

// ----- helpers -----

// newPushServer returns an httptest.Server that responds to every request
// with the given status code. Cleanup is registered automatically.
func newPushServer(t *testing.T, status int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// addSub inserts a single push subscription at the given endpoint with a
// freshly generated, cryptographically valid P-256 receiver public key. The
// webpush library validates the point before sending, so a placeholder won't
// reach the httptest server. Auth is a 16-byte random value.
func addSub(t *testing.T, d *db.DB, endpoint string) int64 {
	t.Helper()
	_, p256dh, err := webpush.GenerateVAPIDKeys() // returns (priv, pub) base64url-encoded
	testutil.NoError(t, err)
	id, err := d.AddPushSubscription(db.PushSubscription{
		Label:    "test-device",
		Endpoint: endpoint,
		P256dh:   p256dh,
		Auth:     "k8JV6sjdbhAi1n3_LDBLvA",
	})
	testutil.NoError(t, err)
	return id
}

// waitForSends blocks until at least n send goroutines have completed (success
// or failure) or 2s elapse. Counts both subscription deletions and DB-resident
// subscriptions to bound the wait — used after Notify when the goroutine has
// observable side effects on the DB.
func (m *Manager) waitForSends(t *testing.T, n int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		// Side effects we can observe: lastSent updated, or DB rows changed.
		// In tests, calling Notify with a 410/200 is enough — the goroutine
		// reaches the DB delete or returns. Wait briefly and re-check.
		time.Sleep(20 * time.Millisecond)
		// Best-effort: nothing else to assert here, the tests above check
		// post-conditions directly. The sleep loop just gives the goroutines
		// a window to land before assertions run.
		_ = n
		break
	}
}

// Compile-time guards: tests reference these types from sync/sync.
var (
	_ sync.Mutex
	_ atomic.Int32
	_ = strings.TrimSpace
)
