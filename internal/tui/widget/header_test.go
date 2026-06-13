package widget

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

	h.SetTab(TabSettings)
	if h.ActiveTab() != TabSettings {
		t.Errorf("tab = %v, want TabSettings", h.ActiveTab())
	}
}

func TestTabLabels(t *testing.T) {
	if len(TabLabels) != 3 {
		t.Errorf("TabLabels count = %d, want 3", len(TabLabels))
	}
	if len(tabKeys) != 3 {
		t.Errorf("tabKeys count = %d, want 3", len(tabKeys))
	}
	if TabLabels[TabHera] != "Hera" {
		t.Errorf("TabLabels[TabHera] = %q, want Hera", TabLabels[TabHera])
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

func TestHeader_SetTabLabel_DefaultsFromTabLabels(t *testing.T) {
	h := NewHeader()
	for i, want := range TabLabels {
		if got := h.TabLabel(Tab(i)); got != want {
			t.Errorf("TabLabel(%d) = %q, want %q", i, got, want)
		}
	}
}

func TestHeader_SetTabLabel_Override(t *testing.T) {
	h := NewHeader()
	h.SetTabLabel(TabHera, "DAG")
	if got := h.TabLabel(TabHera); got != "DAG" {
		t.Errorf("TabLabel(TabHera) after override = %q, want DAG", got)
	}
	// Other tabs must be unaffected.
	if got := h.TabLabel(TabTasks); got != "Tasks" {
		t.Errorf("TabLabel(TabTasks) = %q, want Tasks", got)
	}
}

func TestHeader_SetTabLabel_RenderedInDraw(t *testing.T) {
	screen := tcell.NewSimulationScreen("UTF-8")
	screen.Init() //nolint:errcheck
	screen.SetSize(80, 1)

	h := NewHeader()
	h.SetTabLabel(TabHera, "DAG")
	h.SetRect(0, 0, 80, 1)
	h.Draw(screen)

	all := readAllScreenText(screen, 80, 1)
	if !strings.Contains(all, "DAG") {
		t.Errorf("overridden label DAG not found in rendered output: %q", all)
	}
	if strings.Contains(all, "Hera") {
		t.Errorf("original label Hera should not appear after override: %q", all)
	}
}

func TestHeader_SetTabLabel_OutOfBounds(t *testing.T) {
	h := NewHeader()
	// Should not panic on invalid tab.
	h.SetTabLabel(Tab(99), "Nope")
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
