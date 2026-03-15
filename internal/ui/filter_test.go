package ui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestSuperToAltFilter_CmdArrows(t *testing.T) {
	// Simulate unknownCSISequenceMsg ([]byte subtype) via reflect-compatible type.
	// In practice bubbletea sends these as its unexported unknownCSISequenceMsg,
	// which has underlying type []byte. We use a local alias to test the same
	// reflect path.
	type csiMsg []byte

	tests := []struct {
		name    string
		input   csiMsg
		wantKey tea.KeyType
		wantAlt bool
	}{
		{"Cmd+Up", csiMsg("\x1b[1;9A"), tea.KeyUp, true},
		{"Cmd+Down", csiMsg("\x1b[1;9B"), tea.KeyDown, true},
		{"Cmd+Right", csiMsg("\x1b[1;9C"), tea.KeyRight, true},
		{"Cmd+Left", csiMsg("\x1b[1;9D"), tea.KeyLeft, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := SuperToAltFilter(nil, tt.input)
			km, ok := result.(tea.KeyMsg)
			if !ok {
				t.Fatalf("expected tea.KeyMsg, got %T", result)
			}
			if km.Type != tt.wantKey {
				t.Errorf("Type = %v, want %v", km.Type, tt.wantKey)
			}
			if km.Alt != tt.wantAlt {
				t.Errorf("Alt = %v, want %v", km.Alt, tt.wantAlt)
			}
		})
	}
}

func TestSuperToAltFilter_PassesThrough(t *testing.T) {
	type csiMsg []byte

	// KeyMsg is not comparable (contains Runes slice), so test it separately.
	t.Run("normal KeyMsg", func(t *testing.T) {
		input := tea.KeyMsg{Type: tea.KeyLeft}
		result := SuperToAltFilter(nil, input)
		if _, ok := result.(tea.KeyMsg); !ok {
			t.Fatalf("expected tea.KeyMsg passthrough, got %T", result)
		}
		km := result.(tea.KeyMsg)
		if km.Type != tea.KeyLeft || km.Alt {
			t.Errorf("KeyMsg was modified: %+v", km)
		}
	})

	// These are all comparable types.
	t.Run("wrong length", func(t *testing.T) {
		input := csiMsg("\x1b[1;9")
		result := SuperToAltFilter(nil, input)
		if _, ok := result.(tea.KeyMsg); ok {
			t.Error("short sequence should not be converted")
		}
	})
	t.Run("wrong modifier", func(t *testing.T) {
		input := csiMsg("\x1b[1;3C")
		result := SuperToAltFilter(nil, input)
		if _, ok := result.(tea.KeyMsg); ok {
			t.Error("modifier 3 should not be converted")
		}
	})
	t.Run("WindowSizeMsg", func(t *testing.T) {
		input := tea.WindowSizeMsg{Width: 80, Height: 24}
		result := SuperToAltFilter(nil, input)
		if result != input {
			t.Errorf("expected passthrough, got %v", result)
		}
	})
}
