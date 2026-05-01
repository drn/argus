// Package push wraps webpush-go with VAPID key management persisted in the
// argus DB and a fan-out helper that prunes expired subscriptions.
package push

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/uxlog"
)

const (
	keyPublic  = "push.vapid_public"
	keyPrivate = "push.vapid_private"
	keySubject = "push.vapid_subject"
	defaultTTL = 60 // seconds

	// throttleDuration is how long a per-key throttle blocks repeated Notify
	// calls. Single source of truth — log messages reference it via
	// time.Since() rather than hard-coding "5 minutes" prose.
	throttleDuration = 5 * time.Minute
)

// Manager owns VAPID keys + handles fan-out.
type Manager struct {
	db         *db.DB
	pubKey     string
	privKey    string
	httpClient *http.Client

	// muSubject guards subject. The VAPID JWT `sub` claim must be a real
	// mailto: or https:// URL — Apple WebPush rejects unroutable values like
	// `mailto:argus@localhost` with HTTP 403. The API server updates this
	// from each authenticated request's Origin/Host so it tracks whatever
	// https URL the PWA is currently being served from (typically the user's
	// tailscale-funnel URL).
	muSubject sync.RWMutex
	subject   string

	muThrottle sync.Mutex
	// lastSent tracks per-throttle-key send times. Keys are the same strings
	// callers pass to Notify (e.g. "idle:<taskID>"); empty key disables
	// throttling entirely. Values are the time of the last successful entry
	// into the fan-out path.
	lastSent map[string]time.Time
}

// legacyBadSubject is the original hardcoded default that Apple WebPush
// rejects with 403. Manager.New() clears it so SetSubject will repopulate
// from the next authenticated request.
const legacyBadSubject = "mailto:argus@localhost"

// New loads or generates a VAPID keypair from the DB and returns a Manager.
func New(d *db.DB) (*Manager, error) {
	pub, err := d.GetConfigValue(keyPublic)
	if err != nil {
		return nil, err
	}
	priv, err := d.GetConfigValue(keyPrivate)
	if err != nil {
		return nil, err
	}
	if pub == "" || priv == "" {
		priv, pub, err = webpush.GenerateVAPIDKeys()
		if err != nil {
			return nil, fmt.Errorf("generate VAPID: %w", err)
		}
		if err := d.SetConfigValue(keyPublic, pub); err != nil {
			return nil, err
		}
		if err := d.SetConfigValue(keyPrivate, priv); err != nil {
			return nil, err
		}
		slog.Info("push: generated new VAPID keypair")
	}
	subj, err := d.GetConfigValue(keySubject)
	if err != nil {
		return nil, err
	}
	// Drop the legacy bad default so Apple-bound pushes don't keep failing
	// with 403 until the next authenticated request rewrites it.
	if subj == legacyBadSubject {
		_ = d.SetConfigValue(keySubject, "")
		subj = ""
	}
	return &Manager{
		db:         d,
		pubKey:     pub,
		privKey:    priv,
		subject:    subj,
		httpClient: &http.Client{Timeout: 10 * time.Second},
		lastSent:   make(map[string]time.Time),
	}, nil
}

// PublicKey returns the urlsafe-base64 public VAPID key for the SPA to feed
// into PushManager.subscribe.
func (m *Manager) PublicKey() string { return m.pubKey }

// Subject returns the current VAPID JWT `sub` claim. Empty when no
// authenticated request has supplied an https origin yet.
func (m *Manager) Subject() string {
	if m == nil {
		return ""
	}
	m.muSubject.RLock()
	defer m.muSubject.RUnlock()
	return m.subject
}

// SetSubject updates the VAPID JWT `sub` claim and persists it to the DB.
// No-op for nil receivers, empty input, or unchanged values. Apple WebPush
// rejects values that aren't a valid mailto: or https:// URL — callers
// should pass an https origin (e.g. "https://host.tailnet.ts.net") that
// matches the URL the PWA is being served from.
func (m *Manager) SetSubject(subject string) error {
	if m == nil || subject == "" {
		return nil
	}
	m.muSubject.Lock()
	defer m.muSubject.Unlock()
	if m.subject == subject {
		return nil
	}
	if err := m.db.SetConfigValue(keySubject, subject); err != nil {
		return err
	}
	m.subject = subject
	uxlog.Log("[push] vapid subject updated to %q", subject)
	return nil
}

