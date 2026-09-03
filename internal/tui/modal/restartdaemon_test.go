package modal

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/drn/argus/internal/testutil"
)

func TestRestartDaemonModal_DefaultsToRestart(t *testing.T) {
	m := NewSkewModal(true, false, "", "")
	if m.Selected() != 0 {
		t.Errorf("default selection = %d, want 0 (Restart)", m.Selected())
	}
	if m.Done() {
		t.Error("Done should be false before any input")
	}
}

func TestSkewModal_SupervisorButtonAndChoice(t *testing.T) {
	// Supervisor-only skew: buttons are [Restart supervisor, Skip] with the
	// restart action selected by default. Enter picks it.
	m := NewSkewModal(false, true, "", "svc-abc @ /path/argus")
	if m.Selected() != 0 {
		t.Fatalf("default selection = %d, want 0 (Restart supervisor)", m.Selected())
	}
	m.InputHandler()(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone), nil)
	if !m.ChoseRestartSupervisor() {
		t.Error("Enter on supervisor-only modal should choose ChoseRestartSupervisor")
	}
	if m.ChoseRestartDaemon() || m.ChoseSkip() {
		t.Error("only ChoseRestartSupervisor should be true")
	}
}

func TestSkewModal_SupervisorHasNoLetterShortcut(t *testing.T) {
	// The destructive supervisor restart must NOT be reachable by an accidental
	// letter press; 'r' only chooses daemon (absent here) so it is a no-op.
	m := NewSkewModal(false, true, "", "svc")
	m.InputHandler()(tcell.NewEventKey(tcell.KeyRune, 'r', tcell.ModNone), nil)
	if m.Done() {
		t.Error("'r' must not choose supervisor restart (no daemon button present)")
	}
}

func TestSkewModal_BothStaleThreeButtons(t *testing.T) {
	m := NewSkewModal(true, true, "dae-1 @ /a", "sup-2 @ /b")
	// Tab from daemon(0) → supervisor(1) → skip(2) → wrap to daemon(0).
	h := m.InputHandler()
	h(tcell.NewEventKey(tcell.KeyTab, 0, tcell.ModNone), nil)
	testutil.Equal(t, m.Selected(), 1)
	h(tcell.NewEventKey(tcell.KeyTab, 0, tcell.ModNone), nil)
	testutil.Equal(t, m.Selected(), 2)
	h(tcell.NewEventKey(tcell.KeyTab, 0, tcell.ModNone), nil)
	testutil.Equal(t, m.Selected(), 0)
	// Selecting supervisor (index 1) and pressing Enter chooses supervisor.
	h(tcell.NewEventKey(tcell.KeyTab, 0, tcell.ModNone), nil) // → 1
	h(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone), nil)
	if !m.ChoseRestartSupervisor() {
		t.Error("Enter on supervisor button should choose supervisor restart")
	}
}

func TestSkewModal_ResolveRestartDaemon_LeavesSupervisorButton(t *testing.T) {
	m := NewSkewModal(true, true, "dae-1 @ /a", "sup-2 @ /b")
	remaining := m.ResolveRestartDaemon()
	if !remaining {
		t.Fatal("ResolveRestartDaemon should report a restart action (supervisor) remains")
	}
	if len(m.buttons) != 2 {
		t.Fatalf("buttons = %d, want 2 (Restart supervisor, Skip)", len(m.buttons))
	}
	if m.buttons[0].choice != choiceRestartSupervisor || m.buttons[1].choice != choiceSkip {
		t.Errorf("buttons = %+v, want [Restart supervisor, Skip]", m.buttons)
	}
	if m.Done() {
		t.Error("modal should be re-armed (not done) after resolving one action")
	}
	if m.Selected() != 0 {
		t.Errorf("selection should reset to 0, got %d", m.Selected())
	}
	if m.daemonStale {
		t.Error("daemonStale should clear once resolved")
	}
	if !m.supervisorStale {
		t.Error("supervisorStale should remain set")
	}

	// The re-armed modal accepts a fresh choice for the remaining action.
	m.InputHandler()(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone), nil)
	if !m.ChoseRestartSupervisor() {
		t.Error("Enter after resolving daemon should choose the remaining supervisor button")
	}
}

func TestSkewModal_ResolveRestartDaemon_DaemonOnlyHasNoRemainingAction(t *testing.T) {
	m := NewSkewModal(true, false, "dae-1 @ /a", "")
	remaining := m.ResolveRestartDaemon()
	if remaining {
		t.Error("ResolveRestartDaemon should report no restart action remains when supervisor was never stale")
	}
	if len(m.buttons) != 1 || m.buttons[0].choice != choiceSkip {
		t.Fatalf("buttons = %+v, want [Skip] only", m.buttons)
	}
}

func TestSkewModal_ResolveRestartSupervisor_ResolvesDaemonToo(t *testing.T) {
	// Restarting the supervisor also bounces the daemon, so resolving the
	// supervisor action clears any pending daemon restart too.
	m := NewSkewModal(true, true, "dae-1 @ /a", "sup-2 @ /b")
	m.ResolveRestartSupervisor()
	if len(m.buttons) != 1 || m.buttons[0].choice != choiceSkip {
		t.Fatalf("buttons = %+v, want [Skip] only", m.buttons)
	}
	if m.daemonStale || m.supervisorStale {
		t.Error("both staleness flags should clear")
	}
}

