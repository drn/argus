package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/skills"
	"github.com/drn/argus/internal/testutil"
)

func projectFirstForm(t *testing.T) *NewTaskForm {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	f := NewNewTaskForm(map[string]config.Project{
		"alpha": {Branch: "main", Profile: "default"},
		"beta":  {Branch: "develop", Profile: "other"},
		"gamma": {}, "unused": {},
	}, "alpha", map[string]config.Backend{"b": {Command: "claude"}}, "b")
	f.SetProfileOptions([]string{"default", "other"})
	f.SetAccounts([]AccountOption{{Name: "work", Claude: true}}, func(p string) string {
		if p == "beta" {
			return "work"
		}
		return "default"
	})
	return f
}

func projectKey(f *NewTaskForm, k tcell.Key) { f.InputHandler()(tcell.NewEventKey(k, 0, 0), nil) }

func TestProjectFirst_SelectionAndDefaults(t *testing.T) {
	for _, mode := range []string{"typing", "paste", "initial"} {
		t.Run(mode, func(t *testing.T) {
			f := projectFirstForm(t)
			f.EnableProjectFirst(nil)
			testutil.Equal(t, f.projectStep, true)
			testutil.Equal(t, string(f.projInput), "")
			testutil.Equal(t, f.projACOpen, true)
			testutil.Equal(t, f.projACMatches[f.projACIdx], "alpha")
			wantProject := "beta"
			switch mode {
			case "typing":
				for _, r := range "beta" {
					f.InputHandler()(tcell.NewEventKey(tcell.KeyRune, r, 0), nil)
				}
			case "paste":
				f.PasteHandler()("beta", nil)
			case "initial":
				wantProject = "alpha"
			}
			projectKey(f, tcell.KeyEnter)
			testutil.Equal(t, f.projectStep, false)
			testutil.Equal(t, f.focused, ntFieldPrompt)
			testutil.Equal(t, f.SelectedProject(), wantProject)
			testutil.Equal(t, f.Done(), false)
			// Compare against selecting through the existing full field, including
			// user overrides, rather than encoding a second set of default rules.
			g := projectFirstForm(t)
			g.projInput = []rune(wantProject)
			g.projCursorPos = len(g.projInput)
			g.focused = ntFieldProject
			projectKey(g, tcell.KeyEnter)
			testutil.Equal(t, f.resolvedBranch(), g.resolvedBranch())
			testutil.Equal(t, f.backendIdx, g.backendIdx)
			testutil.DeepEqual(t, f.modelOptions, g.modelOptions)
			testutil.Equal(t, f.modelValue(), g.modelValue())
			testutil.DeepEqual(t, f.profileOptions, g.profileOptions)
			testutil.Equal(t, f.ProfileOverride(), g.ProfileOverride())
			testutil.Equal(t, f.Account(), g.Account())
			testutil.Equal(t, f.SandboxOverride(), g.SandboxOverride())
			testutil.DeepEqual(t, f.skills, g.skills)
		})
	}
}

func TestProjectFirst_BacktrackAndCancel(t *testing.T) {
	f := projectFirstForm(t)
	f.EnableProjectFirst(nil)
	projectKey(f, tcell.KeyEnter)
	f.PasteHandler()("keep this prompt", nil)
	projectKey(f, tcell.KeyTab)
	f.PasteHandler()("keep-name", nil)
	f.sandboxIdx = 2
	f.modelIdx = 1
	// An open autocomplete closes before Escape navigates back.
	f.acOpen = true
	f.acMatches = []skills.SkillItem{{Name: "x"}}
	projectKey(f, tcell.KeyEscape)
	testutil.Equal(t, f.projectStep, false)
	projectKey(f, tcell.KeyEscape)
	testutil.Equal(t, f.projectStep, true)
	testutil.Equal(t, f.projACMatches[f.projACIdx], "alpha")
	f.PasteHandler()("beta", nil)
	projectKey(f, tcell.KeyEnter)
	testutil.Equal(t, string(f.prompt), "keep this prompt")
	testutil.Equal(t, f.EnteredName(), "keep-name")
	testutil.Equal(t, f.Account(), "work")
	testutil.Equal(t, f.sandboxIdx, 2)
	testutil.Equal(t, f.modelIdx, 1)
	// The full project field remains the ordinary editable field.
	f.focused = ntFieldProject
	f.projInput = nil
	f.projCursorPos = 0
	f.PasteHandler()("gamma", nil)
	projectKey(f, tcell.KeyEnter)
	testutil.Equal(t, f.SelectedProject(), "gamma")
	projectKey(f, tcell.KeyEscape)
	testutil.Equal(t, f.projectStep, true)
	projectKey(f, tcell.KeyEscape)
	testutil.Equal(t, f.Canceled(), true)
}

