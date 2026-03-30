package tui2

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
)

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

func TestHeader_Notice(t *testing.T) {
	h := NewHeader()

	// Initially no notice.
	if h.Notice() != "" {
		t.Errorf("initial notice = %q, want empty", h.Notice())
	}

	// Set a notice.
	h.SetNotice("Cleaning worktrees (0/3)")
	if h.Notice() != "Cleaning worktrees (0/3)" {
		t.Errorf("notice = %q, want %q", h.Notice(), "Cleaning worktrees (0/3)")
	}

	// Clear the notice.
	h.ClearNotice()
	if h.Notice() != "" {
		t.Errorf("after clear, notice = %q, want empty", h.Notice())
	}
}

func TestHeader_DrawWithNotice(t *testing.T) {
	screen := tcell.NewSimulationScreen("UTF-8")
	screen.Init() //nolint:errcheck
	screen.SetSize(80, 1)

	h := NewHeader()
	h.SetRect(0, 0, 80, 1)
	h.SetNotice("Cleaning worktrees (1/5)")
	h.Draw(screen)

	// Read the screen content and verify notice text appears.
	all := readAllScreenText(screen, 80, 1)
	if !strings.Contains(all, "Cleaning worktrees (1/5)") {
		t.Errorf("notice text not found in screen output: %q", all)
	}
	// Tab labels should still be visible alongside notice.
	if !strings.Contains(all, "Tasks") {
		t.Errorf("tab labels missing when notice is active: %q", all)
	}
}

func TestHeader_DrawWithoutNotice(t *testing.T) {
	screen := tcell.NewSimulationScreen("UTF-8")
	screen.Init() //nolint:errcheck
	screen.SetSize(80, 1)

	h := NewHeader()
	h.SetRect(0, 0, 80, 1)
	h.Draw(screen)

	// Tab labels should still appear.
	all := readAllScreenText(screen, 80, 1)
	if !strings.Contains(all, "Tasks") {
		t.Errorf("tab labels not found in screen output: %q", all)
	}
}

// readAllScreenText reads all text from the simulation screen.
func readAllScreenText(screen tcell.SimulationScreen, width, height int) string {
	var lines []string
	for row := range height {
		var runes []rune
		for col := range width {
			r, _, _, _ := screen.GetContent(col, row)
			runes = append(runes, r)
		}
		lines = append(lines, string(runes))
	}
	return strings.Join(lines, "\n")
}
