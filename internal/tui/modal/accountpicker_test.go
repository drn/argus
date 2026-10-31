package modal

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/drn/argus/internal/testutil"
)

func pickerChoices() []AccountChoice {
	return []AccountChoice{{Name: "auto", Detail: "most headroom"}, {Name: "personal", Detail: "5h 0% · wk 0%"}}
}

func TestAccountPickerModal_Decisions(t *testing.T) {
	noFocus := func(tview.Primitive) {}
	press := func(m *AccountPickerModal, k tcell.Key, r rune) {
		m.InputHandler()(tcell.NewEventKey(k, r, tcell.ModNone), noFocus)
	}

	t.Run("enter selects the first choice", func(t *testing.T) {
		m := NewAccountPickerModal("Move", "", pickerChoices())
		press(m, tcell.KeyEnter, 0)
		testutil.Equal(t, m.Selected(), "auto")
		testutil.False(t, m.Canceled())
	})
	t.Run("down then enter, clamped", func(t *testing.T) {
		m := NewAccountPickerModal("Move", "", pickerChoices())
		press(m, tcell.KeyDown, 0)
		press(m, tcell.KeyDown, 0)
		press(m, tcell.KeyEnter, 0)
		testutil.Equal(t, m.Selected(), "personal")
	})
	t.Run("j/k navigate", func(t *testing.T) {
		m := NewAccountPickerModal("Move", "", pickerChoices())
		press(m, tcell.KeyRune, 'j')
		press(m, tcell.KeyRune, 'k')
		press(m, tcell.KeyUp, 0)
		press(m, tcell.KeyEnter, 0)
		testutil.Equal(t, m.Selected(), "auto")
	})
	t.Run("esc and ctrl+q cancel", func(t *testing.T) {
		m := NewAccountPickerModal("Move", "", pickerChoices())
		press(m, tcell.KeyEscape, 0)
		testutil.True(t, m.Canceled())
		testutil.Equal(t, m.Selected(), "")
		m2 := NewAccountPickerModal("Move", "", pickerChoices())
		press(m2, tcell.KeyCtrlQ, 0)
		testutil.True(t, m2.Canceled())
	})
	t.Run("enter with no choices selects nothing", func(t *testing.T) {
		m := NewAccountPickerModal("Move", "", nil)
		press(m, tcell.KeyEnter, 0)
		testutil.Equal(t, m.Selected(), "")
	})
}

func TestAccountPickerModal_Draw(t *testing.T) {
	for _, tc := range []struct {
		name     string
		warning  string
		w, h     int
		contains []string
	}{
		{"with warning", "The running agent will be interrupted.", 80, 20, []string{"Move task", "interrupted", "❯ auto  most headroom", "personal  5h 0% · wk 0%", "Enter = move"}},
		{"no warning", "", 80, 20, []string{"Move task", "❯ auto"}},
		{"tiny terminal does not panic", "warn", 10, 4, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			screen := tcell.NewSimulationScreen("UTF-8")
			testutil.NoError(t, screen.Init())
			screen.SetSize(tc.w, tc.h)
			m := NewAccountPickerModal("Move task", tc.warning, pickerChoices())
			m.SetRect(0, 0, tc.w, tc.h)
			m.Draw(screen)
			screen.Show()
			text := strings.Join(screenText(screen, tc.w, tc.h), "\n")
			for _, want := range tc.contains {
				testutil.Contains(t, text, want)
			}
			testutil.Equal(t, m.Title(), "Move task")
		})
	}
}
