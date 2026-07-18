// Package profiles implements diligence profiles: named, on-disk TOML presets
// that describe, per archetype, the model + effort + context-window plus
// process/rigor flags and an opaque reviewer-panel block, with a `default`
// profile and `extends` inheritance.
//
// Profiles are referenced by NAME only; the body lives on disk (never in the
// DB). A name is discovered from an in-repo `.argus/profiles/<name>.toml`
// directory (precedence) and a per-user `~/.argus/profiles/<name>.toml` library
// (fallback). See openspec/changes/add-diligence-profiles for the full design.
//
// This package deliberately does NOT import internal/agent: the model
// allow-list's built-in aliases are injected into Validate as a
// func(command) []string (agent.KnownModels) so that a future
// agent → profiles dependency (Stage 3 threading profiles into ResolveModel)
// cannot create an import cycle.
package profiles

// Source identifies where a profile name resolved from.
type Source string

const (
	// SourceInRepo means the file was found in the worktree's .argus/profiles/.
	SourceInRepo Source = "in-repo"
	// SourceLibrary means the file was found in the per-user ~/.argus/profiles/.
	SourceLibrary Source = "library"
)

// CanonicalArchetypes is the fixed set of thirteen recognized archetype names.
// Any archetype table naming something outside this set is a validation error.
var CanonicalArchetypes = []string{
	"brainstorm",
	"orchestrator",
	"big_build",
	"code_slice",
	"bug_fix",
	"review",
	"security_review",
	"synthesis",
	"spec_audit",
	"ci_loop",
	"verify",
	"recovery",
	"docs",
}

// ValidEfforts is the allowed enum for an archetype's effort field.
var ValidEfforts = []string{"low", "medium", "high"}

// ValidWindows is the allowed enum for an archetype's context-window field.
var ValidWindows = []string{"200k", "1m"}

// Archetype is the per-archetype model/effort/window triple. All fields are
// optional; an empty field means "unset" (and is skipped during validation and
// inherited from a parent during extends overlay).
type Archetype struct {
	Model  string `toml:"model" json:"model,omitempty"`
	Effort string `toml:"effort" json:"effort,omitempty"`
	Window string `toml:"window" json:"window,omitempty"`
}

// Rigor holds the per-profile process/diligence flags.
type Rigor struct {
	ReviewPasses      int  `toml:"review_passes" json:"review_passes,omitempty"`
	Gating            bool `toml:"gating" json:"gating,omitempty"`
	SecuritySpotCheck bool `toml:"security_spot_check" json:"security_spot_check,omitempty"`
}

// Profile is a parsed (and, after Load, fully extends-resolved) diligence
// profile.
type Profile struct {
	// Extends names the parent profile this one inherits from (empty = none).
	Extends string `toml:"extends"`
	// Archetype maps an archetype name to its model/effort/window triple.
	Archetype map[string]Archetype `toml:"archetype"`
	// Rigor holds the process flags.
	Rigor Rigor `toml:"rigor"`
	// Panel is the OPAQUE reviewer-panel block, retained verbatim. Its
	// composition grammar is owned by the sibling 2a-xvendor-review capability;
	// this package never interprets it (see D-PANEL-SEAM in the design).
	Panel map[string]any `toml:"panel"`

	// Resolution metadata (not parsed from TOML).
	Name         string `toml:"-"`
	Source       Source `toml:"-"`
	PanelPresent bool   `toml:"-"`
}

func archetypeSet() map[string]bool {
	set := make(map[string]bool, len(CanonicalArchetypes))
	for _, a := range CanonicalArchetypes {
		set[a] = true
	}
	return set
}

func inSet(v string, allowed []string) bool {
	for _, a := range allowed {
		if v == a {
			return true
		}
	}
	return false
}
