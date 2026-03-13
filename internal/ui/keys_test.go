package ui

import "testing"

func TestDefaultKeyMap(t *testing.T) {
	km := DefaultKeyMap()

	checks := map[string]string{
		"New":       km.New.Keys()[0],
		"Quit":      km.Quit.Keys()[0],
		"Help":      km.Help.Keys()[0],
		"Filter":    km.Filter.Keys()[0],
		"Confirm":   km.Confirm.Keys()[0],
		"Cancel":    km.Cancel.Keys()[0],
	}

	expected := map[string]string{
		"New":     "n",
		"Quit":    "q",
		"Help":    "?",
		"Filter":  "/",
		"Confirm": "y",
		"Cancel":  "esc",
	}

	for name, got := range checks {
		if got != expected[name] {
			t.Errorf("%s key = %q, want %q", name, got, expected[name])
		}
	}
}

func TestShortHelp(t *testing.T) {
	km := DefaultKeyMap()
	bindings := km.ShortHelp()
	if len(bindings) == 0 {
		t.Error("ShortHelp should return bindings")
	}
}

func TestFullHelp(t *testing.T) {
	km := DefaultKeyMap()
	groups := km.FullHelp()
	if len(groups) == 0 {
		t.Error("FullHelp should return groups")
	}
}
