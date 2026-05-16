package macapps

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/drn/argus/internal/testutil"
)

// writeBundle creates a fake .app directory with an XML Info.plist + optional
// .sdef file. Returns the bundle path. Used by every Scan test so we can
// exercise the parser against deterministic fixtures rather than the real
// /Applications directory (which would make tests host-dependent).
func writeBundle(t *testing.T, root, name, plistBody string, sdefName string) string {
	t.Helper()
	bundle := filepath.Join(root, name+".app")
	contents := filepath.Join(bundle, "Contents")
	resources := filepath.Join(contents, "Resources")
	testutil.NoError(t, os.MkdirAll(resources, 0o755))
	testutil.NoError(t, os.WriteFile(filepath.Join(contents, "Info.plist"), []byte(plistBody), 0o644))
	if sdefName != "" {
		testutil.NoError(t, os.WriteFile(filepath.Join(resources, sdefName), []byte("<dictionary/>"), 0o644))
	}
	return bundle
}

const plistHeader = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">`

const plistFooter = `</plist>`

func plistOf(pairs map[string]string) string {
	var b strings.Builder
	b.WriteString(plistHeader)
	b.WriteString("<dict>")
	for k, v := range pairs {
		b.WriteString("<key>")
		b.WriteString(k)
		b.WriteString("</key>")
		// Heuristic: "true"/"false" → bool; everything else → string.
		switch v {
		case "true":
			b.WriteString("<true/>")
		case "false":
			b.WriteString("<false/>")
		default:
			b.WriteString("<string>")
			b.WriteString(v)
			b.WriteString("</string>")
		}
	}
	b.WriteString("</dict>")
	b.WriteString(plistFooter)
	return b.String()
}

func TestScan_FindsBundlesAndIdentifies(t *testing.T) {
	root := t.TempDir()
	writeBundle(t, root, "Foo", plistOf(map[string]string{
		"CFBundleIdentifier":  "com.example.foo",
		"CFBundleDisplayName": "Foo App",
	}), "")
	writeBundle(t, root, "Bar", plistOf(map[string]string{
		"CFBundleIdentifier": "com.example.bar",
		"CFBundleName":       "Bar",
	}), "")
	// No Info.plist file at all → silently skipped.
	noPlist := filepath.Join(root, "Broken.app")
	testutil.NoError(t, os.MkdirAll(noPlist, 0o755))

	apps := Scan([]string{root})
	if len(apps) != 2 {
		t.Fatalf("expected 2 apps, got %d: %#v", len(apps), apps)
	}
	// Sorted by lowercase Name: Bar, Foo App.
	testutil.Equal(t, apps[0].Name, "Bar")
	testutil.Equal(t, apps[0].BundleID, "com.example.bar")
	testutil.Equal(t, apps[1].Name, "Foo App")
	testutil.Equal(t, apps[1].BundleID, "com.example.foo")
}

func TestScan_FallsBackToDirNameWhenNoBundleName(t *testing.T) {
	root := t.TempDir()
	writeBundle(t, root, "Plainoldapp", plistOf(map[string]string{
		"CFBundleIdentifier": "com.example.plain",
	}), "")
	apps := Scan([]string{root})
	if len(apps) != 1 {
		t.Fatalf("expected 1 app, got %d", len(apps))
	}
	// No CFBundleDisplayName or CFBundleName → fall back to dir basename.
	testutil.Equal(t, apps[0].Name, "Plainoldapp")
}

func TestScan_SkipsBundlesWithoutIdentifier(t *testing.T) {
	root := t.TempDir()
	writeBundle(t, root, "NoID", plistOf(map[string]string{
		"CFBundleName": "NoID",
	}), "")
	writeBundle(t, root, "Good", plistOf(map[string]string{
		"CFBundleIdentifier": "com.example.good",
	}), "")
	apps := Scan([]string{root})
	if len(apps) != 1 {
		t.Fatalf("expected 1 app, got %d: %#v", len(apps), apps)
	}
	testutil.Equal(t, apps[0].BundleID, "com.example.good")
}

