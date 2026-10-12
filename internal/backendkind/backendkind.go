// Package backendkind classifies a backend command template into a stable
// family kind. It is a leaf package (no argus imports) so config, agent, api,
// and tui can all share one classifier; the behavioral side of each family
// lives in agent.Family.
package backendkind

import (
	"path/filepath"
	"slices"
	"strings"
)

// Kind is a backend family's stable identifier. It doubles as the diligence
// profile models key and the REST "kind" field.
type Kind string

const (
	Custom      Kind = ""
	Claude      Kind = "claude"
	Codex       Kind = "codex"
	Pi          Kind = "pi"
	Opencode    Kind = "opencode"
	Antigravity Kind = "antigravity"
)

// Spec is the static, dependency-free description of a backend family.
type Spec struct {
	Kind Kind
	// Binaries are the first-word basenames that identify the family. Matching
	// is exact so wrapper scripts and custom commands stay Custom.
	Binaries []string
	// Label is the human-facing name shown in pickers.
	Label string
	// SkillPrefix is the character the backend uses to invoke a skill in a
	// prompt; drives skill autocomplete in every client.
	SkillPrefix string
}

var specs = []Spec{
	{Kind: Claude, Binaries: []string{"claude"}, Label: "Claude Code", SkillPrefix: "/"},
	{Kind: Codex, Binaries: []string{"codex"}, Label: "Codex", SkillPrefix: "$"},
	{Kind: Pi, Binaries: []string{"pi"}, Label: "Pi", SkillPrefix: "/"},
	{Kind: Opencode, Binaries: []string{"opencode"}, Label: "OpenCode", SkillPrefix: "/"},
	// Only `agy`: the cask links the CLI onto PATH under that name, while a bare
	// `antigravity` may be the IDE's launcher and must stay a custom command.
	{Kind: Antigravity, Binaries: []string{"agy"}, Label: "Antigravity", SkillPrefix: "/"},
}

// customSpec describes any command no family claims.
var customSpec = Spec{Kind: Custom, SkillPrefix: "/"}

// All returns every known family spec in a stable order. The slice is a copy.
func All() []Spec {
	out := make([]Spec, len(specs))
	copy(out, specs)
	return out
}

// Binary returns the basename of the command's first word, or "" for an
// empty command.
func Binary(command string) string {
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return ""
	}
	return filepath.Base(fields[0])
}

// Of classifies a backend command by the basename of its first word, so both
// bare names ("agy") and absolute paths ("/opt/homebrew/bin/agy") match.
func Of(command string) Kind {
	return SpecOf(command).Kind
}

// SpecOf returns the family spec for a command, or the custom spec.
func SpecOf(command string) Spec {
	bin := Binary(command)
	if bin == "" {
		return customSpec
	}
	for _, s := range specs {
		if slices.Contains(s.Binaries, bin) {
			return s
		}
	}
	return customSpec
}

// Lookup returns the spec for a kind. Custom resolves to the custom spec.
func Lookup(k Kind) (Spec, bool) {
	if k == Custom {
		return customSpec, true
	}
	for _, s := range specs {
		if s.Kind == k {
			return s, true
		}
	}
	return Spec{}, false
}
