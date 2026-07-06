package tui

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

// --- shared helpers for the optional-name-field tests ----------------------

func optNameForm(t *testing.T) *NewTaskForm {
	t.Helper()
	return NewNewTaskForm(
		map[string]config.Project{"p": {Path: "/tmp/p"}}, "p",
		map[string]config.Backend{"b": {}}, "b",
	)
}

// screenRows reads the simulation screen's front buffer into per-row strings.
// drawSim does not Show(), so callers must Show() after Draw before using this.
func screenRows(sim tcell.SimulationScreen) []string {
	cells, w, h := sim.GetContents()
	rows := make([]string, h)
	for y := 0; y < h; y++ {
		var b strings.Builder
		for x := 0; x < w; x++ {
			rs := cells[y*w+x].Runes
			if len(rs) == 0 || rs[0] == 0 {
				b.WriteRune(' ')
				continue
			}
			b.WriteRune(rs[0])
		}
		rows[y] = b.String()
	}
	return rows
}

func anyContains(rows []string, want string) bool {
	for _, r := range rows {
		if strings.Contains(r, want) {
			return true
		}
	}
	return false
}

func rowIndexOf(rows []string, want string) int {
	for i, r := range rows {
		if strings.Contains(r, want) {
			return i
		}
	}
	return -1
}

// --- forms-and-modals: field presence / placeholder / focus / paste --------

// Scenario: Name field renders before the prompt with placeholder.
func TestNewTaskForm_NameField_RendersBeforePromptWithPlaceholder(t *testing.T) {
	f := optNameForm(t)
	f.SetRect(0, 0, 80, 24)
	sim := drawSim(t)
	f.Draw(sim)
	sim.Show()

	rows := screenRows(sim)
	testutil.Equal(t, anyContains(rows, "Name:"), true)
	testutil.Equal(t, anyContains(rows, "(optional)"), true)
	// The name field is rendered BEFORE (above) the prompt label, right under
	// the modal title.
	testutil.Equal(t, rowIndexOf(rows, "Name:") < rowIndexOf(rows, "Prompt:"), true)
}

// Scenario: Name field is reachable in focus order (immediately after prompt).
func TestNewTaskForm_NameField_TabReachableAfterPrompt(t *testing.T) {
	f := optNameForm(t)
	testutil.Equal(t, f.focused, ntFieldPrompt)

	h := f.InputHandler()
	h(tcell.NewEventKey(tcell.KeyTab, 0, 0), func(p tview.Primitive) {})
	testutil.Equal(t, f.focused, ntFieldName)
}

// Scenario: Name field accepts pasted text.
func TestNewTaskForm_NameField_AcceptsPaste(t *testing.T) {
	f := optNameForm(t)
	f.focused = ntFieldName
	ph := f.PasteHandler()
	ph("My Task", func(p tview.Primitive) {})
	testutil.Equal(t, string(f.nameInput), "My Task")
}

// Scenario: Whitespace-only name is treated as blank.
func TestNewTaskForm_NameField_WhitespaceIsBlank(t *testing.T) {
	f := optNameForm(t)
	f.nameInput = []rune("   ")
	testutil.Equal(t, f.EnteredName(), "")

	f.prompt = []rune("do the thing now")
	testutil.Equal(t, f.Task().Name, model.GenerateNameFromPrompt("do the thing now"))
}

// --- new-task submission (forms-and-modals + auto-naming) ------------------

// Scenario: Submit with an explicit name → trimmed, sanitized, user-chosen.
func TestNewTaskForm_NameField_ExplicitNameSanitized(t *testing.T) {
	f := optNameForm(t)
	f.prompt = []rune("some prompt text here")
	f.nameInput = []rune("  My Cool Feature  ")

	want := agent.SafeName("My Cool Feature")
	testutil.Equal(t, f.EnteredName(), want)
	testutil.Equal(t, f.Task().Name, want)
	// User-chosen name wins over the prompt-derived auto name.
	testutil.Equal(t, f.Task().Name != model.GenerateNameFromPrompt("some prompt text here"), true)
}

// Scenario: Submit without a name → prompt-derived auto name.
func TestNewTaskForm_NameField_BlankUsesPromptDerived(t *testing.T) {
	f := optNameForm(t)
	f.prompt = []rune("fix the login flow")
	testutil.Equal(t, f.EnteredName(), "")
	testutil.Equal(t, f.Task().Name, model.GenerateNameFromPrompt("fix the login flow"))
}

// auto-naming: a user-supplied name disables the background LLM rename at
// creation; a blank name leaves auto-naming on (today's behavior).
func TestAutoNameOnCreate(t *testing.T) {
	testutil.Equal(t, autoNameOnCreate(""), true)
	testutil.Equal(t, autoNameOnCreate("my-name"), false)
}

// --- hera-view: `w` / `n` naming decision ----------------------------------

// Scenarios: `w`/`n` with an explicit name use it; blank derives from prompt.
func TestHeraSpawnName(t *testing.T) {
	// Explicit entered name overrides the prompt-derived slug.
	testutil.Equal(t, heraSpawnName("custom-name", "fix the bug"), "custom-name")
	// Blank derives from the prompt exactly as DeriveHeraWorkerName did before.
	testutil.Equal(t, heraSpawnName("", "fix the bug"), agent.DeriveHeraWorkerName("fix the bug"))
}
