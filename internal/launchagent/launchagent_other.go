//go:build !darwin

package launchagent

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

// ResolveDaemonExe returns ErrUnsupported on non-darwin.
func ResolveDaemonExe() (string, error) { return "", ErrUnsupported }
