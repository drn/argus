package profiles

import (
	"fmt"
	"sort"
	"strings"

	"github.com/drn/argus/internal/config"
)

// validEffortsText is the human-readable allowed-values list for error
// messages, kept in sync with ValidEfforts.
var validEffortsText = strings.Join(ValidEfforts, ", ")

// Validate checks a resolved profile for conformance and returns ALL errors
// found (an empty slice means valid). It checks:
//
//   - every archetype table names a canonical archetype;
//   - an archetype does not set both a scalar model/effort and a menu;
//   - a menu, when present, has at least two entries;
//   - each non-empty effort (scalar, or per-entry within a menu) is one of
//     ValidEfforts;
//   - each non-empty window is one of ValidWindows;
//   - each non-empty model (scalar, or per-entry within a menu) is a member of
//     the union of the built-in backend aliases
//     (knownModels("claude") ∪ knownModels("codex")) and every configured
//     backend's models list;
//   - the [panel] block, if present, is structurally well-formed (it is opaque
//     and its composition grammar is NOT validated here — see D-PANEL-SEAM).
//
// knownModels is injected (pass agent.KnownModels) so this package needs no
// internal/agent import. The extends chain is resolved by Load before Validate
// runs; an extends cycle surfaces as a Load error, aggregated by ValidateName.
func Validate(p *Profile, cfg config.Config, knownModels func(command string) []string) []error {
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

		hasScalar := a.Model != "" || a.Effort != ""
		hasMenu := len(a.Menu) > 0
		if hasScalar && hasMenu {
			errs = append(errs, fmt.Errorf("archetype %q: sets both scalar model/effort and a menu (mutually exclusive)", name))
		}
		if hasMenu && len(a.Menu) < 2 {
			errs = append(errs, fmt.Errorf("archetype %q: menu has %d entry, needs at least 2 to express a choice", name, len(a.Menu)))
		}

		if a.Effort != "" && !inSet(a.Effort, ValidEfforts) {
			errs = append(errs, fmt.Errorf("archetype %q: invalid effort %q (allowed: %s)", name, a.Effort, validEffortsText))
		}
		if a.Window != "" && !inSet(a.Window, ValidWindows) {
			errs = append(errs, fmt.Errorf("archetype %q: invalid window %q (allowed: 200k, 1m)", name, a.Window))
		}
		if a.Model != "" && !allowed[a.Model] {
			errs = append(errs, fmt.Errorf("archetype %q: unknown model %q (not in built-in aliases or any configured backend's models)", name, a.Model))
		}

		for i, m := range a.Menu {
			if m.Effort != "" && !inSet(m.Effort, ValidEfforts) {
				errs = append(errs, fmt.Errorf("archetype %q: menu entry %d: invalid effort %q (allowed: %s)", name, i, m.Effort, validEffortsText))
			}
			if m.Model != "" && !allowed[m.Model] {
				errs = append(errs, fmt.Errorf("archetype %q: menu entry %d: unknown model %q (not in built-in aliases or any configured backend's models)", name, i, m.Model))
			}
		}
	}

	// [panel] is structural-only: when present it is, by construction, a TOML
	// table decoded into a map — so there is nothing further to reject. Its
	// composition grammar is owned by 2a-xvendor-review.

	return errs
}

// ValidateName loads, resolves, and validates a profile by name, aggregating the
// resolution errors (not-found, extends cycle) and the conformance errors into a
// single list. The resolved profile is returned (nil when resolution failed).
func (l *Loader) ValidateName(name string, cfg config.Config, knownModels func(command string) []string) (*Profile, []error) {
	p, err := l.Load(name)
	if err != nil {
		return nil, []error{err}
	}
	return p, Validate(p, cfg, knownModels)
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
