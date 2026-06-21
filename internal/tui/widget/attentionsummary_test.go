package widget

import (
	"strings"
	"testing"

	"github.com/drn/argus/internal/testutil"
	"github.com/gdamore/tcell/v2"
)

func TestAttentionSummary_DesiredHeight(t *testing.T) {
	cases := []struct {
		name string
		n    int
		want int
	}{
		{"zero hides", 0, 0},
		{"one", 1, attentionSummaryHeight},
		{"many", 7, attentionSummaryHeight},
		{"negative treated as zero", -3, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := NewAttentionSummary()
			s.SetCount(tc.n)
			testutil.Equal(t, s.DesiredHeight(), tc.want)
		})
	}
}

func TestAttentionSummary_SetCountClampsAndReports(t *testing.T) {
	s := NewAttentionSummary()
	s.SetCount(5)
	testutil.Equal(t, s.Count(), 5)
	s.SetCount(-2) // clamped to zero
	testutil.Equal(t, s.Count(), 0)
}

func TestAttentionSummary_DrawNarrowRect_NoPanic(t *testing.T) {
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	defer screen.Fini()
	screen.SetSize(2, 3)

	s := NewAttentionSummary()
	s.SetCount(3)
	s.SetRect(0, 0, 1, 3) // too narrow for an interior — must not panic
	s.Draw(screen)
	screen.Show()
}

func TestAttentionSummary_DrawZero_NoBorder(t *testing.T) {
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	defer screen.Fini()
	screen.SetSize(20, 6)

	s := NewAttentionSummary()
	s.SetCount(0)
	s.SetRect(0, 0, 20, 0)
	s.Draw(screen)
	screen.Show()

	for x := 0; x < 20; x++ {
		for y := 0; y < 6; y++ {
			cell, _, _ := screen.Get(x, y)
			if cell == "╭" || cell == "╮" || cell == "─" {
				t.Fatalf("found border rune %q at %d,%d when summary should be hidden", cell, x, y)
			}
		}
	}
}

func TestAttentionSummary_Pluralisation(t *testing.T) {
	cases := []struct {
		count int
		want  string
	}{
		{1, "1 task needs input"},
		{2, "2 tasks need input"},
		{12, "12 tasks need input"},
	}
	for _, tc := range cases {
		t.Run(tc.want, func(t *testing.T) {
			screen := tcell.NewSimulationScreen("UTF-8")
			if err := screen.Init(); err != nil {
				t.Fatal(err)
			}
			defer screen.Fini()
			screen.SetSize(30, attentionSummaryHeight)

			s := NewAttentionSummary()
			s.SetCount(tc.count)
			s.SetRect(0, 0, 30, s.DesiredHeight())
			s.Draw(screen)
			screen.Show()

			got := dumpScreen(screen, 30, s.DesiredHeight())
			if !strings.Contains(got, tc.want) {
				t.Errorf("expected %q in rendered output, got:\n%s", tc.want, got)
			}
			if !strings.ContainsRune(got, '╭') {
				t.Errorf("expected border top-left corner in output, got:\n%s", got)
			}
		})
	}
}
