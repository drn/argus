package ui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestSandboxConfigForm_NewPrePopulated(t *testing.T) {
	domains := []string{"github.com", "npmjs.org"}
	deny := []string{"/secrets"}
	extra := []string{"~/.npm"}

	f := NewSandboxConfigForm(DefaultTheme(), true, domains, deny, extra)

	if !f.enabled {
		t.Error("expected enabled=true")
	}
	if f.inputs[sbFieldDomains].Value() != "github.com,npmjs.org" {
		t.Errorf("unexpected domains: %q", f.inputs[sbFieldDomains].Value())
	}
	if f.inputs[sbFieldDenyRead].Value() != "/secrets" {
		t.Errorf("unexpected deny read: %q", f.inputs[sbFieldDenyRead].Value())
	}
	if f.inputs[sbFieldExtraWrite].Value() != "~/.npm" {
		t.Errorf("unexpected extra write: %q", f.inputs[sbFieldExtraWrite].Value())
	}
}

func TestSandboxConfigForm_Toggle(t *testing.T) {
	f := NewSandboxConfigForm(DefaultTheme(), false, nil, nil, nil)

	if f.enabled {
		t.Error("expected disabled initially")
	}

	// ctrl+e toggles
	f.Update(tea.KeyMsg{Type: tea.KeyCtrlE})
	if !f.enabled {
		t.Error("expected enabled after ctrl+e")
	}

	f.Update(tea.KeyMsg{Type: tea.KeyCtrlE})
	if f.enabled {
		t.Error("expected disabled after second ctrl+e")
	}
}

func TestSandboxConfigForm_Cancel(t *testing.T) {
	f := NewSandboxConfigForm(DefaultTheme(), true, nil, nil, nil)
	f.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if !f.Canceled() {
		t.Error("expected canceled after esc")
	}
}

func TestSandboxConfigForm_SubmitOnLastField(t *testing.T) {
	f := NewSandboxConfigForm(DefaultTheme(), true, []string{"github.com"}, nil, nil)
	f.SetSize(120, 40)

	// Navigate to last field
	f.focused = sbFieldExtraWrite

	// Enter on last field submits
	f.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !f.Done() {
		t.Error("expected done after enter on last field")
	}

	enabled, domains, denyRead, extraWrite := f.Result()
	if !enabled {
		t.Error("expected enabled=true in result")
	}
	if domains != "github.com" {
		t.Errorf("unexpected domains: %q", domains)
	}
	if denyRead != "" {
		t.Errorf("unexpected deny read: %q", denyRead)
	}
	if extraWrite != "" {
		t.Errorf("unexpected extra write: %q", extraWrite)
	}
}

func TestSandboxConfigForm_TabNavigation(t *testing.T) {
	f := NewSandboxConfigForm(DefaultTheme(), false, nil, nil, nil)
	f.SetSize(120, 40)

	if f.focused != sbFieldDomains {
		t.Errorf("expected initial focus on domains, got %d", f.focused)
	}

	// Tab moves forward
	f.Update(tea.KeyMsg{Type: tea.KeyTab})
	if f.focused != sbFieldDenyRead {
		t.Errorf("expected focus on deny read after tab, got %d", f.focused)
	}

	// Shift+tab moves back
	f.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	if f.focused != sbFieldDomains {
		t.Errorf("expected focus back on domains, got %d", f.focused)
	}
}

func TestSandboxConfigForm_View(t *testing.T) {
	f := NewSandboxConfigForm(DefaultTheme(), true, []string{"example.com"}, nil, nil)
	f.SetSize(120, 40)

	v := f.View()
	if v == "" {
		t.Error("expected non-empty view")
	}
}

func TestSandboxConfigForm_NilInputs(t *testing.T) {
	// Zero-valued form should not panic
	var f SandboxConfigForm
	v := f.View()
	if v != "" {
		t.Error("expected empty view for zero-valued form")
	}
}
