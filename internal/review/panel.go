// Package review owns the [panel] reviewer-composition grammar consumed by
// the hera-spawn-review orchestration skill (see
// openspec/changes/add-cross-vendor-review/design.md D7). diligence-profiles
// retains [panel] as an opaque map[string]any and injects a validator built
// here into profiles.Validate (mirroring its own knownModels injection) so
// that internal/profiles never imports this package (D-PANEL-SEAM).
package review

import (
	"fmt"
	"strings"

	"github.com/drn/argus/internal/config"
)

// knownInSessionModels mirrors the Agent tool's in-session model enum
// (sonnet/opus/haiku/fable). A panel finder, lens, or synthesizer naming one
// of these runs as an in-session Claude sub-agent — no live backend involved.
var knownInSessionModels = map[string]bool{
	"opus":   true,
	"fable":  true,
	"sonnet": true,
	"haiku":  true,
}

// Lens is one corrective-lens entry (D7 grammar): a distinct review
// instruction that runs as an in-session sub-agent at Model.
type Lens struct {
	Name  string
	Model string
	Skill string
}

// Panel is the typed [panel] reviewer-composition grammar (D7) decoded from
// the opaque map[string]any diligence-profiles retains verbatim.
type Panel struct {
	// Finders is the broad, vendor-diversity-engine reviewer roster. Each id
	// resolves to a known in-session model (opus, fable, sonnet, haiku) or a
	// configured backend name (e.g. "codex") — the latter is a reserved,
	// valid slot even when no shipped profile composes it (D-SCOPE).
	Finders []string
	// Lens holds the corrective lenses — systematic-gap coverage, each with
	// its own instruction. Model is always an in-session model (lenses never
	// run via a foreign backend).
	Lens []Lens
	// ReviewSkill / ReviewInstruction name the broad finders' review
	// instruction; at most one may be set. Both empty means the caller
	// should fall back to the shipped default ("hera-review").
	ReviewSkill       string
	ReviewInstruction string
	// Synthesizer names the in-session model that owns the final [AUTO-FIX]
	// call. Empty means the caller should fall back to a built-in default
	// ("opus").
	Synthesizer string
	// FixVerification toggles the fix-verification phase.
	FixVerification bool
}

// NewValidator returns an injectable [panel]-grammar validator bound to cfg's
// configured backends. Mirrors the diligence-profiles knownModels injection
// (a func passed in) so internal/profiles never imports this package.
func NewValidator(cfg config.Config) func(map[string]any) []error {
	return func(panel map[string]any) []error {
		return validatePanel(panel, cfg)
	}
}

func validatePanel(raw map[string]any, cfg config.Config) []error {
	var errs []error

	finders, ferr := decodeStringList(raw, "finders")
	if ferr != nil {
		errs = append(errs, ferr)
	} else if len(finders) == 0 {
		errs = append(errs, fmt.Errorf("panel: finders must be a non-empty list"))
	}
	for _, f := range finders {
		if !resolvesToFinder(f, cfg) {
			errs = append(errs, fmt.Errorf("panel: finder %q does not resolve to a known in-session model or a configured backend", f))
		}
	}

	lenses, lerr := decodeLensList(raw)
	if lerr != nil {
		errs = append(errs, lerr)
	} else {
		for _, l := range lenses {
			if strings.TrimSpace(l.Name) == "" {
				errs = append(errs, fmt.Errorf("panel: lens has an empty name"))
			}
			if !knownInSessionModels[l.Model] {
				errs = append(errs, fmt.Errorf("panel: lens %q: unknown model %q", l.Name, l.Model))
			}
		}
	}

	synthesizer := stringField(raw, "synthesizer")
	if synthesizer != "" && !knownInSessionModels[synthesizer] {
		errs = append(errs, fmt.Errorf("panel: synthesizer: unknown model %q", synthesizer))
	}

	reviewSkill := stringField(raw, "review_skill")
	reviewInstruction := stringField(raw, "review_instruction")
	if reviewSkill != "" && reviewInstruction != "" {
		errs = append(errs, fmt.Errorf("panel: at most one of review_skill or review_instruction may be set"))
	}

	if v, ok := raw["fix_verification"]; ok {
		if _, isBool := v.(bool); !isBool {
			errs = append(errs, fmt.Errorf("panel: fix_verification must be a bool"))
		}
	}

	return errs
}

