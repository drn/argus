package tui

import (
	"testing"
	"time"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/daemon"
	"github.com/drn/argus/internal/skew"
	"github.com/drn/argus/internal/testutil"
)

// TestSetSkew_StoresTier pins that the tiered verdict survives SetSkew, not just
// the derived boolean — the tier is what decides modal-vs-notice.
func TestSetSkew_StoresTier(t *testing.T) {
	tests := []struct {
		name      string
		res       skew.Result
		wantStale bool
	}{
		{"coherent", skew.Result{Supervisor: daemon.SurfaceCoherent}, false},
		{"unknown (pre-v6) is not stale", skew.Result{Supervisor: daemon.SurfaceUnknown}, false},
		{"spawn-stale", skew.Result{Supervisor: daemon.SurfaceSpawnStale}, true},
		{"stream-stale", skew.Result{Supervisor: daemon.SurfaceStreamStale}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app := New(testDB(t), agent.NewRunner(nil), true)
			app.SetSkew(tt.res)
			testutil.Equal(t, app.supervisorStale, tt.wantStale)
			testutil.Equal(t, app.startupSkew.Supervisor, tt.res.Supervisor)
		})
	}
}

// TestSmoke_StartupSkew_SpawnOnlyTakesNoticeNotModal is the operator-facing point
// of the two-component split (design D6). A spawn-surface mismatch cannot affect
// a single running agent, so blocking the operator with a modal on it is the same
// false alarm the whole-binary hash used to produce. It must surface as a
// transient status-bar notice and leave the app on the task list.
func TestSmoke_StartupSkew_SpawnOnlyTakesNoticeNotModal(t *testing.T) {
	app := New(testDB(t), agent.NewRunner(nil), true)
	app.SetSkew(skew.Result{
		Supervisor:         daemon.SurfaceSpawnStale,
		SupervisorIdentity: "sup-abc @ /gopath/bin/argus",
	})
	_, cleanup := wireApp(t, app)
	defer cleanup()
	readUI(t, app.tapp, func() { app.applyStartupSkew() })

	if app.restartDaemonModal != nil {
		t.Fatal("a spawn-only supervisor mismatch must NOT open the blocking modal")
	}
	testutil.Equal(t, app.mode, modeTaskList)
	testutil.Contains(t, app.statusbar.Info(), "Supervisor is running an older build")
	testutil.Contains(t, app.statusbar.Info(), "running agents are unaffected")
}

// TestSmoke_StartupSkew_StreamStillBlocks is the counterpart: a stream mismatch
// genuinely affects live sessions, so the blocking startup modal stays exactly
// where it already lived.
func TestSmoke_StartupSkew_StreamStillBlocks(t *testing.T) {
	app := New(testDB(t), agent.NewRunner(nil), true)
	app.SetSkew(skew.Result{
		Supervisor:         daemon.SurfaceStreamStale,
		SupervisorIdentity: "sup-abc @ /gopath/bin/argus",
	})
	_, cleanup := wireApp(t, app)
	defer cleanup()
	readUI(t, app.tapp, func() { app.applyStartupSkew() })

	if app.restartDaemonModal == nil {
		t.Fatal("a stream-surface mismatch must still open the blocking modal")
	}
	testutil.Equal(t, app.mode, modeRestartDaemonPrompt)
}

// TestSmoke_StartupSkew_CoherentIsSilent pins the ~9-in-10 case end to end: a
// coherent supervisor produces neither a modal nor a notice. This is the nagging
// that trained the operator to distrust the signal.
func TestSmoke_StartupSkew_CoherentIsSilent(t *testing.T) {
	app := New(testDB(t), agent.NewRunner(nil), true)
	app.SetSkew(skew.Result{Supervisor: daemon.SurfaceCoherent})
	_, cleanup := wireApp(t, app)
	defer cleanup()
	readUI(t, app.tapp, func() { app.applyStartupSkew() })

	if app.restartDaemonModal != nil {
		t.Fatal("a coherent install must not open the skew modal")
	}
	testutil.Equal(t, app.statusbar.Info(), "")
}

// fakeSkewClient stands in for the daemon client during re-evaluation. It is NOT
// a *dclient.Client, so reevaluateSkew's nil check is what these tests drive —
// see TestReevaluateSkew_NoClientIsInert.
type fakeSkewClient struct {
	calls int
	resp  daemon.BootInfoResp
}

func (f *fakeSkewClient) BootInfo() (daemon.BootInfoResp, error) {
	f.calls++
	return f.resp, nil
}

// TestReevaluateSkew_NoClientIsInert pins that the periodic re-check is a no-op
// without a daemon client (--remote mode, or the in-process-runner fallback):
// there is no BootInfo to ask for, and it must not panic or block.
func TestReevaluateSkew_NoClientIsInert(t *testing.T) {
	app := New(testDB(t), agent.NewRunner(nil), true)
	app.daemonClient = nil
	app.reevaluateSkew()
	testutil.Equal(t, app.lastSkewNotice, "")
}

// TestSkewRecheckInterval pins the cadence is far slower than the 1s tick.
func TestSkewRecheckInterval(t *testing.T) {
	if skewRecheckInterval < 30*time.Second {
		t.Errorf("skewRecheckInterval = %v; a per-tick BootInfo RPC + binary hash is not free", skewRecheckInterval)
	}
}

