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

// Notify is a fire-and-forget notification: title + body + optional taskId for
// deep-linking. Throttled to 1 push per task per 5 minutes (key="" disables
// throttling).
func (m *Manager) Notify(throttleKey, title, body, taskID string) {
	if m == nil {
		return
	}
	if throttleKey != "" {
		m.muThrottle.Lock()
		if t, ok := m.lastSent[throttleKey]; ok && time.Since(t) < 5*time.Minute {
			m.muThrottle.Unlock()
			return
		}
		m.lastSent[throttleKey] = time.Now()
		m.muThrottle.Unlock()
	}

	subs, err := m.db.PushSubscriptions()
	if err != nil {
		slog.Warn("push: list subscriptions failed", "err", err)
		return
	}
	if len(subs) == 0 {
		return
	}

	payload, _ := json.Marshal(map[string]string{
		"title":  title,
		"body":   body,
		"taskId": taskID,
	})

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
		return
	}
	defer resp.Body.Close()
	// Push services return 410 Gone or 404 for permanently expired subs.
	if resp.StatusCode == 410 || resp.StatusCode == 404 {
		slog.Info("push: dropping expired subscription", "id", s.ID)
		_ = m.db.DeletePushSubscriptionByEndpoint(s.Endpoint)
	} else if resp.StatusCode >= 400 {
		slog.Warn("push: non-OK response", "status", resp.StatusCode, "id", s.ID)
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
