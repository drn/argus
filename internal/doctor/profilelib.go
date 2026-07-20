package doctor

import (
	"fmt"
	"strings"
)

// ProfileLibraryStatus classifies whether the per-user diligence-profile
// library (~/.argus/profiles/) contains at least one valid profile file. This
// check is independent of the binary-coherence Verdict and the Stop-hook
// status above and never affects argus doctor's exit code — it exists purely
// to surface the silent fail-open behavior of an empty profile library (see
// add-doctor-profile-check). It reports the library's existence only, never a
// project's profile binding — an unbound project is an accepted, unwarned
// state.
type ProfileLibraryStatus int

const (
	// ProfileLibraryFound: at least one *.toml file in the library passes
	// profile validation.
	ProfileLibraryFound ProfileLibraryStatus = iota
	// ProfileLibraryNone: the library directory does not exist, is empty, or
	// every file in it fails validation.
	ProfileLibraryNone
	// ProfileLibraryUnknown: the library directory could not be listed for a
	// reason other than nonexistence (e.g. a permission error) — reported
	// distinctly from ProfileLibraryNone rather than assumed empty.
	ProfileLibraryUnknown
)

// DiagnoseProfileLibrary classifies the diligence-profile library from the
// names of every profile file that passed validation (validNames) and the
// outcome of listing the library directory. dirMissing reports whether
// listErr is specifically a "directory does not exist" error — the common
// never-installed case, classified as ProfileLibraryNone — as opposed to any
// other listing failure, which degrades to ProfileLibraryUnknown rather than
// being assumed empty.
func DiagnoseProfileLibrary(validNames []string, dirMissing bool, listErr error) ProfileLibraryStatus {
	if listErr != nil && !dirMissing {
		return ProfileLibraryUnknown
	}
	if len(validNames) == 0 {
		return ProfileLibraryNone
	}
	return ProfileLibraryFound
}

// profileInstallSnippet is the exact remediation command, mirroring the
// README's diligence-profiles seed-install affordance.
const profileInstallSnippet = "argus profiles install-defaults"

// RenderProfileLibrary builds the human-readable diligence-profile-library
// status line(s) printed by `argus doctor` alongside the binary-coherence
// table and the Stop-hook section.
func RenderProfileLibrary(status ProfileLibraryStatus) string {
	var b strings.Builder
	b.WriteString("\nDiligence profiles (~/.argus/profiles/): ")
	switch status {
	case ProfileLibraryFound:
		b.WriteString("FOUND\n")
	case ProfileLibraryNone:
		fmt.Fprintf(&b, "NONE FOUND\n\nInstall the seed profiles:\n  %s\n", profileInstallSnippet)
	default:
		b.WriteString("UNKNOWN (could not list ~/.argus/profiles/)\n")
	}
	return b.String()
}
