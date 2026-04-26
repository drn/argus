package modal

import (
	"testing"

	"github.com/gdamore/tcell/v2"
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
			name:        "tab then enter selects skip",
			keys:        []*tcell.EventKey{tcell.NewEventKey(tcell.KeyTab, 0, tcell.ModNone), tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone)},
			wantDone:    true,
			wantSkip:    true,
			wantSelect:  1,
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
