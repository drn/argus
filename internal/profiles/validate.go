package profiles

import (
	"fmt"
	"sort"

	"github.com/drn/argus/internal/config"
)

// Validate checks a resolved profile for conformance and returns ALL errors
// found (an empty slice means valid). It checks:
//
//   - every archetype table names a canonical archetype;
//   - each non-empty effort is one of ValidEfforts;
//   - each non-empty window is one of ValidWindows;
//   - each non-empty model is a member of the union of the built-in backend
//     aliases (knownModels("claude") ∪ knownModels("codex")) and every
//     configured backend's models list;
//   - the [panel] block, when present, passes panelValidator if one is
//     injected; when panelValidator is nil, [panel] is accepted on structural
//     shape alone (it is, by construction, a decoded TOML table — nothing
//     further to check).
//
// knownModels is injected (pass agent.KnownModels) so this package needs no
// internal/agent import. panelValidator is injected the same way (pass
// review.NewValidator(cfg)) so this package needs no internal/review import —
// panel composition grammar is owned by the sibling cross-vendor-review
// capability (D-PANEL-SEAM). The extends chain is resolved by Load before
// Validate runs; an extends cycle surfaces as a Load error, aggregated by
// ValidateName.
func Validate(p *Profile, cfg config.Config, knownModels func(command string) []string, panelValidator func(panel map[string]any) []error) []error {
	var errs []error

	allowed := modelAllowList(cfg, knownModels)
	canon := archetypeSet()

	// Iterate in a stable order so the reported error list is deterministic.
	names := make([]string, 0, len(p.Archetype))
	for name := range p.Archetype {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		a := p.Archetype[name]
		if !canon[name] {
			errs = append(errs, fmt.Errorf("unknown archetype %q (not one of the 13 canonical archetypes)", name))
		}
		if a.Effort != "" && !inSet(a.Effort, ValidEfforts) {
			errs = append(errs, fmt.Errorf("archetype %q: invalid effort %q (allowed: low, medium, high)", name, a.Effort))
		}
		if a.Window != "" && !inSet(a.Window, ValidWindows) {
			errs = append(errs, fmt.Errorf("archetype %q: invalid window %q (allowed: 200k, 1m)", name, a.Window))
		}
		if a.Model != "" && !allowed[a.Model] {
			errs = append(errs, fmt.Errorf("archetype %q: unknown model %q (not in built-in aliases or any configured backend's models)", name, a.Model))
		}
	}

	// [panel] grammar is enforced only when a validator is injected (owned by
	// the sibling cross-vendor-review capability — see D-PANEL-SEAM); absent
	// that, a present [panel] is accepted on structural shape alone.
	if panelValidator != nil && p.PanelPresent {
		errs = append(errs, panelValidator(p.Panel)...)
	}

	return errs
}

// ValidateName loads, resolves, and validates a profile by name, aggregating the
// resolution errors (not-found, extends cycle) and the conformance errors into a
// single list. The resolved profile is returned (nil when resolution failed).
func (l *Loader) ValidateName(name string, cfg config.Config, knownModels func(command string) []string, panelValidator func(panel map[string]any) []error) (*Profile, []error) {
	p, err := l.Load(name)
	if err != nil {
		return nil, []error{err}
	}
	return p, Validate(p, cfg, knownModels, panelValidator)
}

// modelAllowList builds the union of built-in backend aliases (for the "claude"
// and "codex" commands) and every configured backend's models list.
func modelAllowList(cfg config.Config, knownModels func(command string) []string) map[string]bool {
	set := map[string]bool{}
	if knownModels != nil {
		for _, cmd := range []string{"claude", "codex"} {
			for _, m := range knownModels(cmd) {
				if m != "" {
					set[m] = true
				}
			}
		}
	}
	for _, b := range cfg.Backends {
		for _, m := range b.Models {
			if m != "" {
				set[m] = true
			}
		}
	}
	return set
}
