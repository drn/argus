package review

import (
	"testing"

	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/testutil"
)

// cfgWithCodex returns a config carrying a "codex" backend entry, mirroring
// the default config's built-in codex backend — used to exercise the
// "resolves to a configured backend" leg of finder-id resolution without
// depending on config.DefaultConfig()'s specifics.
func cfgWithCodex() config.Config {
	return config.Config{
		Backends: map[string]config.Backend{
			"codex": {Command: "codex"},
		},
	}
}

func TestNewValidator_WellFormedAccepted(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		panel map[string]any
	}{
		{
			name:  "single in-session finder",
			panel: map[string]any{"finders": []any{"opus"}},
		},
		{
			name:  "multiple in-session finders",
			panel: map[string]any{"finders": []any{"opus", "fable"}},
		},
		{
			name:  "reserved codex finder id resolves to configured backend",
			panel: map[string]any{"finders": []any{"opus", "fable", "codex"}},
		},
		{
			name: "well-formed lens",
			panel: map[string]any{
				"finders": []any{"opus"},
				"lens": []any{
					map[string]any{"name": "test-adversary", "model": "opus", "skill": "hera-review-test-adversary"},
				},
			},
		},
		{
			name: "synthesizer known model",
			panel: map[string]any{
				"finders":     []any{"opus"},
				"synthesizer": "opus",
			},
		},
		{
			name: "fix_verification bool",
			panel: map[string]any{
				"finders":          []any{"opus"},
				"fix_verification": true,
			},
		},
		{
			name: "review_skill alone",
			panel: map[string]any{
				"finders":      []any{"opus"},
				"review_skill": "my-review",
			},
		},
		{
			name: "review_instruction alone",
			panel: map[string]any{
				"finders":            []any{"opus"},
				"review_instruction": "use /my-review to review the work",
			},
		},
		{
			name:  "string-slice finders (Go-authored, not TOML-decoded)",
			panel: map[string]any{"finders": []string{"opus", "fable"}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			errs := NewValidator(cfgWithCodex())(tc.panel)
			testutil.Equal(t, len(errs), 0)
		})
	}
}

func TestNewValidator_EmptyOrUnknownFindersRejected(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		panel map[string]any
	}{
		{"missing finders key", map[string]any{}},
		{"empty finders list", map[string]any{"finders": []any{}}},
		{"unknown finder id", map[string]any{"finders": []any{"opus", "bogus-model"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			errs := NewValidator(cfgWithCodex())(tc.panel)
			testutil.Equal(t, len(errs) > 0, true)
		})
	}
}

func TestNewValidator_MalformedLensRejected(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		panel map[string]any
	}{
		{
			name: "empty lens name",
			panel: map[string]any{
				"finders": []any{"opus"},
				"lens":    []any{map[string]any{"name": "", "model": "opus"}},
			},
		},
		{
			name: "unknown lens model",
			panel: map[string]any{
				"finders": []any{"opus"},
				"lens":    []any{map[string]any{"name": "test-adversary", "model": "bogus-model"}},
			},
		},
		{
			name: "codex is not a valid lens model (lenses run in-session only)",
			panel: map[string]any{
				"finders": []any{"opus"},
				"lens":    []any{map[string]any{"name": "test-adversary", "model": "codex"}},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			errs := NewValidator(cfgWithCodex())(tc.panel)
			testutil.Equal(t, len(errs) > 0, true)
		})
	}
}

func TestNewValidator_ConflictingReviewInstructionRejected(t *testing.T) {
	t.Parallel()
	panel := map[string]any{
		"finders":            []any{"opus"},
		"review_skill":       "my-review",
		"review_instruction": "use /my-review to review the work",
	}
	errs := NewValidator(cfgWithCodex())(panel)
	testutil.Equal(t, len(errs) > 0, true)
}

func TestNewValidator_UnknownSynthesizerRejected(t *testing.T) {
	t.Parallel()
	panel := map[string]any{
		"finders":     []any{"opus"},
		"synthesizer": "codex",
	}
	errs := NewValidator(cfgWithCodex())(panel)
	testutil.Equal(t, len(errs) > 0, true)
}

func TestNewValidator_ReviewSkillAndLensSkillAreFreeForm(t *testing.T) {
	t.Parallel()
	// review_skill / lens.skill are not checked for existence at profile-load
	// time (D7) — any non-empty string is accepted.
	panel := map[string]any{
		"finders":      []any{"opus"},
		"review_skill": "totally-made-up-skill-name",
		"lens": []any{
			map[string]any{"name": "test-adversary", "model": "opus", "skill": "also-made-up"},
		},
	}
	errs := NewValidator(cfgWithCodex())(panel)
	testutil.Equal(t, len(errs), 0)
}