func TestScan_DeduplicatesByBundleID(t *testing.T) {
	root1, root2 := t.TempDir(), t.TempDir()
	body := plistOf(map[string]string{"CFBundleIdentifier": "com.example.dup", "CFBundleName": "Dup"})
	writeBundle(t, root1, "First", body, "")
	writeBundle(t, root2, "Second", body, "")
	apps := Scan([]string{root1, root2})
	if len(apps) != 1 {
		t.Fatalf("expected 1 app after dedup, got %d", len(apps))
	}
	// First-scanned wins — root1 came before root2.
	testutil.Equal(t, filepath.Dir(apps[0].Path), root1)
}

func TestScan_NonExistentDirIsSkipped(t *testing.T) {
	apps := Scan([]string{"/this/path/does/not/exist/at/all"})
	if len(apps) != 0 {
		t.Errorf("expected empty result for missing dir, got %#v", apps)
	}
}

func TestScan_EmptyDirsUsesDefaults(t *testing.T) {
	// We can't assert specific apps (host-dependent) but Scan(nil) must not
	// panic and must return a non-nil slice on macOS where /Applications and
	// /System/Applications exist.
	if _, err := os.Stat("/System/Applications"); err != nil {
		t.Skip("not on macOS; default dirs unavailable")
	}
	apps := Scan(nil)
	if apps == nil {
		t.Fatal("Scan(nil) returned nil — must always return a slice")
	}
}

func TestExpandHome(t *testing.T) {
	cases := []struct {
		name, in, home, want string
	}{
		{"tilde-slash", "~/foo", "/Users/me", "/Users/me/foo"},
		{"bare-tilde", "~", "/Users/me", "/Users/me"},
		{"absolute-passthrough", "/abs/path", "/Users/me", "/abs/path"},
		{"empty-home-returns-as-is", "~/foo", "", "~/foo"},
		{"empty-path", "", "/Users/me", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			testutil.Equal(t, expandHome(tc.in, tc.home), tc.want)
		})
	}
}

func TestScriptable_NSAppleScriptEnabledBool(t *testing.T) {
	root := t.TempDir()
	writeBundle(t, root, "Scripty", plistOf(map[string]string{
		"CFBundleIdentifier":   "com.example.scripty",
		"NSAppleScriptEnabled": "true",
	}), "")
	apps := Scan([]string{root})
	testutil.Equal(t, len(apps), 1)
	testutil.Equal(t, apps[0].Scriptable, true)
}

func TestScriptable_NSAppleScriptEnabledStringYes(t *testing.T) {
	// Finder uses NSAppleScriptEnabled=<string>Yes</string> rather than a
	// boolean — we must accept that idiom.
	root := t.TempDir()
	body := plistHeader + `<dict>
		<key>CFBundleIdentifier</key><string>com.example.finderish</string>
		<key>NSAppleScriptEnabled</key><string>Yes</string>
	</dict>` + plistFooter
	writeBundle(t, root, "Finderish", body, "")
	apps := Scan([]string{root})
	testutil.Equal(t, len(apps), 1)
	testutil.Equal(t, apps[0].Scriptable, true)
}

func TestScriptable_OSAScriptingDefinitionKey(t *testing.T) {
	root := t.TempDir()
	writeBundle(t, root, "OSAish", plistOf(map[string]string{
		"CFBundleIdentifier":     "com.example.osaish",
		"OSAScriptingDefinition": "OSAish.sdef",
	}), "")
	apps := Scan([]string{root})
	testutil.Equal(t, apps[0].Scriptable, true)
}

func TestScriptable_SdefFileFallback(t *testing.T) {
	// No Info.plist key, but a .sdef sitting in Resources/ — Apple's older
	// system apps register scripting this way. Must still flag scriptable.
	root := t.TempDir()
	writeBundle(t, root, "Sdefonly", plistOf(map[string]string{
		"CFBundleIdentifier": "com.example.sdefonly",
	}), "Sdefonly.sdef")
	apps := Scan([]string{root})
	testutil.Equal(t, apps[0].Scriptable, true)
}

func TestScriptable_NonScriptableApp(t *testing.T) {
	root := t.TempDir()
	writeBundle(t, root, "Plain", plistOf(map[string]string{
		"CFBundleIdentifier": "com.example.plain",
	}), "")
	apps := Scan([]string{root})
	testutil.Equal(t, apps[0].Scriptable, false)
}

