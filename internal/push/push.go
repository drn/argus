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
)

// Manager owns VAPID keys + handles fan-out.
type Manager struct {
	db        *db.DB
	pubKey    string
	privKey   string
	subject   string
	httpClient *http.Client

	muThrottle sync.Mutex
	lastSent   map[string]time.Time // key: taskID, value: last push time
}

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
	if subj == "" {
		subj = "mailto:argus@localhost"
		_ = d.SetConfigValue(keySubject, subj)
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

// ForgetTask removes the throttle entry for a task. Called when a task's
// session has exited so the in-memory lastSent map doesn't grow without
// bound. Idempotent.
func (m *Manager) ForgetTask(taskID string) {
	if m == nil {
		return
	}
	m.muThrottle.Lock()
	delete(m.lastSent, "idle:"+taskID)
	m.muThrottle.Unlock()
}

// ResetThrottle clears the throttle entry for a key so the next Notify with
// that key fires immediately. Used when an agent transitions back to busy:
// once the user's been re-engaged with output, the next idle event is a fresh
// "task done" signal and shouldn't be suppressed by the 5-minute window from
// an earlier mid-run pause.
func (m *Manager) ResetThrottle(throttleKey string) {
	if m == nil || throttleKey == "" {
		return
	}
	m.muThrottle.Lock()
	delete(m.lastSent, throttleKey)
	m.muThrottle.Unlock()
}

// Notify is a fire-and-forget notification: title + body + optional taskId for
// deep-linking. Throttled to 1 push per key per 5 minutes (key="" disables
// throttling). The throttle is set only if at least one subscription exists,
// so an empty-subs state can't poison the next 5 minutes once the user
// subscribes mid-run.
func (m *Manager) Notify(throttleKey, title, body, taskID string) {
	if m == nil {
		return
	}
	if throttleKey != "" {
		m.muThrottle.Lock()
		if t, ok := m.lastSent[throttleKey]; ok && time.Since(t) < 5*time.Minute {
			m.muThrottle.Unlock()
			uxlog.Log("[push] notify throttled key=%q (last sent %s ago)", throttleKey, time.Since(t).Round(time.Second))
			return
		}
		m.muThrottle.Unlock()
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
		m.muThrottle.Lock()
		m.lastSent[throttleKey] = time.Now()
		m.muThrottle.Unlock()
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
	sub := &webpush.Subscription{
		Endpoint: s.Endpoint,
		Keys: webpush.Keys{
			P256dh: s.P256dh,
			Auth:   s.Auth,
		},
	}
	resp, err := webpush.SendNotification(payload, sub, &webpush.Options{
		HTTPClient:      m.httpClient,
		Subscriber:      m.subject,
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