// resolvesToFinder reports whether id names a known in-session model or a
// configured backend (cfg.Backends key).
func resolvesToFinder(id string, cfg config.Config) bool {
	if knownInSessionModels[id] {
		return true
	}
	_, ok := cfg.Backends[id]
	return ok
}

// decodeStringList reads a []string-shaped field, tolerating both the
// []interface{} shape BurntSushi/toml produces when decoding into
// map[string]any and a plain []string (for Go-authored panels in tests).
// A missing key returns (nil, nil) — absence is not itself an error, callers
// decide whether that's acceptable. A present-but-wrong-shaped value is an
// error.
func decodeStringList(raw map[string]any, key string) ([]string, error) {
	v, ok := raw[key]
	if !ok || v == nil {
		return nil, nil
	}
	switch vv := v.(type) {
	case []string:
		return vv, nil
	case []any:
		out := make([]string, 0, len(vv))
		for _, item := range vv {
			s, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("panel: %s must be a list of strings", key)
			}
			out = append(out, s)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("panel: %s must be a list of strings", key)
	}
}

// decodeLensList reads the [[panel.lens]] array-of-tables, tolerating both
// the []map[string]interface{} shape BurntSushi/toml produces and a plain
// []any of map[string]any (Go-authored panels in tests). A missing key
// returns (nil, nil).
func decodeLensList(raw map[string]any) ([]Lens, error) {
	v, ok := raw["lens"]
	if !ok || v == nil {
		return nil, nil
	}
	entries, err := asMapSlice(v)
	if err != nil {
		return nil, fmt.Errorf("panel: lens: %w", err)
	}
	out := make([]Lens, 0, len(entries))
	for _, e := range entries {
		out = append(out, Lens{
			Name:  stringField(e, "name"),
			Model: stringField(e, "model"),
			Skill: stringField(e, "skill"),
		})
	}
	return out, nil
}

// asMapSlice normalizes the two shapes a decoded TOML array-of-tables (or a
// Go-authored equivalent) can take into a uniform []map[string]any.
func asMapSlice(v any) ([]map[string]any, error) {
	switch vv := v.(type) {
	case []map[string]any:
		return vv, nil
	case []any:
		out := make([]map[string]any, 0, len(vv))
		for _, item := range vv {
			m, ok := item.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("must be a list of tables")
			}
			out = append(out, m)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("must be a list of tables")
	}
}

// stringField reads an optional string field, returning "" when absent or
// not a string (malformed-type values are not separately reported here — the
// scenarios this grammar guards against are missing/unknown values, not
// wrong-typed ones).
func stringField(raw map[string]any, key string) string {
	v, ok := raw[key]
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
}

// DecodePanel decodes the opaque [panel] map into the typed Panel
// representation, independent of validation. Used by consumers (e.g. a future
// Go-side hera-spawn-review helper) that want the typed shape after a profile
// has already validated.
func DecodePanel(raw map[string]any) (Panel, error) {
	finders, err := decodeStringList(raw, "finders")
	if err != nil {
		return Panel{}, err
	}
	lenses, err := decodeLensList(raw)
	if err != nil {
		return Panel{}, err
	}
	p := Panel{
		Finders:           finders,
		Lens:              lenses,
		ReviewSkill:       stringField(raw, "review_skill"),
		ReviewInstruction: stringField(raw, "review_instruction"),
		Synthesizer:       stringField(raw, "synthesizer"),
	}
	if v, ok := raw["fix_verification"]; ok {
		if b, isBool := v.(bool); isBool {
			p.FixVerification = b
		}
	}
	return p, nil
}
