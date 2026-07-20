package review

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/profiles"
	"github.com/drn/argus/internal/testutil"
)

// testKnownModels mirrors agent.KnownModels for the two built-in backends,
// injected so this test never imports internal/agent.
func testKnownModels(command string) []string {
	switch command {
	case "claude":
		return []string{"opus", "sonnet", "haiku", "fable"}
	case "codex":
		return []string{"gpt-5-codex", "gpt-5"}
	default:
		return nil
	}
}

func errorsText(errs []error) string {
	var b strings.Builder
	for _, e := range errs {
		b.WriteString(e.Error())
		b.WriteString("\n")
	}
	return b.String()
}

// TestShippedProfiles_PanelValidatesUnderRealGrammar proves the shipped
// internal/profiles/seeds/{default,lean,customer_grade}.toml [panel] blocks
// validate against the REAL panel-grammar validator (not just structural
// shape) — exercising the injection end-to-end the way a daemon-side caller
// (internal/agent, internal/mcp) would. cfg carries a "codex" backend entry
// (matching config.DefaultConfig()) so the reserved-but-unused codex slot
// would validate if a profile ever named it — none of the shipped profiles do.
func TestShippedProfiles_PanelValidatesUnderRealGrammar(t *testing.T) {
	loader := &profiles.Loader{LibraryDir: filepath.Join("..", "profiles", "seeds")}
	cfg := config.DefaultConfig()
	validator := NewValidator(cfg)

	for _, name := range []string{"default", "lean", "customer_grade"} {
		t.Run(name, func(t *testing.T) {
			p, errs := loader.ValidateName(name, cfg, testKnownModels, validator)
			testutil.NotNil(t, p)
			if len(errs) != 0 {
				t.Fatalf("seed %q failed real panel-grammar validation: %s", name, errorsText(errs))
			}
		})
	}
}

// TestShippedProfiles_NoneComposeCodex proves D-SCOPE's "reserved but not
// shipped" rule: codex validates as a finder id (see
// TestNewValidator_WellFormedAccepted's reserved-codex case) but no shipped
// profile actually names it.
func TestShippedProfiles_NoneComposeCodex(t *testing.T) {
	loader := &profiles.Loader{LibraryDir: filepath.Join("..", "profiles", "seeds")}
	for _, name := range []string{"default", "lean", "customer_grade"} {
		t.Run(name, func(t *testing.T) {
			p, err := loader.Load(name)
			testutil.NoError(t, err)
			panel, derr := DecodePanel(p.Panel)
			testutil.NoError(t, derr)
			for _, f := range panel.Finders {
				if f == "codex" {
					t.Fatalf("seed %q composes codex in [panel] finders — D-SCOPE reserves the slot but defers shipping it", name)
				}
			}
		})
	}
}
