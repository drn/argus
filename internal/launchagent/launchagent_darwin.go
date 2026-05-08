//go:build darwin

package launchagent

import (
	"encoding/xml"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/drn/argus/internal/db"
)

func available() bool { return true }

// runner executes external commands. Tests override this to capture invocations
// without shelling out. The only call site passes the literal "launchctl" with
// fixed verbs ("print", "bootout", "bootstrap") and a plist path we wrote
// ourselves — no untrusted input reaches the shell.
var runner = func(name string, args ...string) ([]byte, error) {
	return exec.Command(name, args...).CombinedOutput() //nolint:gosec // see comment above
}

// homeDir resolves the user's home directory. Indirected through a variable so
// tests can override it after t.Setenv("HOME", ...) without depending on
// os.UserHomeDir caching behavior.
var homeDir = func() (string, error) { return os.UserHomeDir() }

// PlistPath returns the absolute path to the LaunchAgent plist file.
func PlistPath() (string, error) {
	home, err := homeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home: %w", err)
	}
	return filepath.Join(home, "Library", "LaunchAgents", PlistFilename), nil
}

// CurrentStatus reports whether the plist exists and is loaded into launchd.
func CurrentStatus() Status {
	s := Status{}
	path, err := PlistPath()
	if err != nil {
		s.Reason = err.Error()
		return s
	}
	s.PlistPath = path
	if _, err := os.Stat(path); err == nil {
		s.Installed = true
	}
	// `launchctl print gui/<uid>/<label>` exits 0 when loaded, non-zero otherwise.
	target := fmt.Sprintf("gui/%d/%s", os.Getuid(), Label)
	if _, err := runner("launchctl", "print", target); err == nil {
		s.Loaded = true
	}
	return s
}

// Install writes the plist and bootstraps it into launchd. If the agent is
// already loaded it is booted out first so the new plist takes effect.
//
// daemonExe is the absolute path that launchd will exec. Callers typically
// pass the path to a stable symlink (e.g. ~/.argus/argusd) so Activity Monitor
// shows a friendly process name.
func Install(daemonExe string) error {
	if daemonExe == "" {
		return fmt.Errorf("daemonExe is required")
	}
	path, err := PlistPath()
	if err != nil {
		return err
	}
	home, err := homeDir()
	if err != nil {
		return fmt.Errorf("resolve home: %w", err)
	}

	// Ensure ~/Library/LaunchAgents exists.
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create launch agents dir: %w", err)
	}
	// Ensure ~/.argus exists for the launchd stdout/stderr file.
	if err := os.MkdirAll(db.DataDir(), 0o700); err != nil {
		return fmt.Errorf("create data dir: %w", err)
	}

	logPath := filepath.Join(db.DataDir(), "launchd.log")
	plist := renderPlist(daemonExe, logPath, home)
	if err := os.WriteFile(path, []byte(plist), 0o600); err != nil {
		return fmt.Errorf("write plist: %w", err)
	}

	// If already loaded, bootout first so the new plist takes effect.
	target := fmt.Sprintf("gui/%d/%s", os.Getuid(), Label)
	if _, err := runner("launchctl", "print", target); err == nil {
		if out, err := runner("launchctl", "bootout", target); err != nil {
			return fmt.Errorf("bootout existing agent: %w (output: %s)", err, strings.TrimSpace(string(out)))
		}
	}

	domain := fmt.Sprintf("gui/%d", os.Getuid())
	if out, err := runner("launchctl", "bootstrap", domain, path); err != nil {
		return fmt.Errorf("bootstrap agent: %w (output: %s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Uninstall bootouts the agent (if loaded) and removes the plist file.
// Both steps are best-effort: a missing plist is not an error.
func Uninstall() error {
	path, err := PlistPath()
	if err != nil {
		return err
	}
	target := fmt.Sprintf("gui/%d/%s", os.Getuid(), Label)
	// Bootout is only meaningful if currently loaded — print exits 0 when so.
	if _, err := runner("launchctl", "print", target); err == nil {
		if out, err := runner("launchctl", "bootout", target); err != nil {
			return fmt.Errorf("bootout agent: %w (output: %s)", err, strings.TrimSpace(string(out)))
		}
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove plist: %w", err)
	}
	return nil
}

// ResolveDaemonExe returns the path that should be written into the LaunchAgent
// plist's ProgramArguments[0] for the currently-running argus binary. Resolves
// os.Executable, follows symlinks, and ensures the ~/.argus/argusd symlink
// points at it (so Activity Monitor displays "argusd"). Surfaces the same
// triple-step pattern that both the CLI install path and the Settings toggle
// would otherwise duplicate.
func ResolveDaemonExe() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolve executable: %w", err)
	}
	if resolved, rerr := filepath.EvalSymlinks(exe); rerr == nil {
		exe = resolved
	}
	return EnsureDaemonSymlink(exe), nil
}

// EnsureDaemonSymlink creates the ~/.argus/argusd symlink pointing at exe and
// returns the symlink path. Matches the pattern used by dclient.AutoStart so
// Activity Monitor displays "argusd" instead of the binary's filename.
//
// If the symlink already points at exe, no work is done. If creating the
// symlink fails (e.g. read-only filesystem), exe is returned as-is so the
// caller can still write a working plist.
func EnsureDaemonSymlink(exe string) string {
	link := filepath.Join(db.DataDir(), "argusd")
	if target, err := os.Readlink(link); err == nil && target == exe {
		return link
	}
	if err := os.MkdirAll(db.DataDir(), 0o700); err != nil {
		return exe
	}
	_ = os.Remove(link)
	if err := os.Symlink(exe, link); err != nil {
		return exe
	}
	return link
}

// renderPlist generates the LaunchAgent plist XML. Exposed at package level
// for tests; the public API is Install/Uninstall/CurrentStatus.
//
// All path/user inputs are XML-escaped — paths containing `&`, `<`, `>`, or
// `"` are legal on macOS and would otherwise produce a malformed plist that
// launchctl silently rejects.
func renderPlist(daemonExe, logPath, home string) string {
	// KeepAlive { SuccessfulExit = false } means: restart the daemon if it
	// exits non-zero (a crash), but honor a clean `argus daemon stop`.
	return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>` + Label + `</string>
	<key>ProgramArguments</key>
	<array>
		<string>` + xmlEscape(daemonExe) + `</string>
		<string>daemon</string>
		<string>start</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<dict>
		<key>SuccessfulExit</key>
		<false/>
	</dict>
	<key>StandardOutPath</key>
	<string>` + xmlEscape(logPath) + `</string>
	<key>StandardErrorPath</key>
	<string>` + xmlEscape(logPath) + `</string>
	<key>WorkingDirectory</key>
	<string>` + xmlEscape(home) + `</string>
	<key>ProcessType</key>
	<string>Interactive</string>
</dict>
</plist>
`
}

func xmlEscape(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}
