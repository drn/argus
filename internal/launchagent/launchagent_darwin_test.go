//go:build darwin

package launchagent

import (
	"encoding/xml"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/drn/argus/internal/testutil"
)

// fakeRunner records launchctl invocations and returns scripted responses
// keyed by the first arg ("print", "bootout", "bootstrap"). A response with
// err != nil simulates a non-zero exit (e.g. "not loaded").
type fakeRunner struct {
	mu     sync.Mutex
	calls  [][]string
	resp   map[string]error // key: first arg
	output map[string][]byte
}

func (f *fakeRunner) run(name string, args ...string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	all := append([]string{name}, args...)
	f.calls = append(f.calls, all)
	if len(args) == 0 {
		return nil, nil
	}
	return f.output[args[0]], f.resp[args[0]]
}

func (f *fakeRunner) callsFor(verb string) [][]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out [][]string
	for _, c := range f.calls {
		if len(c) >= 2 && c[1] == verb {
			out = append(out, c)
		}
	}
	return out
}

// withFakeRunner installs a fakeRunner for the duration of the test.
func withFakeRunner(t *testing.T, f *fakeRunner) {
	t.Helper()
	prev := runner
	runner = f.run
	t.Cleanup(func() { runner = prev })
}

// withTempHome redirects HOME to a tempdir so plist writes/reads don't touch
// the real ~/Library/LaunchAgents.
func withTempHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	prev := homeDir
	homeDir = func() (string, error) { return home, nil }
	t.Cleanup(func() { homeDir = prev })
	return home
}

func TestPlistPath(t *testing.T) {
	home := withTempHome(t)
	got, err := PlistPath()
	testutil.NoError(t, err)
	want := filepath.Join(home, "Library", "LaunchAgents", "com.drn.argus.daemon.plist")
	testutil.Equal(t, got, want)
}

func TestRenderPlist(t *testing.T) {
	out := renderPlist("/usr/local/bin/argusd", "/Users/me/.argus/launchd.log", "/Users/me")
	for _, want := range []string{
		"<key>Label</key>",
		"<string>com.drn.argus.daemon</string>",
		"<string>/usr/local/bin/argusd</string>",
		"<string>daemon</string>",
		"<string>start</string>",
		"<key>RunAtLoad</key>",
		"<true/>",
		"<key>KeepAlive</key>",
		"<key>SuccessfulExit</key>",
		"<false/>",
		"<string>/Users/me/.argus/launchd.log</string>",
		"<string>/Users/me</string>",
	} {
		testutil.Contains(t, out, want)
	}
}

func TestRenderPlist_EscapesXMLSpecialChars(t *testing.T) {
	// Paths can contain & < > " on macOS; raw concat would produce malformed XML.
	out := renderPlist(
		`/path/with/<evil>&"chars"/argusd`,
		`/Users/me/.argus/launchd<.log`,
		`/Users/m&me`,
	)
	for _, escaped := range []string{
		"/path/with/&lt;evil&gt;&amp;",
		"launchd&lt;.log",
		"/Users/m&amp;me",
	} {
		testutil.Contains(t, out, escaped)
	}
	// Sanity: no unescaped specials leaked through.
	if strings.Contains(out, "<evil>") {
		t.Errorf("plist contains unescaped <evil>: %s", out)
	}
	if strings.Contains(out, `&"chars"`) {
		t.Errorf("plist contains unescaped &\"chars\": %s", out)
	}
	// Confirm the result parses as XML.
	if err := xml.Unmarshal([]byte(out), new(struct {
		XMLName xml.Name `xml:"plist"`
	})); err != nil {
		t.Fatalf("plist is not well-formed XML: %v", err)
	}
}

func TestCurrentStatus_NotInstalled(t *testing.T) {
	withTempHome(t)
	withFakeRunner(t, &fakeRunner{
		resp: map[string]error{"print": errors.New("not loaded")},
	})

	s := CurrentStatus()
	testutil.Equal(t, s.Installed, false)
	testutil.Equal(t, s.Loaded, false)
	if !strings.HasSuffix(s.PlistPath, "com.drn.argus.daemon.plist") {
		t.Errorf("PlistPath = %q, want suffix com.drn.argus.daemon.plist", s.PlistPath)
	}
}

func TestCurrentStatus_InstalledAndLoaded(t *testing.T) {
	home := withTempHome(t)
	// Pre-create the plist file.
	plistDir := filepath.Join(home, "Library", "LaunchAgents")
	testutil.NoError(t, os.MkdirAll(plistDir, 0o700))
	testutil.NoError(t, os.WriteFile(filepath.Join(plistDir, "com.drn.argus.daemon.plist"), []byte("dummy"), 0o600))

	withFakeRunner(t, &fakeRunner{}) // print returns nil error → loaded

	s := CurrentStatus()
	testutil.Equal(t, s.Installed, true)
	testutil.Equal(t, s.Loaded, true)
}