func TestScanScriptable_FiltersOutNonScriptable(t *testing.T) {
	root := t.TempDir()
	writeBundle(t, root, "Plain", plistOf(map[string]string{
		"CFBundleIdentifier": "com.example.plain",
	}), "")
	writeBundle(t, root, "Scripty", plistOf(map[string]string{
		"CFBundleIdentifier":   "com.example.scripty",
		"NSAppleScriptEnabled": "true",
	}), "")
	apps := ScanScriptable([]string{root})
	if len(apps) != 1 {
		t.Fatalf("expected 1 scriptable app, got %d: %#v", len(apps), apps)
	}
	testutil.Equal(t, apps[0].BundleID, "com.example.scripty")
}

func TestIsValidBundleID(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"messages-legacy", "com.apple.iChat", true},
		{"messages-binary", "com.apple.MobileSMS", true},
		{"finder", "com.apple.finder", true},
		{"hyphens", "com.example.my-app", true},
		{"single-word", "Finder", true},
		// Edge cases that the agent.IsValidBundleID test pins too — the two
		// regexes must agree.
		{"trailing-dot", "com.apple.", true},
		{"trailing-hyphen", "com.apple-", true},
		{"empty", "", false},
		{"leading-dot", ".com.apple.iChat", false},
		{"leading-hyphen", "-com.apple.iChat", false},
		{"space", "com.apple iChat", false},
		{"quote", `com.apple"iChat`, false},
		{"paren", "com.apple)iChat", false},
		{"slash", "com/apple/iChat", false},
		{"underscore", "com.apple_iChat", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			testutil.Equal(t, IsValidBundleID(tc.in), tc.want)
		})
	}
}

func TestFilterByText(t *testing.T) {
	apps := []App{
		{Name: "Messages", BundleID: "com.apple.MobileSMS"},
		{Name: "Finder", BundleID: "com.apple.finder"},
		{Name: "Music", BundleID: "com.apple.Music"},
	}
	cases := []struct {
		name      string
		query     string
		wantCount int
		wantFirst string // empty = don't assert
	}{
		{"empty-passes-through", "", 3, "Messages"},
		{"whitespace-trimmed", "   ", 3, "Messages"},
		{"case-insensitive-name", "MUSIC", 1, "Music"},
		{"matches-bundle-id", "MobileSMS", 1, "Messages"},
		{"partial-match", "fin", 1, "Finder"},
		{"no-match", "zzz", 0, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := FilterByText(apps, tc.query)
			testutil.Equal(t, len(got), tc.wantCount)
			if tc.wantFirst != "" {
				testutil.Equal(t, got[0].Name, tc.wantFirst)
			}
		})
	}
}

func TestApp_Format(t *testing.T) {
	a := App{Name: "Messages", BundleID: "com.apple.MobileSMS"}
	testutil.Equal(t, a.Format(), "Messages — com.apple.MobileSMS")
}

func TestParseBundle_InvalidPlistReturnsNotOK(t *testing.T) {
	root := t.TempDir()
	bundle := filepath.Join(root, "Garbage.app")
	contents := filepath.Join(bundle, "Contents")
	testutil.NoError(t, os.MkdirAll(contents, 0o755))
	// Not a valid plist at all.
	testutil.NoError(t, os.WriteFile(filepath.Join(contents, "Info.plist"), []byte("not a plist"), 0o644))
	apps := Scan([]string{root})
	testutil.Equal(t, len(apps), 0)
}

// TestScan_RealMacApplications is a smoke test for the actual /Applications
// directory on macOS hosts — exercises the real plist parser against real
// Info.plist files (XML and binary). Skips when /System/Applications is
// missing (Linux CI, etc.). Sanity-checks that Messages.app is discovered
// under its binary identifier so the picker's behavior matches user
// expectations and the gotcha docs remain accurate.
func TestScan_RealMacApplications(t *testing.T) {
	if _, err := os.Stat("/System/Applications/Messages.app"); err != nil {
		t.Skip("Messages.app not present; not on macOS")
	}
	apps := Scan([]string{"/System/Applications"})
	if len(apps) < 10 {
		t.Fatalf("expected at least 10 system apps, got %d", len(apps))
	}
	var messages *App
	for i := range apps {
		if apps[i].BundleID == "com.apple.MobileSMS" {
			messages = &apps[i]
			break
		}
	}
	if messages == nil {
		t.Fatal("Messages.app (com.apple.MobileSMS) not found among system apps — scanner regression")
	}
	testutil.Equal(t, messages.Scriptable, true)
}
