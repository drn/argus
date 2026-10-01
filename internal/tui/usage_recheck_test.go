package tui

import (
	"testing"
	"time"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/daemon"
	"github.com/drn/argus/internal/testutil"
)

// TestUsageRecheckInterval pins the cadence is far slower than the 1s tick —
// the daemon's own probe tickers refresh only every 30 minutes, so polling
// faster buys nothing (fix-backend-routing-semantics).
func TestUsageRecheckInterval(t *testing.T) {
	if usageRecheckInterval < 30*time.Second {
		t.Errorf("usageRecheckInterval = %v; a per-tick BootInfo RPC is not free", usageRecheckInterval)
	}
}

// TestClaimUsageCheck_Gates mirrors TestClaimSkewCheck_Gates: the periodic
// usage poll does not run on every 1s tick, and is inert without a provider
// (--remote mode, or the in-process-runner fallback).
func TestClaimUsageCheck_Gates(t *testing.T) {
	t.Run("no provider ⇒ never claims", func(t *testing.T) {
		app := New(testDB(t), agent.NewRunner(nil), true)
		app.skewProvider = nil
		app.lastUsageCheck = time.Time{}
		_, ok := app.claimUsageCheck()
		testutil.Equal(t, ok, false)
	})

	t.Run("within the interval ⇒ does not claim", func(t *testing.T) {
		app := New(testDB(t), agent.NewRunner(nil), true)
		app.skewProvider = &fakeSkewClient{}
		app.lastUsageCheck = time.Now()
		_, ok := app.claimUsageCheck()
		testutil.Equal(t, ok, false)
	})

	t.Run("past the interval ⇒ claims once, then re-gates", func(t *testing.T) {
		app := New(testDB(t), agent.NewRunner(nil), true)
		app.skewProvider = &fakeSkewClient{}
		app.lastUsageCheck = time.Now().Add(-2 * usageRecheckInterval)

		got, ok := app.claimUsageCheck()
		testutil.Equal(t, ok, true)
		if got == nil {
			t.Fatal("claim returned a nil provider")
		}
		_, ok = app.claimUsageCheck()
		testutil.Equal(t, ok, false)
	})
}

// TestReevaluateUsage_NoClientIsInert pins that the periodic re-poll is a
// no-op without a daemon client: there is no BootInfo to ask for, and it
// must not panic or block.
func TestReevaluateUsage_NoClientIsInert(t *testing.T) {
	app := New(testDB(t), agent.NewRunner(nil), true)
	app.skewProvider = nil
	app.reevaluateUsage()
}

// TestReevaluateUsage_FeedsStatusBar drives the full re-poll path against a
// running event loop: a fresh BootInfo reading must land on the status bar
// without a relaunch.
func TestReevaluateUsage_FeedsStatusBar(t *testing.T) {
	app := New(testDB(t), agent.NewRunner(nil), true)
	_, cleanup := wireApp(t, app)
	defer cleanup()

	app.skewProvider = &fakeSkewClient{resp: daemon.BootInfoResp{
		ClaudeUsagePct:   42,
		ClaudeUsageKnown: true,
		CodexUsagePct:    76,
		CodexUsageKnown:  true,
	}}
	app.lastUsageCheck = time.Now().Add(-2 * usageRecheckInterval)
	app.reevaluateUsage()

	testutil.Contains(t, app.statusbar.UsageSummary(), "cla 42% · cdx 76%")
}