func TestInstall_FreshInstall(t *testing.T) {
	withTempHome(t)

	f := &fakeRunner{
		// print fails (not currently loaded), bootstrap succeeds
		resp: map[string]error{"print": errors.New("not loaded")},
	}
	withFakeRunner(t, f)

	err := Install("/Users/me/.argus/argusd")
	testutil.NoError(t, err)

	// Plist file should exist with the right contents.
	path, _ := PlistPath()
	data, err := os.ReadFile(path)
	testutil.NoError(t, err)
	testutil.Contains(t, string(data), "/Users/me/.argus/argusd")
	testutil.Contains(t, string(data), "com.drn.argus.daemon")

	// launchctl should have been called: print (probe), then bootstrap (no bootout
	// because print failed).
	bootouts := f.callsFor("bootout")
	if len(bootouts) != 0 {
		t.Errorf("expected no bootout when not loaded, got %d", len(bootouts))
	}
	bootstraps := f.callsFor("bootstrap")
	if len(bootstraps) != 1 {
		t.Fatalf("expected 1 bootstrap call, got %d", len(bootstraps))
	}
	// bootstrap call: launchctl bootstrap gui/<uid> <plist>
	if !strings.HasPrefix(bootstraps[0][2], "gui/") {
		t.Errorf("bootstrap domain = %q, want gui/<uid>", bootstraps[0][2])
	}
	testutil.Equal(t, bootstraps[0][3], path)
}

func TestInstall_ReinstallBootoutsFirst(t *testing.T) {
	withTempHome(t)

	f := &fakeRunner{} // print returns nil → already loaded
	withFakeRunner(t, f)

	err := Install("/Users/me/.argus/argusd")
	testutil.NoError(t, err)

	bootouts := f.callsFor("bootout")
	if len(bootouts) != 1 {
		t.Fatalf("expected 1 bootout when already loaded, got %d", len(bootouts))
	}
	bootstraps := f.callsFor("bootstrap")
	if len(bootstraps) != 1 {
		t.Fatalf("expected 1 bootstrap, got %d", len(bootstraps))
	}
}

func TestInstall_RequiresExe(t *testing.T) {
	withTempHome(t)
	withFakeRunner(t, &fakeRunner{})

	err := Install("")
	if err == nil {
		t.Fatal("expected error for empty daemonExe")
	}
}

func TestInstall_BootstrapFailureSurfaces(t *testing.T) {
	withTempHome(t)
	f := &fakeRunner{
		resp:   map[string]error{"print": errors.New("not loaded"), "bootstrap": errors.New("permission denied")},
		output: map[string][]byte{"bootstrap": []byte("Bootstrap failed: 5: Input/output error")},
	}
	withFakeRunner(t, f)

	err := Install("/Users/me/.argus/argusd")
	if err == nil {
		t.Fatal("expected error from bootstrap failure")
	}
	testutil.Contains(t, err.Error(), "bootstrap")
}

func TestUninstall_LoadedAgent(t *testing.T) {
	home := withTempHome(t)
	plistDir := filepath.Join(home, "Library", "LaunchAgents")
	testutil.NoError(t, os.MkdirAll(plistDir, 0o700))
	plistPath := filepath.Join(plistDir, "com.drn.argus.daemon.plist")
	testutil.NoError(t, os.WriteFile(plistPath, []byte("dummy"), 0o600))

	f := &fakeRunner{} // print returns nil → loaded
	withFakeRunner(t, f)

	err := Uninstall()
	testutil.NoError(t, err)

	if _, err := os.Stat(plistPath); !os.IsNotExist(err) {
		t.Errorf("plist should be removed, stat err = %v", err)
	}
	if len(f.callsFor("bootout")) != 1 {
		t.Errorf("expected 1 bootout, got %d", len(f.callsFor("bootout")))
	}
}

func TestUninstall_NotInstalledIsNoop(t *testing.T) {
	withTempHome(t)
	f := &fakeRunner{
		resp: map[string]error{"print": errors.New("not loaded")},
	}
	withFakeRunner(t, f)

	err := Uninstall()
	testutil.NoError(t, err) // no-op, no error
	if len(f.callsFor("bootout")) != 0 {
		t.Errorf("expected no bootout when not loaded, got %d", len(f.callsFor("bootout")))
	}
}

func TestEnsureDaemonSymlink_CreatesSymlink(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	exe := filepath.Join(t.TempDir(), "argus-bin")
	testutil.NoError(t, os.WriteFile(exe, []byte("#!/bin/sh\n"), 0o600))

	link := EnsureDaemonSymlink(exe)
	target, err := os.Readlink(link)
	testutil.NoError(t, err)
	testutil.Equal(t, target, exe)
}

func TestEnsureDaemonSymlink_IdempotentWhenAlreadyCorrect(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	exe := filepath.Join(t.TempDir(), "argus-bin")
	testutil.NoError(t, os.WriteFile(exe, []byte("#!/bin/sh\n"), 0o600))

	first := EnsureDaemonSymlink(exe)
	statBefore, err := os.Lstat(first)
	testutil.NoError(t, err)

	second := EnsureDaemonSymlink(exe)
	testutil.Equal(t, second, first)

	statAfter, err := os.Lstat(first)
	testutil.NoError(t, err)
	// Symlink wasn't recreated — mtime preserved.
	testutil.Equal(t, statBefore.ModTime().Equal(statAfter.ModTime()), true)
}

func TestAvailable(t *testing.T) {
	testutil.Equal(t, Available(), true)
}