func TestNewValidator_ReturnsAllErrors(t *testing.T) {
	t.Parallel()
	// Three independent violations: unknown finder, empty lens name, conflicting
	// review instruction fields.
	panel := map[string]any{
		"finders":            []any{"bogus-model"},
		"lens":               []any{map[string]any{"name": "", "model": "opus"}},
		"review_skill":       "my-review",
		"review_instruction": "use /my-review to review the work",
	}
	errs := NewValidator(cfgWithCodex())(panel)
	testutil.Equal(t, len(errs), 3)
}

func TestNewValidator_NoConfiguredBackendsRejectsCodex(t *testing.T) {
	t.Parallel()
	// Without a "codex" entry in cfg.Backends, the id does not resolve to
	// anything known.
	panel := map[string]any{"finders": []any{"opus", "codex"}}
	errs := NewValidator(config.Config{})(panel)
	testutil.Equal(t, len(errs) > 0, true)
}

func TestNewValidator_WrongShapedFieldsRejected(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		panel map[string]any
	}{
		{"finders is a bare string, not a list", map[string]any{"finders": "opus"}},
		{"finders contains a non-string item", map[string]any{"finders": []any{"opus", 42}}},
		{"lens is a bare string, not a list", map[string]any{"finders": []any{"opus"}, "lens": "test-adversary"}},
		{"lens list contains a non-table item", map[string]any{"finders": []any{"opus"}, "lens": []any{42}}},
		{"fix_verification is not a bool", map[string]any{"finders": []any{"opus"}, "fix_verification": "yes"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			errs := NewValidator(cfgWithCodex())(tc.panel)
			testutil.Equal(t, len(errs) > 0, true)
		})
	}
}

func TestDecodePanel_WellFormed(t *testing.T) {
	t.Parallel()
	panel := map[string]any{
		"finders":          []any{"opus", "fable"},
		"review_skill":     "my-review",
		"synthesizer":      "opus",
		"fix_verification": true,
		"lens": []any{
			map[string]any{"name": "test-adversary", "model": "opus", "skill": "hera-review-test-adversary"},
		},
	}
	p, err := DecodePanel(panel)
	testutil.NoError(t, err)
	testutil.DeepEqual(t, p.Finders, []string{"opus", "fable"})
	testutil.Equal(t, p.ReviewSkill, "my-review")
	testutil.Equal(t, p.Synthesizer, "opus")
	testutil.Equal(t, p.FixVerification, true)
	testutil.Equal(t, len(p.Lens), 1)
	testutil.Equal(t, p.Lens[0].Name, "test-adversary")
	testutil.Equal(t, p.Lens[0].Model, "opus")
	testutil.Equal(t, p.Lens[0].Skill, "hera-review-test-adversary")
}

func TestDecodePanel_MapShapedLens(t *testing.T) {
	t.Parallel()
	// []map[string]any is the shape a Go-authored panel (not decoded from
	// TOML) might use directly for lens, distinct from the []any-of-map shape
	// BurntSushi/toml produces.
	panel := map[string]any{
		"finders": []string{"opus"},
		"lens": []map[string]any{
			{"name": "test-adversary", "model": "opus"},
		},
	}
	p, err := DecodePanel(panel)
	testutil.NoError(t, err)
	testutil.Equal(t, len(p.Lens), 1)
	testutil.Equal(t, p.Lens[0].Name, "test-adversary")
}

func TestDecodePanel_PropagatesFindersDecodeError(t *testing.T) {
	t.Parallel()
	_, err := DecodePanel(map[string]any{"finders": "opus"})
	testutil.Equal(t, err != nil, true)
}

func TestDecodePanel_PropagatesLensDecodeError(t *testing.T) {
	t.Parallel()
	_, err := DecodePanel(map[string]any{"finders": []any{"opus"}, "lens": "bogus"})
	testutil.Equal(t, err != nil, true)
}

func TestDecodePanel_FixVerificationWrongTypeIgnored(t *testing.T) {
	t.Parallel()
	// DecodePanel is a best-effort typed projection (validation is a separate
	// concern owned by validatePanel) — a wrong-typed fix_verification is
	// simply left at its zero value rather than erroring.
	p, err := DecodePanel(map[string]any{"finders": []any{"opus"}, "fix_verification": "yes"})
	testutil.NoError(t, err)
	testutil.Equal(t, p.FixVerification, false)
}

func TestDecodePanel_EmptyPanel(t *testing.T) {
	t.Parallel()
	p, err := DecodePanel(map[string]any{})
	testutil.NoError(t, err)
	testutil.Equal(t, len(p.Finders), 0)
	testutil.Equal(t, len(p.Lens), 0)
}
