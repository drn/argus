package profiles

import (
	"fmt"
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
	errs := Validate(p, config.Config{}, testKnownModels, nil)
	testutil.Equal(t, len(errs), 0)
}

func TestValidate_UnknownArchetype(t *testing.T) {
	p := loadOne(t, `
[archetype.planner]
model = "opus"
`)
	errs := Validate(p, config.Config{}, testKnownModels, nil)
	testutil.Equal(t, len(errs), 1)
	testutil.Contains(t, errorsText(errs), "planner")
}

func TestValidate_OutOfEnumEffort(t *testing.T) {
	p := loadOne(t, `
[archetype.code_slice]
model  = "sonnet"
effort = "max"
`)
	errs := Validate(p, config.Config{}, testKnownModels, nil)
	testutil.Equal(t, len(errs), 1)
	testutil.Contains(t, errorsText(errs), "effort")
}

func TestValidate_OutOfEnumWindow(t *testing.T) {
	p := loadOne(t, `
[archetype.code_slice]
model  = "sonnet"
window = "2m"
`)
	errs := Validate(p, config.Config{}, testKnownModels, nil)
	testutil.Equal(t, len(errs), 1)
	testutil.Contains(t, errorsText(errs), "window")
}

func TestValidate_UnknownModel(t *testing.T) {
	p := loadOne(t, `
[archetype.code_slice]
model = "gemini-2.5-pro"
`)
	errs := Validate(p, config.Config{}, testKnownModels, nil)
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
	errs := Validate(p, cfg, testKnownModels, nil)
	testutil.Equal(t, len(errs), 0)
}

func TestValidate_CodexBuiltinAccepted(t *testing.T) {
	p := loadOne(t, `
[archetype.code_slice]
model = "gpt-5-codex"
`)
	errs := Validate(p, config.Config{}, testKnownModels, nil)
	testutil.Equal(t, len(errs), 0)
}

func TestValidate_PanelStructuralAccepted(t *testing.T) {
	// A structurally well-formed [panel] block is accepted without grammar checks
	// when no panel-grammar validator is injected (nil).
	p := loadOne(t, `
[archetype.docs]
model = "haiku"

[panel]
reviewers   = ["opus", "gpt-5"]
synthesizer = "opus"
weird_field = 42
`)
	errs := Validate(p, config.Config{}, testKnownModels, nil)
	testutil.Equal(t, len(errs), 0)
}

// alwaysRejectPanel and alwaysAcceptPanel are stub panel-grammar validators
// used to prove profiles.Validate applies whatever is injected, without this
// package importing internal/review (the real validator's grammar is tested
// in internal/review; here we only test the injection seam).
func alwaysRejectPanel(map[string]any) []error {
	return []error{fmt.Errorf("stub panel validator: rejected")}
}

func alwaysAcceptPanel(map[string]any) []error {
	return nil
}

func TestValidate_PanelInjectedValidatorApplied(t *testing.T) {
	p := loadOne(t, `
[panel]
finders = ["opus"]
`)
	errs := Validate(p, config.Config{}, testKnownModels, alwaysRejectPanel)
	testutil.Equal(t, len(errs), 1)
	testutil.Contains(t, errorsText(errs), "stub panel validator")
}

func TestValidate_PanelInjectedValidatorAccepts(t *testing.T) {
	p := loadOne(t, `
[panel]
finders = ["opus"]
`)
	errs := Validate(p, config.Config{}, testKnownModels, alwaysAcceptPanel)
	testutil.Equal(t, len(errs), 0)
}

func TestValidate_PanelInjectedValidatorSkippedWhenPanelAbsent(t *testing.T) {
	// A profile with no [panel] table at all never invokes the injected
	// validator — a missing panel is not itself a grammar violation.
	p := loadOne(t, `
[archetype.docs]
model = "haiku"
`)
	errs := Validate(p, config.Config{}, testKnownModels, alwaysRejectPanel)
	testutil.Equal(t, len(errs), 0)
}

func TestValidate_ReportsAllErrors(t *testing.T) {
	// Three independent violations: unknown archetype, bad effort, unknown model.
	p := loadOne(t, `
[archetype.planner]
model = "opus"

[archetype.code_slice]
model  = "nope-model"
effort = "max"
`)
	errs := Validate(p, config.Config{}, testKnownModels, nil)
	// planner (unknown archetype) + code_slice bad effort + code_slice unknown model
	testutil.Equal(t, len(errs), 3)
}

func TestValidateName_CycleReported(t *testing.T) {
	lib := t.TempDir()
	writeProfile(t, lib, "a", `extends = "b"`)
	writeProfile(t, lib, "b", `extends = "a"`)
	l := &Loader{LibraryDir: lib}
	_, errs := l.ValidateName("a", config.Config{}, testKnownModels, nil)
	testutil.Equal(t, len(errs), 1)
	testutil.Contains(t, errorsText(errs), "cycle")
}

func TestValidateName_Valid(t *testing.T) {
	lib := writeProfile(t, t.TempDir(), "ok", `
[archetype.code_slice]
model = "sonnet"
`)
	l := &Loader{LibraryDir: lib}
	p, errs := l.ValidateName("ok", config.Config{}, testKnownModels, nil)
	testutil.Equal(t, len(errs), 0)
	testutil.Equal(t, p.Name, "ok")
}
