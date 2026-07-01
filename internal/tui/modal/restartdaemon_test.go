package modal

import (
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/drn/argus/internal/testutil"
)

func TestRestartDaemonModal_DefaultsToRestart(t *testing.T) {
	m := NewRestartDaemonModal()
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
			m := NewRestartDaemonModal()
			handler := m.InputHandler()
			for _, k := range tt.keys {
				handler(k, nil)
			}
			if got := m.Done(); got != tt.wantDone {
				t.Errorf("Done() = %v, want %v", got, tt.wantDone)
			}
			if got := m.ChoseRestart(); got != tt.wantRestart {
				t.Errorf("ChoseRestart() = %v, want %v", got, tt.wantRestart)
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