func TestSkewModal_DrawShowsRichIdentity(t *testing.T) {
	sim := drawAt(t, 80, 24)
	m := NewSkewModal(true, true, "a1b2c3 (dirty) @ /usr/bin/argus", "d4e5f6 @ /gopath/bin/argus")
	m.SetRect(0, 0, 80, 24)
	m.Draw(sim)
	sim.Sync()
	body := screenString(sim)
	testutil.Contains(t, body, "Binaries out of date")
	testutil.Contains(t, body, "a1b2c3 (dirty)")
	testutil.Contains(t, body, "d4e5f6")
	testutil.Contains(t, body, "Restart daemon")
	testutil.Contains(t, body, "Restart supervisor")
	testutil.Contains(t, body, "Skip")
}

func TestSkewModal_SupervisorOnlyTitle(t *testing.T) {
	sim := drawAt(t, 80, 24)
	m := NewSkewModal(false, true, "", "d4e5f6 @ /gopath/bin/argus")
	m.SetRect(0, 0, 80, 24)
	m.Draw(sim)
	sim.Sync()
	body := screenString(sim)
	testutil.Contains(t, body, "Supervisor out of date")
	testutil.Contains(t, body, "d4e5f6")
}

func TestRestartDaemonModal_KeyHandling(t *testing.T) {
	tests := []struct {
		name        string
		keys        []*tcell.EventKey
		wantDone    bool
		wantRestart bool
		wantSkip    bool
		wantSelect  int
	}{
		{
			name:        "enter on default selects restart",
			keys:        []*tcell.EventKey{tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone)},
			wantDone:    true,
			wantRestart: true,
		},
		{
			name:       "tab moves selection to skip",
			keys:       []*tcell.EventKey{tcell.NewEventKey(tcell.KeyTab, 0, tcell.ModNone)},
			wantSelect: 1,
		},
		{
			name:       "tab then enter selects skip",
			keys:       []*tcell.EventKey{tcell.NewEventKey(tcell.KeyTab, 0, tcell.ModNone), tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone)},
			wantDone:   true,
			wantSkip:   true,
			wantSelect: 1,
		},
		{
			name:     "esc selects skip",
			keys:     []*tcell.EventKey{tcell.NewEventKey(tcell.KeyEscape, 0, tcell.ModNone)},
			wantDone: true,
			wantSkip: true,
		},
		{
			name:        "r shortcut chooses restart",
			keys:        []*tcell.EventKey{tcell.NewEventKey(tcell.KeyRune, 'r', tcell.ModNone)},
			wantDone:    true,
			wantRestart: true,
		},
		{
			name:     "s shortcut chooses skip",
			keys:     []*tcell.EventKey{tcell.NewEventKey(tcell.KeyRune, 's', tcell.ModNone)},
			wantDone: true,
			wantSkip: true,
		},
		{
			name:       "right arrow moves to skip",
			keys:       []*tcell.EventKey{tcell.NewEventKey(tcell.KeyRight, 0, tcell.ModNone)},
			wantSelect: 1,
		},
		{
			name:       "right then left returns to restart",
			keys:       []*tcell.EventKey{tcell.NewEventKey(tcell.KeyRight, 0, tcell.ModNone), tcell.NewEventKey(tcell.KeyLeft, 0, tcell.ModNone)},
			wantSelect: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := NewSkewModal(true, false, "", "")
			handler := m.InputHandler()
			for _, k := range tt.keys {
				handler(k, nil)
			}
			if got := m.Done(); got != tt.wantDone {
				t.Errorf("Done() = %v, want %v", got, tt.wantDone)
			}
			if got := m.ChoseRestartDaemon(); got != tt.wantRestart {
				t.Errorf("ChoseRestartDaemon() = %v, want %v", got, tt.wantRestart)
			}
			if got := m.ChoseSkip(); got != tt.wantSkip {
				t.Errorf("ChoseSkip() = %v, want %v", got, tt.wantSkip)
			}
			if !tt.wantDone {
				if got := m.Selected(); got != tt.wantSelect {
					t.Errorf("Selected() = %d, want %d", got, tt.wantSelect)
				}
			}
		})
	}
}

// TestSkewModal_SupervisorConsequence pins that the modal names what a supervisor
// restart would actually buy. The dangerous case is a modal opened because the
// DAEMON is stale: it still offers "Restart supervisor", which SIGHUPs every
// running agent, so the operator must be able to see whether the supervisor's own
// mismatch is worth that.
func TestSkewModal_SupervisorConsequence(t *testing.T) {
	t.Run("rendered when set", func(t *testing.T) {
		m := NewSkewModal(true, true, "dae @ /a", "sup @ /b")
		m.SetSupervisorConsequence("spawn config only — running agents are unaffected")
		joined := strings.Join(m.bodyLines(), "\n")
		testutil.Contains(t, joined, "supervisor: sup @ /b")
		testutil.Contains(t, joined, "running agents are unaffected")
	})

	t.Run("omitted when unset", func(t *testing.T) {
		m := NewSkewModal(false, true, "", "sup @ /b")
		for _, line := range m.bodyLines() {
			if strings.HasPrefix(line, "  (") {
				t.Errorf("unset consequence still rendered a line: %q", line)
			}
		}
	})

	t.Run("never rendered for a daemon-only skew", func(t *testing.T) {
		m := NewSkewModal(true, false, "dae @ /a", "")
		m.SetSupervisorConsequence("live sessions are affected")
		joined := strings.Join(m.bodyLines(), "\n")
		if strings.Contains(joined, "live sessions are affected") {
			t.Error("a daemon-only skew must not render a supervisor consequence")
		}
	})
}