func TestProjectFirst_RecencyAndFiltering(t *testing.T) {
	f := projectFirstForm(t)
	f.EnableProjectFirst([]*model.Task{
		{Project: "beta", CreatedAt: time.Unix(30, 0)},
		{Project: "gamma", CreatedAt: time.Unix(40, 0)},
		{Project: "beta", CreatedAt: time.Unix(10, 0)},
		{Project: "deleted", CreatedAt: time.Unix(50, 0)},
	})
	testutil.DeepEqual(t, f.projACMatches, []string{"alpha", "gamma", "beta", "unused"})
	projectKey(f, tcell.KeyDown)
	testutil.Equal(t, f.projACIdx, 1)
	f.PasteHandler()("a", nil)
	testutil.Equal(t, f.projACIdx, 0)
	testutil.DeepEqual(t, f.projACMatches, []string{"alpha", "gamma", "beta"})
	projectKey(f, tcell.KeyUp)
	projectKey(f, tcell.KeyEnter)
	testutil.Equal(t, f.SelectedProject(), "beta")
	projectKey(f, tcell.KeyEscape)
	testutil.Equal(t, string(f.projInput), "")
	testutil.Equal(t, f.projACMatches[f.projACIdx], "beta")
	testutil.DeepEqual(t, f.projectNames, []string{"alpha", "beta", "gamma", "unused"})
}

func TestProjectFirst_InvalidAndBypass(t *testing.T) {
	for _, tc := range []struct {
		name, def string
		projects  map[string]config.Project
	}{
		{"zero", "", nil}, {"one", "", map[string]config.Project{"only": {}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			f := NewNewTaskForm(tc.projects, tc.def, nil, "")
			f.EnableProjectFirst(nil)
			testutil.Equal(t, f.projectStep, false)
			testutil.Equal(t, f.focused, ntFieldPrompt)
			if tc.name == "one" {
				testutil.Equal(t, f.SelectedProject(), "only")
			}
		})
	}
	t.Run("invalid default and filter", func(t *testing.T) {
		f := projectFirstForm(t)
		f.projInput = []rune("missing")
		f.EnableProjectFirst(nil)
		testutil.Equal(t, f.projACMatches[f.projACIdx], "alpha")
		f.PasteHandler()("no match", nil)
		projectKey(f, tcell.KeyEnter)
		projectKey(f, tcell.KeyTab)
		projectKey(f, tcell.KeyBacktab)
		testutil.Equal(t, f.projectStep, true)
		testutil.Equal(t, f.Done(), false)
		projectKey(f, tcell.KeyEscape)
		testutil.Equal(t, f.Canceled(), true)
	})
	for _, step := range []bool{false, true} {
		t.Run(map[bool]string{true: "step ctrlq", false: "full ctrlq"}[step], func(t *testing.T) {
			f := projectFirstForm(t)
			f.EnableProjectFirst(nil)
			if !step {
				projectKey(f, tcell.KeyEnter)
				f.projACOpen = true
			}
			projectKey(f, tcell.KeyCtrlQ)
			testutil.Equal(t, f.Canceled(), true)
		})
	}
}

func TestProjectFirst_ProjectSkillsAndRendering(t *testing.T) {
	f := projectFirstForm(t)
	path := t.TempDir()
	skillDir := filepath.Join(path, ".claude", "skills", "project-only")
	testutil.NoError(t, os.MkdirAll(skillDir, 0o755))
	testutil.NoError(t, os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\ndescription: Project only\n---\n"), 0o600))
	f.projects["beta"] = config.Project{Path: path, Branch: "develop"}
	for _, p := range []string{"delta", "epsilon", "omega", "zeta"} {
		f.projectNames = append(f.projectNames, p)
		f.projects[p] = config.Project{}
	}
	f.EnableProjectFirst(nil)
	sim := drawSim(t)
	sim.SetSize(100, 30)
	f.SetRect(0, 0, 100, 30)
	f.Draw(sim)
	sim.Show()
	text := screenText(sim, 100, 30)
	testutil.Contains(t, text, "Choose project")
	testutil.Contains(t, text, "(1/8)")
	testutil.Equal(t, strings.Contains(text, "Backend:"), false)
	f.PasteHandler()("no-match", nil)
	sim.Clear()
	f.Draw(sim)
	sim.Show()
	testutil.Contains(t, screenText(sim, 100, 30), "No matching projects")
	projectKey(f, tcell.KeyCtrlU)
	f.PasteHandler()("beta", nil)
	projectKey(f, tcell.KeyEnter)
	testutil.Equal(t, len(f.skills) > 0, true)
	found := false
	for _, s := range f.skills {
		if s.Name == "project-only" {
			found = true
		}
	}
	testutil.Equal(t, found, true)
	sim.Clear()
	f.Draw(sim)
	sim.Show()
	testutil.Contains(t, screenText(sim, 100, 30), "Esc projects")
	projectKey(f, tcell.KeyEscape)
	for _, size := range [][2]int{{10, 30}, {100, 5}} {
		t.Run(strings.Join([]string{itoa(size[0]), itoa(size[1])}, "x"), func(t *testing.T) {
			sim.Clear()
			f.SetRect(0, 0, size[0], size[1])
			f.Draw(sim)
			sim.Show()
			testutil.Equal(t, strings.Contains(screenText(sim, 100, 30), "Choose project"), false)
		})
	}
}
