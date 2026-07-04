package profiles

import (
	"strings"
	"testing"

	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/testutil"
)

// testKnownModels mirrors agent.KnownModels for the two built-in backends,
// injected so this package never imports internal/agent (cycle-safe).
func testKnownModels(command string) []string {
	switch command {
	case "claude":
		return []string{"opus", "sonnet", "haiku"}
	case "codex":
		return []string{"gpt-5-codex", "gpt-5"}
	default:
		return nil
	}
}

// loadOne is a test helper that loads a single profile from a temp library.
func loadOne(t *testing.T, content string) *Profile {
	t.Helper()
	lib := writeProfile(t, t.TempDir(), "p", content)
	p, err := (&Loader{LibraryDir: lib}).Load("p")
	testutil.NoError(t, err)
	return p
}

func errorsText(errs []error) string {
	var b strings.Builder
	for _, e := range errs {
		b.WriteString(e.Error())
		b.WriteString("\n")
	}
	return b.String()
}

func TestValidate_ValidProfile(t *testing.T) {
	p := loadOne(t, `
[archetype.code_slice]
model  = "sonnet"
effort = "high"
window = "1m"

[panel]
reviewers = ["opus"]
`)
	errs := Validate(p, config.Config{}, testKnownModels)
	testutil.Equal(t, len(errs), 0)
}

func TestValidate_UnknownArchetype(t *testing.T) {
	p := loadOne(t, `
[archetype.planner]
model = "opus"
`)
	errs := Validate(p, config.Config{}, testKnownModels)
	testutil.Equal(t, len(errs), 1)
	testutil.Contains(t, errorsText(errs), "planner")
}

func TestValidate_OutOfEnumEffort(t *testing.T) {
	p := loadOne(t, `
[archetype.code_slice]
model  = "sonnet"
effort = "critical"
`)
	errs := Validate(p, config.Config{}, testKnownModels)
	testutil.Equal(t, len(errs), 1)
	testutil.Contains(t, errorsText(errs), "effort")
}

func TestValidate_WidenedEffortEnumAccepted(t *testing.T) {
	for _, effort := range ValidEfforts {
		t.Run(effort, func(t *testing.T) {
			p := loadOne(t, `
[archetype.code_slice]
model  = "sonnet"
effort = "`+effort+`"
`)
			errs := Validate(p, config.Config{}, testKnownModels)
			testutil.Equal(t, len(errs), 0)
		})
	}
}

func TestValidate_OutOfEnumWindow(t *testing.T) {
	p := loadOne(t, `
[archetype.code_slice]
model  = "sonnet"
window = "2m"
`)
	errs := Validate(p, config.Config{}, testKnownModels)
	testutil.Equal(t, len(errs), 1)
	testutil.Contains(t, errorsText(errs), "window")
}

func TestValidate_UnknownModel(t *testing.T) {
	p := loadOne(t, `
[archetype.code_slice]
model = "gemini-2.5-pro"
`)
	errs := Validate(p, config.Config{}, testKnownModels)
	testutil.Equal(t, len(errs), 1)
	testutil.Contains(t, errorsText(errs), "gemini-2.5-pro")
}

func TestValidate_BackendContributedModelAccepted(t *testing.T) {
	p := loadOne(t, `
[archetype.code_slice]
model = "gemini-2.5-pro"
`)
	cfg := config.Config{
		Backends: map[string]config.Backend{
			"gem": {Command: "gemini", Models: []string{"gemini-2.5-pro"}},
		},
	}
	errs := Validate(p, cfg, testKnownModels)
	testutil.Equal(t, len(errs), 0)
}

func TestValidate_CodexBuiltinAccepted(t *testing.T) {
	p := loadOne(t, `
[archetype.code_slice]
model = "gpt-5-codex"
`)
	errs := Validate(p, config.Config{}, testKnownModels)
	testutil.Equal(t, len(errs), 0)
}

func TestValidate_PanelStructuralAccepted(t *testing.T) {
	// A structurally well-formed [panel] block is accepted without grammar checks.
	p := loadOne(t, `
[archetype.docs]
model = "haiku"

[panel]
reviewers   = ["opus", "gpt-5"]
synthesizer = "opus"
weird_field = 42
`)
	errs := Validate(p, config.Config{}, testKnownModels)
	testutil.Equal(t, len(errs), 0)
}

func TestValidate_MenuValid(t *testing.T) {
	p := loadOne(t, `
[archetype.code_slice]
menu = [
  { model = "sonnet", effort = "high" },
  { model = "opus",   effort = "low" },
]
`)
	errs := Validate(p, config.Config{}, testKnownModels)
	testutil.Equal(t, len(errs), 0)
}

func TestValidate_MenuAndScalarRejected(t *testing.T) {
	p := loadOne(t, `
[archetype.code_slice]
model = "sonnet"
menu = [
  { model = "sonnet", effort = "high" },
  { model = "opus",   effort = "low" },
]
`)
	errs := Validate(p, config.Config{}, testKnownModels)
	testutil.Equal(t, len(errs), 1)
	testutil.Contains(t, errorsText(errs), "code_slice")
}

func TestValidate_MenuTooShortRejected(t *testing.T) {
	p := loadOne(t, `
[archetype.code_slice]
menu = [
  { model = "sonnet", effort = "high" },
]
`)
	errs := Validate(p, config.Config{}, testKnownModels)
	testutil.Equal(t, len(errs), 1)
	testutil.Contains(t, errorsText(errs), "code_slice")
}

func TestValidate_MenuEntryValidatedPerField(t *testing.T) {
	p := loadOne(t, `
[archetype.code_slice]
menu = [
  { model = "nope-model", effort = "critical" },
  { model = "opus",       effort = "low" },
]
`)
	errs := Validate(p, config.Config{}, testKnownModels)
	// one bad model + one bad effort within the same menu entry
	testutil.Equal(t, len(errs), 2)
	testutil.Contains(t, errorsText(errs), "nope-model")
	testutil.Contains(t, errorsText(errs), "critical")
}

func TestValidate_ReportsAllErrors(t *testing.T) {
	// Three independent violations: unknown archetype, bad effort, unknown model.
	p := loadOne(t, `
[archetype.planner]
model = "opus"

[archetype.code_slice]
model  = "nope-model"
effort = "critical"
`)
	errs := Validate(p, config.Config{}, testKnownModels)
	// planner (unknown archetype) + code_slice bad effort + code_slice unknown model
	testutil.Equal(t, len(errs), 3)
}

func TestValidateName_CycleReported(t *testing.T) {
	lib := t.TempDir()
	writeProfile(t, lib, "a", `extends = "b"`)
	writeProfile(t, lib, "b", `extends = "a"`)
	l := &Loader{LibraryDir: lib}
	_, errs := l.ValidateName("a", config.Config{}, testKnownModels)
	testutil.Equal(t, len(errs), 1)
	testutil.Contains(t, errorsText(errs), "cycle")
}

func TestValidateName_Valid(t *testing.T) {
	lib := writeProfile(t, t.TempDir(), "ok", `
[archetype.code_slice]
model = "sonnet"
`)
	l := &Loader{LibraryDir: lib}
	p, errs := l.ValidateName("ok", config.Config{}, testKnownModels)
	testutil.Equal(t, len(errs), 0)
	testutil.Equal(t, p.Name, "ok")
}
