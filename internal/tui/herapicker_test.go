package tui

import (
	"testing"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/testutil"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

func orchs() []*db.HeraOrchestrator {
	return []*db.HeraOrchestrator{
		{ID: 1, Name: "alpha"},
		{ID: 2, Name: "beta"},
		{ID: 3, Name: "alpha-two"},
	}
}

func typeInto(m *OrchPickerModal, s string) {
	h := m.InputHandler()
	for _, r := range s {
		h(tcell.NewEventKey(tcell.KeyRune, r, tcell.ModNone), func(tview.Primitive) {})
	}
}

func TestOrchPickerModal_FilterSelectCancel(t *testing.T) {
	t.Run("substring filter narrows by name, case-insensitive", func(t *testing.T) {
		m := NewOrchPickerModal("Adopt x into…", orchs())
		testutil.Equal(t, len(m.filtered), 3)
		typeInto(m, "ALPHA")
		testutil.Equal(t, len(m.filtered), 2) // alpha, alpha-two
		testutil.Equal(t, m.filtered[0].Name, "alpha")
		testutil.Equal(t, m.filtered[1].Name, "alpha-two")
	})

	t.Run("Enter selects the highlighted orchestrator", func(t *testing.T) {
		m := NewOrchPickerModal("t", orchs())
		typeInto(m, "beta")
		h := m.InputHandler()
		h(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone), func(tview.Primitive) {})
		testutil.Equal(t, m.Selected(), true)
		testutil.Equal(t, m.SelectedOrch().Name, "beta")
	})

	t.Run("Enter on empty match list does not select", func(t *testing.T) {
		m := NewOrchPickerModal("t", orchs())
		typeInto(m, "zzz")
		h := m.InputHandler()
		h(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone), func(tview.Primitive) {})
		testutil.Equal(t, m.Selected(), false)
		testutil.Nil(t, m.SelectedOrch())
	})

	t.Run("Esc cancels", func(t *testing.T) {
		m := NewOrchPickerModal("t", orchs())
		h := m.InputHandler()
		h(tcell.NewEventKey(tcell.KeyEscape, 0, tcell.ModNone), func(tview.Primitive) {})
		testutil.Equal(t, m.Canceled(), true)
	})

	t.Run("Down/Up move the cursor within the filtered set", func(t *testing.T) {
		m := NewOrchPickerModal("t", orchs())
		h := m.InputHandler()
		h(tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone), func(tview.Primitive) {})
		testutil.Equal(t, m.cursor, 1)
		h(tcell.NewEventKey(tcell.KeyUp, 0, tcell.ModNone), func(tview.Primitive) {})
		testutil.Equal(t, m.cursor, 0)
	})
}

func TestOrchPickerModal_DrawVariousSizesNoPanic(t *testing.T) {
	cases := []struct{ w, h int }{{1, 10}, {5, 20}, {8, 20}, {9, 20}, {30, 5}, {80, 24}, {200, 60}, {0, 0}}
	for _, tc := range cases {
		m := NewOrchPickerModal("Adopt some-very-long-freelancer-name into…", orchs())
		m.SetRect(0, 0, tc.w, tc.h)
		screen := tcell.NewSimulationScreen("")
		testutil.NoError(t, screen.Init())
		screen.SetSize(tc.w, tc.h)
		m.Draw(screen) // must not panic
	}
}

func TestOrchPickerModal_DrawEmptyList(t *testing.T) {
	m := NewOrchPickerModal("t", nil)
	m.SetRect(0, 0, 80, 24)
	screen := tcell.NewSimulationScreen("")
	testutil.NoError(t, screen.Init())
	screen.SetSize(80, 24)
	m.Draw(screen) // must not panic on the empty-list branch
}
