// Package buildid extracts a binary's version-control identity (commit SHA +
// dirty flag) from the build info Go stamps in at compile time. It is the
// display-only half of binary-skew detection: the daemon, session-supervisor,
// and TUI each read their OWN identity via Current() and report it so a skew
// modal / `argus doctor` can name the exact build each process is running.
//
// The identity is BLANK for binaries built outside a module's git tree (Go
// only stamps vcs.* when building from within one), so it is never the gating
// signal for staleness — that stays the SHA-256 content hash. VCS info is for
// human-readable display and root-cause diagnosis only.
package buildid

import "runtime/debug"

// VCS is the version-control identity Go records in a binary built from inside
// a module's git tree. Both fields are zero-valued when the binary was built
// outside a git tree (e.g. from a source archive), which callers MUST treat as
// "unknown", never as an error.
type VCS struct {
	Revision string // vcs.revision — the full commit SHA; empty when built outside a git tree
	Modified bool   // vcs.modified — the working tree was dirty at build time
}

// Present reports whether a commit revision was recorded. Display code uses it
// to decide between the rich SHA identity and the content-hash fallback.
func (v VCS) Present() bool { return v.Revision != "" }

// readBuildInfo is a seam so tests can inject synthetic build info without
// depending on how the test binary itself was compiled.
var readBuildInfo = debug.ReadBuildInfo

// Current reads runtime/debug.ReadBuildInfo() and extracts the caller binary's
// VCS identity. Returns a zero VCS (Present() == false) when build info is
// unavailable or carries no vcs.* settings.
func Current() VCS {
	info, ok := readBuildInfo()
	if !ok {
		return VCS{}
	}
	return fromSettings(info.Settings)
}

// fromSettings is the pure extraction: pull vcs.revision + vcs.modified out of
// the build settings. Kept separate from Current so it is unit-testable without
// the ReadBuildInfo seam.
func fromSettings(settings []debug.BuildSetting) VCS {
	var v VCS
	for _, s := range settings {
		switch s.Key {
		case "vcs.revision":
			v.Revision = s.Value
		case "vcs.modified":
			v.Modified = s.Value == "true"
		}
	}
	return v
}