// TestClaimSkewCheck_Gates pins that the re-check does NOT run on every 1s tick,
// and is inert without a provider. Each evaluation costs a BootInfo RPC plus a
// SHA-256 of the TUI's own binary, and what it watches for (someone running `go
// install` in another terminal) happens on human timescales.
func TestClaimSkewCheck_Gates(t *testing.T) {
	t.Run("no provider ⇒ never claims", func(t *testing.T) {
		app := New(testDB(t), agent.NewRunner(nil), true)
		app.skewProvider = nil
		app.lastSkewCheck = time.Time{}
		_, ok := app.claimSkewCheck()
		testutil.Equal(t, ok, false)
	})

	t.Run("within the interval ⇒ does not claim", func(t *testing.T) {
		app := New(testDB(t), agent.NewRunner(nil), true)
		app.skewProvider = &fakeSkewClient{}
		app.lastSkewCheck = time.Now()
		_, ok := app.claimSkewCheck()
		testutil.Equal(t, ok, false)
	})

	t.Run("past the interval ⇒ claims once, then re-gates", func(t *testing.T) {
		app := New(testDB(t), agent.NewRunner(nil), true)
		app.skewProvider = &fakeSkewClient{}
		app.lastSkewCheck = time.Now().Add(-2 * skewRecheckInterval)

		got, ok := app.claimSkewCheck()
		testutil.Equal(t, ok, true)
		if got == nil {
			t.Fatal("claim returned a nil provider")
		}
		// Stamped on the way out, so a slow BootInfo cannot pile up overlapping
		// evaluations behind it.
		_, ok = app.claimSkewCheck()
		testutil.Equal(t, ok, false)
	})
}

// TestNoticeForSkew_DedupesPerVerdict pins the anti-nag rule. A standing skew
// must not re-raise every minute — that is exactly how the whole-binary hash
// trained the operator to ignore the signal — but a CHANGED verdict must speak
// up again, and a cleared one must reset so a later recurrence is announced.
func TestNoticeForSkew_DedupesPerVerdict(t *testing.T) {
	app := New(testDB(t), agent.NewRunner(nil), true)

	spawn := skew.Result{Supervisor: daemon.SurfaceSpawnStale}
	stream := skew.Result{Supervisor: daemon.SurfaceStreamStale}
	clean := skew.Result{Supervisor: daemon.SurfaceCoherent}

	first := app.noticeForSkew(spawn)
	testutil.Contains(t, first, "running agents are unaffected")

	if repeat := app.noticeForSkew(spawn); repeat != "" {
		t.Errorf("the same standing verdict re-nagged: %q", repeat)
	}

	escalated := app.noticeForSkew(stream)
	testutil.Contains(t, escalated, "live sessions are affected")

	// Clearing is silent (nothing to say) but resets the memory...
	testutil.Equal(t, app.noticeForSkew(clean), "")
	// ...so a recurrence is announced afresh rather than swallowed.
	testutil.Contains(t, app.noticeForSkew(stream), "live sessions are affected")
}

// TestReevaluateSkew_RaisesNoticeOnLiveApp drives the whole re-evaluation path
// against a running event loop: a skew that appears AFTER startup must reach the
// status bar without a relaunch, and must never open a modal (design D6 — firing
// one mid-session because a build landed in another terminal would interrupt
// exactly the work this change protects).
func TestReevaluateSkew_RaisesNoticeOnLiveApp(t *testing.T) {
	app := New(testDB(t), agent.NewRunner(nil), true)
	app.SetSkew(skew.Result{Supervisor: daemon.SurfaceCoherent}) // coherent at startup
	_, cleanup := wireApp(t, app)
	defer cleanup()

	testutil.Equal(t, app.statusbar.Info(), "")
	if app.restartDaemonModal != nil {
		t.Fatal("precondition: no modal should be open")
	}

	// A new build lands: the supervisor now reports an older stream surface.
	cur := daemon.CurrentSupervisorSurface()
	app.skewProvider = &fakeSkewClient{resp: daemon.BootInfoResp{
		SupervisorPresent:       true,
		SupervisorHash:          "cafebabe",
		SupervisorSpawnSurface:  cur.Spawn,
		SupervisorStreamSurface: cur.Stream + 1,
	}}
	app.lastSkewCheck = time.Now().Add(-2 * skewRecheckInterval)
	app.reevaluateSkew()

	testutil.Contains(t, app.statusbar.Info(), "live sessions are affected")
	if app.restartDaemonModal != nil {
		t.Error("a post-startup skew must never present a blocking modal")
	}
	testutil.Equal(t, app.mode, modeTaskList)
}

// TestReevaluateSkew_CoherentSupervisorStaysSilent is the re-evaluation half of
// the ~9-in-10 case: a differing binary hash with a matching surface must not
// raise anything at all.
func TestReevaluateSkew_CoherentSupervisorStaysSilent(t *testing.T) {
	app := New(testDB(t), agent.NewRunner(nil), true)
	_, cleanup := wireApp(t, app)
	defer cleanup()

	cur := daemon.CurrentSupervisorSurface()
	provider := &fakeSkewClient{resp: daemon.BootInfoResp{
		SupervisorPresent:       true,
		SupervisorHash:          "a-different-binary-entirely",
		SupervisorSpawnSurface:  cur.Spawn,
		SupervisorStreamSurface: cur.Stream,
	}}
	app.skewProvider = provider
	app.lastSkewCheck = time.Now().Add(-2 * skewRecheckInterval)
	app.reevaluateSkew()

	testutil.Equal(t, provider.calls, 1)
	testutil.Equal(t, app.statusbar.Info(), "")
}
