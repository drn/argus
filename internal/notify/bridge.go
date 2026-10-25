package notify

import (
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"time"
)

const bridgeLease = 30 * time.Second

// BridgeRoute is resolved by the daemon's existing bridge authority seam.
type BridgeRoute struct {
	Live, Idle, NeedsInput    bool
	Generation, Boot, Session string
}

type BridgeEvidence string

const (
	BridgeFound   BridgeEvidence = "found"
	BridgeAbsent  BridgeEvidence = "absent"
	BridgeUnknown BridgeEvidence = "unknown"
)

// BridgeClaim fences a single delivery; authorization is required before submit.
type BridgeClaim struct {
	Token      string    `json:"token"`
	Text       string    `json:"text"`
	TaskID     string    `json:"-"`
	DeliveryID string    `json:"delivery_id"`
	Generation string    `json:"-"`
	Boot       string    `json:"-"`
	Session    string    `json:"-"`
	Started    time.Time `json:"-"`
	Expires    time.Time `json:"-"`
	Authorized bool      `json:"-"`
}

// BridgeMarker is emitted in the submitted user row for crash recovery.
func BridgeMarker(id string) string { return "[argus-delivery:" + id + "]" }

// SetBridgeRoute wires authority and transcript evidence before serving.
func (n *Notifier) SetBridgeRoute(route func(string) BridgeRoute, evidence func(BridgeClaim) BridgeEvidence) {
	n.bridgeRoute, n.bridgeEvidence = route, evidence
}

func (n *Notifier) route(taskID string) BridgeRoute {
	if n.bridgeRoute == nil {
		return BridgeRoute{}
	}
	return n.bridgeRoute(taskID)
}

func (n *Notifier) ClaimBridge(taskID, generation, boot string, now time.Time) *BridgeClaim {
	route := n.route(taskID)
	if !route.Live || !route.Idle || route.NeedsInput || route.Generation != generation || route.Boot != boot {
		return nil
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	d := n.pending[taskID]
	if d == nil || n.inFlight[taskID] != "" || d.bridgeFallback || d.bridgeAmbiguous || now.After(d.deadline) {
		return nil
	}
	select {
	case <-d.cancelCh:
		return nil
	default:
	}
	if c := d.bridgeClaim; c != nil {
		if c.Generation != generation || c.Boot != boot || !now.Before(c.Expires) || c.Authorized {
			return nil
		}
		out := *c
		return &out
	}
	var key [16]byte
	if _, err := rand.Read(key[:]); err != nil {
		return nil
	}
	d.bridgeClaim = &BridgeClaim{Token: hex.EncodeToString(key[:]), Text: d.text + "\n" + BridgeMarker(d.deliveryID), TaskID: taskID, DeliveryID: d.deliveryID, Generation: generation, Boot: boot, Session: route.Session, Started: now, Expires: now.Add(bridgeLease)}
	d.bridgeOffers++
	logDelivery(slog.LevelInfo, "bridge claimed", taskID, d.deliveryID)
	out := *d.bridgeClaim
	return &out
}

func (n *Notifier) AuthorizeBridge(taskID, token, generation, boot string, now time.Time) bool {
	route := n.route(taskID)
	if !route.Live || !route.Idle || route.NeedsInput || route.Generation != generation || route.Boot != boot {
		return false
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	d := n.pending[taskID]
	if d == nil || n.inFlight[taskID] != "" || d.bridgeAmbiguous || now.After(d.deadline) {
		return false
	}
	select {
	case <-d.cancelCh:
		return false
	default:
	}
	c := d.bridgeClaim
	if c == nil || c.Token != token || c.Generation != generation || c.Boot != boot || !now.Before(c.Expires) {
		return false
	}
	// Repeated authorization cannot grant a second submission.
	if c.Authorized {
		return false
	}
	c.Authorized = true
	logDelivery(slog.LevelInfo, "bridge authorized", taskID, d.deliveryID)
	return true
}

func (n *Notifier) AckBridge(taskID, token string, now time.Time) bool {
	n.mu.Lock()
	d := n.pending[taskID]
	valid := d != nil && d.bridgeClaim != nil && d.bridgeClaim.Token == token && d.bridgeClaim.Authorized && !now.After(d.deadline)
	var id string
	if valid {
		id = d.deliveryID
		n.removeAndAdvanceLocked(taskID, id, true)
	}
	n.mu.Unlock()
	if !valid {
		return false
	}
	logDelivery(slog.LevelInfo, "bridge submitted", taskID, id)
	return true
}

// reconcileBridge returns true while the mod owns the route, including
// ambiguous outcomes: a timeout alone never proves a prompt was not submitted.
func (n *Notifier) reconcileBridge(taskID string, d *delivery, now time.Time) bool {
	route := n.route(taskID)
	n.mu.Lock()
	if n.pending[taskID] != d {
		n.mu.Unlock()
		return true
	}
	c := d.bridgeClaim
	if c != nil {
		if now.Before(c.Expires) {
			n.mu.Unlock()
			return true
		}
		claim := *c
		n.mu.Unlock()
		evidence := BridgeAbsent // no authorization means no permitted submit
		if claim.Authorized {
			evidence = BridgeUnknown
			if n.bridgeEvidence != nil {
				evidence = n.bridgeEvidence(claim)
			}
		}
		if evidence == BridgeFound {
			n.mu.Lock()
			if n.pending[taskID] == d && d.bridgeClaim == c {
				n.removeAndAdvanceLocked(taskID, d.deliveryID, true)
			}
			n.mu.Unlock()
			logDelivery(slog.LevelInfo, "bridge recovered submission", taskID, d.deliveryID)
			return true
		}
		n.mu.Lock()
		if n.pending[taskID] != d || d.bridgeClaim != c {
			n.mu.Unlock()
			return true
		}
		if evidence == BridgeUnknown {
			first := !d.bridgeAmbiguous
			d.bridgeAmbiguous = true
			n.mu.Unlock()
			if first {
				logDelivery(slog.LevelWarn, "bridge ambiguous: automatic submission suspended", taskID, d.deliveryID)
			}
			return true
		}
		d.bridgeClaim = nil
		d.bridgeAmbiguous = false
		d.bridgeOffered = now
		if d.bridgeOffers >= 2 {
			d.bridgeFallback = true
		}
		logDelivery(slog.LevelInfo, "bridge claim expired without submission", taskID, d.deliveryID)
	}
	if d.bridgeFallback {
		n.mu.Unlock()
		return false
	}
	if !route.Live {
		n.mu.Unlock()
		return false
	}
	if !route.Idle || route.NeedsInput {
		d.bridgeOffered = time.Time{}
		n.mu.Unlock()
		return true
	}
	if d.bridgeOffered.IsZero() {
		d.bridgeOffered = now
	}
	if now.Sub(d.bridgeOffered) >= bridgeLease {
		d.bridgeFallback = true
		n.mu.Unlock()
		logDelivery(slog.LevelWarn, "bridge unavailable: using PTY fallback", taskID, d.deliveryID)
		return false
	}
	n.mu.Unlock()
	return true
}
