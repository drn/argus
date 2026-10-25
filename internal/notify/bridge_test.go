package notify

import (
	"testing"
	"time"

	"github.com/drn/argus/internal/testutil"
)

func TestBridgeDeliveryClaim(t *testing.T) {
	r := newFakeRunner()
	sess := r.addSession("t", false)
	n := New(r, NewFocusTracker(nil))
	n.SetBridgeRoute(func(string) BridgeRoute {
		return BridgeRoute{Live: true, Idle: true, Generation: "g", Boot: "b", Session: "s"}
	}, nil)
	n.ReliableNotify("t", "notice", "hera:1", NotifyOpts{})
	now := time.Now()
	n.Reconcile(now)
	testutil.Equal(t, len(sess.writes), 0)
	claim := n.ClaimBridge("t", "g", "b", now)
	testutil.NotNil(t, claim)
	testutil.Contains(t, claim.Text, "hera:1")
	testutil.True(t, n.AuthorizeBridge("t", claim.Token, "g", "b", now))
	testutil.False(t, n.AuthorizeBridge("t", claim.Token, "old", "b", now))
	testutil.True(t, n.AckBridge("t", claim.Token, now))
	testutil.Equal(t, n.DeliveryState("t", "hera:1"), StateSubmitted)
	testutil.Nil(t, n.ClaimBridge("t", "g", "b", now))
}

func TestBridgeCancelAndGates(t *testing.T) {
	for _, gate := range []string{"busy", "dialog", "cancel", "deadline", "old-generation"} {
		t.Run(gate, func(t *testing.T) {
			n := New(newFakeRunner(), NewFocusTracker(nil))
			route := BridgeRoute{Live: true, Idle: gate != "busy", NeedsInput: gate == "dialog", Generation: "g", Boot: "b"}
			n.SetBridgeRoute(func(string) BridgeRoute { return route }, nil)
			n.ReliableNotify("t", "notice", "1", NotifyOpts{})
			now := time.Now()
			if gate == "deadline" {
				now = now.Add(6 * time.Minute)
			}
			if gate == "cancel" {
				n.Cancel("t", "1")
			}
			gen := "g"
			if gate == "old-generation" {
				gen = "old"
			}
			testutil.Nil(t, n.ClaimBridge("t", gen, "b", now))
		})
	}
}

func TestBridgeRecovery(t *testing.T) {
	for _, evidence := range []BridgeEvidence{BridgeFound, BridgeAbsent, BridgeUnknown} {
		t.Run(string(evidence), func(t *testing.T) {
			n := New(newFakeRunner(), NewFocusTracker(nil))
			n.SetBridgeRoute(func(string) BridgeRoute { return BridgeRoute{Live: true, Idle: true, Generation: "g", Boot: "b"} }, func(BridgeClaim) BridgeEvidence { return evidence })
			n.ReliableNotify("t", "notice", "1", NotifyOpts{})
			now := time.Now()
			claim := n.ClaimBridge("t", "g", "b", now)
			testutil.NotNil(t, claim)
			testutil.True(t, n.AuthorizeBridge("t", claim.Token, "g", "b", now))
			n.Reconcile(now.Add(bridgeLease + time.Second))
			switch evidence {
			case BridgeFound:
				testutil.Equal(t, n.DeliveryState("t", "1"), StateSubmitted)
			case BridgeAbsent:
				testutil.NotNil(t, n.ClaimBridge("t", "g", "b", now.Add(bridgeLease+time.Second)))
			default:
				testutil.Nil(t, n.ClaimBridge("t", "g", "b", now.Add(bridgeLease+time.Second)))
				testutil.Equal(t, n.DeliveryState("t", "1"), StatePending)
			}
		})
	}
}

func TestBridgeReofferThenPTY(t *testing.T) {
	r := newFakeRunner()
	sess := r.addSession("t", true)
	n := New(r, NewFocusTracker(nil))
	n.waitForSettled = func(SessionHandleIface, uint64, time.Duration, time.Duration) bool { return true }
	n.waitForAdvance = func(SessionHandleIface, uint64, time.Duration) bool { return true }
	n.SetBridgeRoute(func(string) BridgeRoute { return BridgeRoute{Live: true, Idle: true, Generation: "g", Boot: "b"} }, func(BridgeClaim) BridgeEvidence { return BridgeAbsent })
	n.ReliableNotify("t", "notice", "1", NotifyOpts{})
	now := time.Now()
	first := n.ClaimBridge("t", "g", "b", now)
	testutil.NotNil(t, first)
	now = now.Add(bridgeLease + time.Second)
	n.Reconcile(now)
	second := n.ClaimBridge("t", "g", "b", now)
	testutil.NotNil(t, second)
	testutil.False(t, n.AuthorizeBridge("t", first.Token, "g", "b", now))
	now = now.Add(bridgeLease + time.Second)
	n.Reconcile(now)
	testutil.Equal(t, n.DeliveryState("t", "1"), StateSubmitted)
	testutil.Equal(t, len(sess.writes), 3)
	testutil.Equal(t, string(sess.writes[1]), "notice")
	testutil.False(t, n.AuthorizeBridge("t", second.Token, "g", "b", now))
}

func TestBridgeBusyNeverAgesIntoPTY(t *testing.T) {
	r := newFakeRunner()
	sess := r.addSession("t", true)
	n := New(r, NewFocusTracker(nil))
	n.SetBridgeRoute(func(string) BridgeRoute { return BridgeRoute{Live: true, Idle: false} }, nil)
	n.ReliableNotify("t", "notice", "1", NotifyOpts{})
	n.Reconcile(time.Now())
	n.Reconcile(time.Now().Add(time.Minute))
	testutil.Equal(t, len(sess.writes), 0)
}
