package tui

import (
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

func TestScheduleForm_New(t *testing.T) {
	sf := NewScheduleForm([]string{"p1", "p2"}, []string{"b1"})
	testutil.Equal(t, sf.Done(), false)
	testutil.Equal(t, sf.Canceled(), false)
	testutil.Equal(t, sf.enabled, true)
	testutil.Equal(t, sf.focused, sfFieldName)
	// "@daily" default schedule
	testutil.Equal(t, string(sf.fields[sfFieldSchedule]), "@daily")
	// Backend options always include leading "" for default.
	testutil.Equal(t, sf.backendOptions[0], "")
	testutil.Equal(t, sf.backendOptions[1], "b1")
}

func TestScheduleForm_LoadSchedule(t *testing.T) {
	sf := NewScheduleForm([]string{"alpha", "beta"}, []string{"claude"})
	s := &model.ScheduledTask{
		ID:       "id-1",
		Name:     "nightly",
		Project:  "beta",
		Backend:  "claude",
		Schedule: "@hourly",
		Prompt:   "do work\nmore work",
		Enabled:  false,
	}
	sf.LoadSchedule(s)

	testutil.Equal(t, sf.editMode, true)
	testutil.Equal(t, sf.ScheduleID(), "id-1")
	testutil.Equal(t, string(sf.fields[sfFieldName]), "nightly")
	testutil.Equal(t, string(sf.fields[sfFieldSchedule]), "@hourly")
	testutil.Equal(t, string(sf.fields[sfFieldPrompt]), "do work\nmore work")
	testutil.Equal(t, sf.enabled, false)
	testutil.Equal(t, sf.projectIdx, 1) // beta is index 1
	testutil.Equal(t, sf.backendIdx, 1) // claude after default
}

func TestScheduleForm_TabNavigation(t *testing.T) {
	sf := NewScheduleForm([]string{"p"}, []string{})
	for i := 1; i < sfFieldCount; i++ {
		sf.HandleKey(tcell.NewEventKey(tcell.KeyTab, 0, 0))
		testutil.Equal(t, sf.focused, i)
	}
	// One more tab wraps back to 0.
	sf.HandleKey(tcell.NewEventKey(tcell.KeyTab, 0, 0))
	testutil.Equal(t, sf.focused, 0)

	// Backtab cycles backward.
	sf.HandleKey(tcell.NewEventKey(tcell.KeyBacktab, 0, 0))
	testutil.Equal(t, sf.focused, sfFieldCount-1)
}

func TestScheduleForm_Escape(t *testing.T) {
	sf := NewScheduleForm([]string{"p"}, []string{})
	sf.HandleKey(tcell.NewEventKey(tcell.KeyEscape, 0, 0))
	testutil.Equal(t, sf.Canceled(), true)
}

func TestScheduleForm_CtrlQ(t *testing.T) {
	sf := NewScheduleForm([]string{"p"}, []string{})
	sf.HandleKey(tcell.NewEventKey(tcell.KeyCtrlQ, 0, tcell.ModNone))
	testutil.Equal(t, sf.Canceled(), true)
}

func TestScheduleForm_CtrlSSubmits(t *testing.T) {
	sf := NewScheduleForm([]string{"p"}, []string{})
	sf.HandleKey(tcell.NewEventKey(tcell.KeyCtrlS, 0, tcell.ModNone))
	testutil.Equal(t, sf.Done(), true)
}

func TestScheduleForm_EnterAdvancesAndSubmits(t *testing.T) {
	sf := NewScheduleForm([]string{"p"}, []string{})
	// Type into name field.
	for _, r := range "morning" {
		sf.HandleKey(tcell.NewEventKey(tcell.KeyRune, r, 0))
	}
	// Enter on text field advances.
	sf.HandleKey(tcell.NewEventKey(tcell.KeyEnter, 0, 0))
	testutil.Equal(t, sf.focused, sfFieldProject)

	// Enter on selector cycles forward, doesn't advance.
	sf.HandleKey(tcell.NewEventKey(tcell.KeyEnter, 0, 0))
	testutil.Equal(t, sf.focused, sfFieldProject)

	// Move to enabled (last) field and submit via Enter.
	sf.focused = sfFieldEnabled
	sf.HandleKey(tcell.NewEventKey(tcell.KeyEnter, 0, 0))
	testutil.Equal(t, sf.Done(), true)
}

func TestScheduleForm_TypeAndCursor(t *testing.T) {
	sf := NewScheduleForm([]string{"p"}, []string{})
	sf.focused = sfFieldName
	for _, r := range "abc" {
		sf.HandleKey(tcell.NewEventKey(tcell.KeyRune, r, 0))
	}
	testutil.Equal(t, string(sf.fields[sfFieldName]), "abc")
	testutil.Equal(t, sf.cursors[sfFieldName], 3)

	// Left moves cursor.
	sf.HandleKey(tcell.NewEventKey(tcell.KeyLeft, 0, 0))
	testutil.Equal(t, sf.cursors[sfFieldName], 2)

	// Right moves cursor.
	sf.HandleKey(tcell.NewEventKey(tcell.KeyRight, 0, 0))
	testutil.Equal(t, sf.cursors[sfFieldName], 3)

	// Backspace removes a rune.
	sf.HandleKey(tcell.NewEventKey(tcell.KeyBackspace2, 0, 0))
	testutil.Equal(t, string(sf.fields[sfFieldName]), "ab")
}

func TestScheduleForm_BackspaceAtZeroIsNoOp(t *testing.T) {
	sf := NewScheduleForm([]string{"p"}, []string{})
	sf.focused = sfFieldName
	sf.HandleKey(tcell.NewEventKey(tcell.KeyBackspace2, 0, 0))
	testutil.Equal(t, string(sf.fields[sfFieldName]), "")
}

func TestScheduleForm_LeftAtZeroIsNoOp(t *testing.T) {
	sf := NewScheduleForm([]string{"p"}, []string{})
	sf.focused = sfFieldName
	sf.HandleKey(tcell.NewEventKey(tcell.KeyLeft, 0, 0))
	testutil.Equal(t, sf.cursors[sfFieldName], 0)
}

func TestScheduleForm_RightAtEndIsNoOp(t *testing.T) {
	sf := NewScheduleForm([]string{"p"}, []string{})
	sf.focused = sfFieldName
	sf.HandleKey(tcell.NewEventKey(tcell.KeyRune, 'a', 0))
	sf.HandleKey(tcell.NewEventKey(tcell.KeyRight, 0, 0))
	testutil.Equal(t, sf.cursors[sfFieldName], 1)
}

func TestScheduleForm_ProjectSelectorCycle(t *testing.T) {
	sf := NewScheduleForm([]string{"a", "b", "c"}, []string{})
	sf.focused = sfFieldProject
	testutil.Equal(t, sf.projectIdx, 0)

	// Right cycles forward.
	sf.HandleKey(tcell.NewEventKey(tcell.KeyRight, 0, 0))
	testutil.Equal(t, sf.projectIdx, 1)

	// Left cycles back.
	sf.HandleKey(tcell.NewEventKey(tcell.KeyLeft, 0, 0))
	testutil.Equal(t, sf.projectIdx, 0)

	// Left wraps backwards.
	sf.HandleKey(tcell.NewEventKey(tcell.KeyLeft, 0, 0))
	testutil.Equal(t, sf.projectIdx, 2)

	// Right wraps forwards.
	sf.HandleKey(tcell.NewEventKey(tcell.KeyRight, 0, 0))
	testutil.Equal(t, sf.projectIdx, 0)
}

func TestScheduleForm_BackendSelectorCycle(t *testing.T) {
	sf := NewScheduleForm([]string{"p"}, []string{"b1", "b2"})
	sf.focused = sfFieldBackend
	// 3 backend options: "" (default), b1, b2
	testutil.Equal(t, len(sf.backendOptions), 3)
	testutil.Equal(t, sf.backendIdx, 0)

	sf.HandleKey(tcell.NewEventKey(tcell.KeyRight, 0, 0))
	testutil.Equal(t, sf.backendIdx, 1)
}

func TestScheduleForm_EnabledToggle(t *testing.T) {
	sf := NewScheduleForm([]string{"p"}, []string{})
	sf.focused = sfFieldEnabled
	testutil.Equal(t, sf.enabled, true)

	// Enter on enabled field submits, but the field's selector still works on left/right.
	sf.HandleKey(tcell.NewEventKey(tcell.KeyLeft, 0, 0))
	testutil.Equal(t, sf.enabled, false)

	sf.HandleKey(tcell.NewEventKey(tcell.KeyRight, 0, 0))
	testutil.Equal(t, sf.enabled, true)
}

func TestScheduleForm_Result(t *testing.T) {
	sf := NewScheduleForm([]string{"alpha", "beta"}, []string{"claude"})
	sf.fields[sfFieldName] = []rune(" my-task ")
	sf.fields[sfFieldSchedule] = []rune("@daily")
	sf.fields[sfFieldPrompt] = []rune("hello world")
	sf.projectIdx = 1 // beta
	sf.backendIdx = 1 // claude
	sf.enabled = false
	sf.scheduleID = "abc"

	got := sf.Result()
	testutil.Equal(t, got.ID, "abc")
	testutil.Equal(t, got.Name, "my-task") // trimmed
	testutil.Equal(t, got.Project, "beta")
	testutil.Equal(t, got.Backend, "claude")
	testutil.Equal(t, got.Schedule, "@daily")
	testutil.Equal(t, got.Prompt, "hello world")
	testutil.Equal(t, got.Enabled, false)
}

func TestScheduleForm_Result_DefaultBackendEmpty(t *testing.T) {
	sf := NewScheduleForm([]string{"p"}, []string{"b"})
	sf.fields[sfFieldName] = []rune("x")
	sf.backendIdx = 0 // "" default
	got := sf.Result()
	testutil.Equal(t, got.Backend, "")
}

func TestScheduleForm_PasteHandler_TextField(t *testing.T) {
	sf := NewScheduleForm([]string{"p"}, []string{})
	sf.focused = sfFieldName
	paste := sf.PasteHandler()
	paste("hello", nil)
	testutil.Equal(t, string(sf.fields[sfFieldName]), "hello")
	testutil.Equal(t, sf.cursors[sfFieldName], 5)
}

func TestScheduleForm_PasteHandler_SelectorIgnored(t *testing.T) {
	sf := NewScheduleForm([]string{"a", "b"}, []string{})
	sf.focused = sfFieldProject
	paste := sf.PasteHandler()
	paste("garbage", nil)
	testutil.Equal(t, sf.projectIdx, 0)
}

func TestScheduleForm_PasteHandler_EmptyNoOp(t *testing.T) {
	sf := NewScheduleForm([]string{"p"}, []string{})
	sf.focused = sfFieldName
	paste := sf.PasteHandler()
	paste("", nil)
	testutil.Equal(t, len(sf.fields[sfFieldName]), 0)
}

func TestScheduleForm_SetError(t *testing.T) {
	sf := NewScheduleForm([]string{"p"}, []string{})
	sf.SetError("oops")
	testutil.Equal(t, sf.errMsg, "oops")
}

func TestScheduleForm_IsSelector(t *testing.T) {
	sf := NewScheduleForm([]string{"p"}, []string{})
	testutil.Equal(t, sf.isSelector(sfFieldName), false)
	testutil.Equal(t, sf.isSelector(sfFieldProject), true)
	testutil.Equal(t, sf.isSelector(sfFieldBackend), true)
	testutil.Equal(t, sf.isSelector(sfFieldSchedule), false)
	testutil.Equal(t, sf.isSelector(sfFieldPrompt), false)
	testutil.Equal(t, sf.isSelector(sfFieldEnabled), true)
}

func TestScheduleForm_ProjectAndBackendValueOOB(t *testing.T) {
	sf := NewScheduleForm([]string{}, []string{})
	// projectIdx is 0 but no projects.
	testutil.Equal(t, sf.projectValue(), "")
	// backendIdx 0 → "" (default slot).
	testutil.Equal(t, sf.backendValue(), "")
}

func TestScheduleForm_CycleEmptyOptions(t *testing.T) {
	sf := NewScheduleForm([]string{}, []string{})
	sf.focused = sfFieldProject
	sf.HandleKey(tcell.NewEventKey(tcell.KeyRight, 0, 0))
	testutil.Equal(t, sf.projectIdx, 0)
}

func TestScheduleForm_Draw(t *testing.T) {
	sf := NewScheduleForm([]string{"alpha", "beta"}, []string{"claude"})
	sf.SetRect(0, 0, 80, 24)
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { screen.Fini() })
	screen.SetSize(80, 24)
	sf.Draw(screen) // must not panic
}

func TestScheduleForm_Draw_EditModeAndError(t *testing.T) {
	sf := NewScheduleForm([]string{"alpha"}, []string{"claude"})
	s := &model.ScheduledTask{ID: "id1", Name: "x", Project: "alpha", Schedule: "@daily", Prompt: "a\nb\nc", Enabled: true}
	sf.LoadSchedule(s)
	sf.SetError("validation failed")
	sf.SetRect(0, 0, 80, 24)
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { screen.Fini() })
	screen.SetSize(80, 24)
	sf.Draw(screen) // must not panic
}

func TestScheduleForm_Draw_TinyRect(t *testing.T) {
	sf := NewScheduleForm([]string{"p"}, []string{})
	sf.SetRect(0, 0, 0, 0)
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { screen.Fini() })
	sf.Draw(screen) // must not panic
}
