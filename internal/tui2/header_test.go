package tui2

import "testing"

func TestHeader_SetTab(t *testing.T) {
	h := NewHeader()

	if h.ActiveTab() != TabTasks {
		t.Errorf("initial tab = %v, want TabTasks", h.ActiveTab())
	}

	h.SetTab(TabReviews)
	if h.ActiveTab() != TabReviews {
		t.Errorf("tab = %v, want TabReviews", h.ActiveTab())
	}

	h.SetTab(TabSettings)
	if h.ActiveTab() != TabSettings {
		t.Errorf("tab = %v, want TabSettings", h.ActiveTab())
	}
}

func TestTabLabels(t *testing.T) {
	if len(tabLabels) != 4 {
		t.Errorf("tabLabels count = %d, want 4", len(tabLabels))
	}
	if len(tabKeys) != 4 {
		t.Errorf("tabKeys count = %d, want 4", len(tabKeys))
	}
}
