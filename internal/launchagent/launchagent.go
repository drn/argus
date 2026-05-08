// Package launchagent manages the macOS LaunchAgent that auto-starts the
// argus daemon at user login.
//
// On non-darwin platforms every operation is a no-op: Available returns false,
// Status returns a reason explaining why, and Install/Uninstall return
// ErrUnsupported.
package launchagent

import "errors"

// ErrUnsupported is returned by Install/Uninstall on platforms where launchd
// is not available. Declared in the shared file so callers on darwin can use
// errors.Is(err, launchagent.ErrUnsupported) without #ifdef-ing the import.
var ErrUnsupported = errors.New("launchagent: unsupported on this platform")

// Label is the launchd job label written into the plist.
const Label = "com.drn.argus.daemon"

// PlistFilename is the basename of the plist file under ~/Library/LaunchAgents.
const PlistFilename = Label + ".plist"

// Status describes the LaunchAgent's installation state.
type Status struct {
	// Installed is true when the plist file exists on disk.
	Installed bool
	// Loaded is true when launchctl recognizes the label as loaded into the
	// gui/<uid> domain. Best-effort — false if launchctl print fails.
	Loaded bool
	// PlistPath is the absolute path the plist file would (or does) live at.
	// Empty on platforms where launchd is not available.
	PlistPath string
	// Reason explains why the LaunchAgent is unavailable. Empty when supported.
	Reason string
}

// Available reports whether LaunchAgent management is supported on this OS.
// True on darwin, false elsewhere.
func Available() bool { return available() }
