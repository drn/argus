//go:build !darwin

package launchagent

import "errors"

func available() bool { return false }

// PlistPath returns ErrUnsupported on non-darwin.
func PlistPath() (string, error) { return "", ErrUnsupported }

// CurrentStatus returns a Status with Reason set to explain why the
// LaunchAgent is unavailable.
func CurrentStatus() Status {
	return Status{Reason: "macOS only (launchd LaunchAgent)"}
}

// Install returns ErrUnsupported on non-darwin.
func Install(daemonExe string) error { return ErrUnsupported }

// Uninstall returns ErrUnsupported on non-darwin.
func Uninstall() error { return ErrUnsupported }

// EnsureDaemonSymlink is a no-op on non-darwin and returns exe unchanged.
func EnsureDaemonSymlink(exe string) string { return exe }

// ErrUnsupported is returned by Install/Uninstall on non-darwin platforms.
var ErrUnsupported = errors.New("launchagent: unsupported on this platform")