// ForgetTask removes the per-task idle throttle entry. Called when a task's
// session has exited so the in-memory lastSent map doesn't grow without
// bound. Idempotent. Implemented in terms of ResetThrottle so the
// "idle:<taskID>" key schema lives in exactly one place.
func (m *Manager) ForgetTask(taskID string) {
	m.ResetThrottle("idle:" + taskID)
}

// ResetThrottle clears the throttle entry for a key so the next Notify with
// that key fires immediately. Used when an agent transitions back to busy:
// once the user's been re-engaged with output, the next idle event is a fresh
// "task done" signal and shouldn't be suppressed by the throttleDuration
// window from an earlier mid-run pause.
func (m *Manager) ResetThrottle(throttleKey string) {
	if m == nil || throttleKey == "" {
		return
	}
	m.muThrottle.Lock()
	delete(m.lastSent, throttleKey)
	m.muThrottle.Unlock()
}

// Notify is a fire-and-forget notification: title + body + optional taskId for
// deep-linking. Throttled to 1 push per key per throttleDuration (key=""
// disables throttling). The throttle is recorded only if at least one
// subscription exists, so an empty-subs state can't poison the next throttle
// window once the user subscribes mid-run.
//
// Concurrency: the throttle mutex is held continuously from the throttle
// check through the subscription query and the lastSent write. This
// serializes concurrent Notify calls with the same key — only the first
// reaches the fan-out, the rest see the recorded send and bail. Holding
// across the (fast, in-memory SQLite) read is the simplest way to close the
// check-then-set TOCTOU window.
func (m *Manager) Notify(throttleKey, title, body, taskID string) {
	if m == nil {
		return
	}
	if throttleKey != "" {
		m.muThrottle.Lock()
		defer m.muThrottle.Unlock()
		if t, ok := m.lastSent[throttleKey]; ok && time.Since(t) < throttleDuration {
			uxlog.Log("[push] notify throttled key=%q (last sent %s ago)", throttleKey, time.Since(t).Round(time.Second))
			return
		}
	}

	subs, err := m.db.PushSubscriptions()
	if err != nil {
		slog.Warn("push: list subscriptions failed", "err", err)
		uxlog.Log("[push] list subscriptions failed: %v", err)
		return
	}
	if len(subs) == 0 {
		uxlog.Log("[push] notify skipped: no subscriptions registered (key=%q title=%q)", throttleKey, title)
		return
	}

	if throttleKey != "" {
		m.lastSent[throttleKey] = time.Now()
	}

	payload, _ := json.Marshal(map[string]string{
		"title":  title,
		"body":   body,
		"taskId": taskID,
	})
	uxlog.Log("[push] notify fan-out subs=%d key=%q title=%q taskId=%q", len(subs), throttleKey, title, taskID)

	for _, s := range subs {
		go m.sendOne(s, payload)
	}
}

func (m *Manager) sendOne(s db.PushSubscription, payload []byte) {
	subj := m.Subject()
	if subj == "" {
		uxlog.Log("[push] send skipped id=%d: vapid subject not yet set (waiting for first authenticated request)", s.ID)
		return
	}
	sub := &webpush.Subscription{
		Endpoint: s.Endpoint,
		Keys: webpush.Keys{
			P256dh: s.P256dh,
			Auth:   s.Auth,
		},
	}
	resp, err := webpush.SendNotification(payload, sub, &webpush.Options{
		HTTPClient:      m.httpClient,
		Subscriber:      subj,
		VAPIDPublicKey:  m.pubKey,
		VAPIDPrivateKey: m.privKey,
		TTL:             defaultTTL,
	})
	if err != nil {
		slog.Warn("push: send failed", "endpoint", truncate(s.Endpoint, 60), "err", err)
		uxlog.Log("[push] send failed id=%d endpoint=%s err=%v", s.ID, truncate(s.Endpoint, 60), err)
		return
	}
	defer resp.Body.Close()
	// Push services return 410 Gone or 404 for permanently expired subs.
	if resp.StatusCode == 410 || resp.StatusCode == 404 {
		slog.Info("push: dropping expired subscription", "id", s.ID)
		uxlog.Log("[push] dropping expired subscription id=%d status=%d", s.ID, resp.StatusCode)
		_ = m.db.DeletePushSubscriptionByEndpoint(s.Endpoint)
	} else if resp.StatusCode >= 400 {
		slog.Warn("push: non-OK response", "status", resp.StatusCode, "id", s.ID)
		uxlog.Log("[push] non-OK response id=%d status=%d endpoint=%s", s.ID, resp.StatusCode, truncate(s.Endpoint, 60))
	} else {
		uxlog.Log("[push] sent id=%d status=%d", s.ID, resp.StatusCode)
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
