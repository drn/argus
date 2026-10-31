package voicetask

import (
	"testing"

	"github.com/drn/argus/internal/testutil"
)

var projs = []string{"forge", "argus", "thanx-web", "api", "mobile-platform"}

func TestParse(t *testing.T) {
	tests := []struct {
		name     string
		in       string
		projects []string
		def      string
		want     Result
	}{
		{"leading", "In project Forge, fix the login bug.", projs, "argus",
			Result{Project: "forge", Prompt: "fix the login bug.", Source: "spoken"}},
		{"trailing", "fix the login bug in project argus.", projs, "forge",
			Result{Project: "argus", Prompt: "fix the login bug.", Source: "spoken"}},
		{"trailing no punct", "fix the login bug in project argus", projs, "forge",
			Result{Project: "argus", Prompt: "fix the login bug", Source: "spoken"}},
		{"middle", "fix the bug in project argus please", projs, "forge",
			Result{Project: "argus", Prompt: "fix the bug please", Source: "spoken"}},
		{"middle comma", "fix the bug, in project argus, please", projs, "forge",
			Result{Project: "argus", Prompt: "fix the bug, please", Source: "spoken"}},
		{"in the project", "in the project forge add tests", projs, "",
			Result{Project: "forge", Prompt: "add tests", Source: "spoken"}},
		{"colon", "In project: forge add tests", projs, "",
			Result{Project: "forge", Prompt: "add tests", Source: "spoken"}},
		{"colon space before", "in project : forge add tests", projs, "",
			Result{Project: "forge", Prompt: "add tests", Source: "spoken"}},
		{"upper", "IN PROJECT FORGE do it", projs, "",
			Result{Project: "forge", Prompt: "do it", Source: "spoken"}},
		{"multi word", "in project thanx web fix css", projs, "forge",
			Result{Project: "thanx-web", Prompt: "fix css", Source: "spoken"}},
		{"hyphenated", "in project Thanx-Web fix css", projs, "forge",
			Result{Project: "thanx-web", Prompt: "fix css", Source: "spoken"}},
		{"underscore", "in project thanx_web fix css", projs, "forge",
			Result{Project: "thanx-web", Prompt: "fix css", Source: "spoken"}},
		{"three words", "in project mobile platform fix css", projs, "forge",
			Result{Project: "mobile-platform", Prompt: "fix css", Source: "spoken"}},
		{"longest first", "in project thanx web api work", projs, "",
			Result{Project: "thanx-web", Prompt: "api work", Source: "spoken"}},
		{"fuzzy dist1", "in project argos fix it", projs, "forge",
			Result{Project: "argus", Prompt: "fix it", Source: "spoken"}},
		{"fuzzy long dist2", "in project mobile platfrm fix it", projs, "forge",
			Result{Project: "mobile-platform", Prompt: "fix it", Source: "spoken"}},
		{"short name no fuzzy", "in project apx fix it", projs, "forge",
			Result{Project: "forge", Prompt: "in project apx fix it", Source: "default", SpokenProject: "apx"}},
		{"ambiguous fuzzy", "in project argas fix it", []string{"argus", "argos"}, "",
			Result{Prompt: "in project argas fix it", SpokenProject: "argas"}},
		{"unknown spoken falls to default", "in project zzz fix it", projs, "forge",
			Result{Project: "forge", Prompt: "in project zzz fix it", Source: "default", SpokenProject: "zzz"}},
		{"no trigger default", "fix the login bug", projs, "Forge",
			Result{Project: "forge", Prompt: "fix the login bug", Source: "default"}},
		{"default missing", "fix the login bug", projs, "nope",
			Result{Prompt: "fix the login bug"}},
		{"default empty", "fix the login bug", projs, "",
			Result{Prompt: "fix the login bug"}},
		{"default punctuation only", "fix", projs, "--",
			Result{Prompt: "fix"}},
		{"empty transcript", "   ", projs, "forge",
			Result{Project: "forge", Source: "default"}},
		{"trigger at end", "fix the bug in project", projs, "forge",
			Result{Project: "forge", Prompt: "fix the bug in project", Source: "default"}},
		{"trigger at end colon", "fix the bug in project:", projs, "",
			Result{Prompt: "fix the bug in project:"}},
		{"plural", "fix bugs in projects forge", projs, "argus",
			Result{Project: "argus", Prompt: "fix bugs in projects forge", Source: "default"}},
		{"embedded prefix", "within project forge", projs, "",
			Result{Prompt: "within project forge"}},
		{"unicode before", "éin project forge", projs, "",
			Result{Prompt: "éin project forge"}},
		{"only phrase", "in project forge", projs, "",
			Result{Project: "forge", Source: "spoken"}},
		{"first resolving", "in project zzz and in project forge go", projs, "argus",
			Result{Project: "forge", Prompt: "in project zzz and go", Source: "spoken", SpokenProject: "zzz"}},
		{"trailing period name", "in project forge. do it", projs, "",
			Result{Project: "forge", Prompt: ". do it", Source: "spoken"}},
		{"unicode words", "in project café résumé", []string{"café-résumé"}, "",
			Result{Project: "café-résumé", Source: "spoken"}},
		{"unicode body", "naïve fix in project forge", projs, "",
			Result{Project: "forge", Prompt: "naïve fix", Source: "spoken"}},
		{"punct after trigger", "in project, forge", projs, "argus",
			Result{Project: "argus", Prompt: "in project, forge", Source: "default"}},
		{"leading dash word", "in project -forge x", []string{"-forge"}, "",
			Result{Project: "-forge", Prompt: "x", Source: "spoken"}},
		{"stray symbol word", "in project @ x", projs, "",
			Result{Prompt: "in project @ x"}},
		{"dotted name", "in project my.app go", []string{"myapp"}, "",
			Result{Project: "myapp", Prompt: "go", Source: "spoken"}},
		{"empty project entry", "in project forge go", []string{"", "forge"}, "",
			Result{Project: "forge", Prompt: "go", Source: "spoken"}},
		{"multiline", "in project forge\nfix it", projs, "",
			Result{Project: "forge", Prompt: "fix it", Source: "spoken"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testutil.DeepEqual(t, Parse(tt.in, tt.projects, tt.def), tt.want)
		})
	}
}

func TestLevenshtein(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"", "", 0},
		{"abc", "", 3},
		{"", "abc", 3},
		{"argus", "argos", 1},
		{"kitten", "sitting", 3},
		{"same", "same", 0},
	}
	for _, tt := range tests {
		t.Run(tt.a+"_"+tt.b, func(t *testing.T) {
			testutil.Equal(t, levenshtein(tt.a, tt.b), tt.want)
		})
	}
}

func TestFuzzyBudget(t *testing.T) {
	testutil.Equal(t, fuzzyBudget(3), 0)
	testutil.Equal(t, fuzzyBudget(4), 1)
	testutil.Equal(t, fuzzyBudget(7), 1)
	testutil.Equal(t, fuzzyBudget(8), 2)
}
