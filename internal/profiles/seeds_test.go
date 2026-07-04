package profiles

import (
	"path/filepath"
	"testing"

	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/testutil"
)

// seedLoader points at the in-repo seed example files under docs/profiles/.
// Tests run with CWD = the package dir (internal/profiles), so the repo root is
// two levels up.
func seedLoader() *Loader {
	return &Loader{LibraryDir: filepath.Join("..", "..", "docs", "profiles")}
}

func TestSeeds_EachValidates(t *testing.T) {
	l := seedLoader()
	for _, name := range []string{"default", "lean", "customer_grade"} {
		t.Run(name, func(t *testing.T) {
			p, errs := l.ValidateName(name, config.Config{}, testKnownModels)
			testutil.NotNil(t, p)
			if len(errs) != 0 {
				t.Fatalf("seed %q failed validation: %s", name, errorsText(errs))
			}
		})
	}
}

func TestSeeds_DefaultCoversAllArchetypes(t *testing.T) {
	p, err := seedLoader().Load("default")
	testutil.NoError(t, err)
	for _, name := range CanonicalArchetypes {
		a, ok := p.Archetype[name]
		if !ok {
			t.Errorf("default seed missing archetype %q", name)
			continue
		}
		if a.Model == "" && len(a.Menu) == 0 {
			t.Errorf("default seed archetype %q has no model or menu", name)
		}
	}
	testutil.Equal(t, len(p.Archetype), len(CanonicalArchetypes))
}

func TestSeeds_DefaultCodeSliceIsMenu(t *testing.T) {
	p, err := seedLoader().Load("default")
	testutil.NoError(t, err)
	a := p.Archetype["code_slice"]
	testutil.Equal(t, a.Model, "")
	testutil.Equal(t, a.Effort, "")
	if len(a.Menu) < 2 {
		t.Fatalf("default seed's code_slice menu has %d entries, want >= 2", len(a.Menu))
	}
	testutil.Equal(t, a.Menu[0].Model, "sonnet")
	testutil.Equal(t, a.Menu[0].Effort, "high")
	testutil.Equal(t, a.Menu[1].Model, "opus")
	testutil.Equal(t, a.Menu[1].Effort, "low")
}

func TestSeeds_LeanAndCustomerGradeExtendDefault(t *testing.T) {
	l := seedLoader()

	lean, err := l.Load("lean")
	testutil.NoError(t, err)
	// lean inherits default's model allocation, including the code_slice menu...
	testutil.Equal(t, len(lean.Archetype["code_slice"].Menu), 2)
	testutil.Equal(t, lean.Archetype["code_slice"].Menu[0].Model, "sonnet")
	testutil.Equal(t, lean.Archetype["brainstorm"].Model, "opus")
	// ...and expresses its own rigor.
	testutil.Equal(t, lean.Rigor.ReviewPasses, 1)
	testutil.False(t, lean.Rigor.Gating)

	cg, err := l.Load("customer_grade")
	testutil.NoError(t, err)
	testutil.Equal(t, len(cg.Archetype["code_slice"].Menu), 2) // inherited
	testutil.Equal(t, cg.Archetype["code_slice"].Menu[0].Model, "sonnet")
	testutil.Equal(t, cg.Rigor.ReviewPasses, 2)
	testutil.True(t, cg.Rigor.Gating)
	testutil.True(t, cg.Rigor.SecuritySpotCheck)
	// customer_grade carries an opaque panel block.
	testutil.True(t, cg.PanelPresent)
}

func TestSeeds_DefaultArchetypeDefaultsMatchFramework(t *testing.T) {
	p, err := seedLoader().Load("default")
	testutil.NoError(t, err)
	want := map[string]string{
		"brainstorm":      "opus",
		"orchestrator":    "opus",
		"big_build":       "opus",
		"bug_fix":         "sonnet",
		"review":          "opus",
		"security_review": "opus",
		"synthesis":       "opus",
		"spec_audit":      "sonnet",
		"ci_loop":         "haiku",
		"verify":          "haiku",
		"recovery":        "sonnet",
		"docs":            "haiku",
	}
	for name, model := range want {
		testutil.Equal(t, p.Archetype[name].Model, model)
	}
	// brainstorm carries the high-effort hint from §2.
	testutil.Equal(t, p.Archetype["brainstorm"].Effort, "high")
}
