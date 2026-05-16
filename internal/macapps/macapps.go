// Package macapps discovers installed macOS applications by scanning the
// standard app directories (/Applications, /System/Applications, ~/Applications)
// and reading each bundle's Info.plist for identity and scriptability metadata.
//
// The primary consumer is the Argus TUI's AppleEvents allowlist picker, which
// needs to surface the subset of apps that can be scripted via osascript so
// the user can pick CFBundleIdentifiers for the per-project SBPL
// AllowAppleEvents allowlist without typing them by hand.
//
// Pure Go (howett.net/plist) — no shelling to plutil.
//
// Gotcha: an app's binary CFBundleIdentifier is not always the same as the
// identifier its AppleEvent target is registered under in LaunchServices.
// Messages.app is the canonical example: CFBundleIdentifier="com.apple.MobileSMS"
// but the AppleEvent target the SBPL allow rule must reference is the legacy
// alias "com.apple.iChat" (preserved from the iChat→Messages rename, also the
// key TCC uses for Automation grants). A file-scanner cannot discover legacy
// aliases — picker UIs that consume this package should also accept manual
// bundle-ID entry for that case.
package macapps

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"howett.net/plist"
)

// App is one discovered application.
type App struct {
	// Name is the human-facing display name. CFBundleDisplayName when set,
	// otherwise CFBundleName, otherwise the .app directory basename without
	// the extension.
	Name string
	// BundleID is the CFBundleIdentifier from Info.plist.
	BundleID string
	// Path is the absolute path to the .app bundle.
	Path string
	// Scriptable is true when the app declares AppleScript support via
	// NSAppleScriptEnabled=true OR an OSAScriptingDefinition Info.plist key
	// OR a .sdef file in Contents/Resources/.
	Scriptable bool
}

// DefaultDirs are the standard search roots for installed apps. The expansion
// of ~ to $HOME happens inside Scan so an empty HOME (test environments) doesn't
// panic.
var DefaultDirs = []string{
	"/Applications",
	"/System/Applications",
	"~/Applications",
}

// Scan walks dirs (defaulting to DefaultDirs if dirs is empty) and returns
// every .app bundle it can parse. Result is sorted by lowercase Name. Errors
// reading individual bundles are skipped silently — a malformed Info.plist
// in one app must not break discovery of every other app. Returns the result
// slice; never nil even when nothing is found.
func Scan(dirs []string) []App {
	if len(dirs) == 0 {
		dirs = DefaultDirs
	}

	home, _ := os.UserHomeDir()
	seen := make(map[string]struct{}) // dedupe by bundle ID
	var apps []App

	for _, dir := range dirs {
		dir = expandHome(strings.TrimSpace(dir), home)
		if dir == "" {
			continue
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			name := e.Name()
			if !strings.HasSuffix(name, ".app") {
				continue
			}
			app, ok := parseBundle(filepath.Join(dir, name))
			if !ok {
				continue
			}
			if _, dup := seen[app.BundleID]; dup {
				continue
			}
			seen[app.BundleID] = struct{}{}
			apps = append(apps, app)
		}
	}

	sort.Slice(apps, func(i, j int) bool {
		return strings.ToLower(apps[i].Name) < strings.ToLower(apps[j].Name)
	})
	return apps
}

// ScanScriptable is a convenience wrapper that returns only Scriptable=true
// entries. This is what the AppleEvents picker uses by default — surfacing
// every installed app is noisy because most aren't scriptable, and an
// SBPL allow rule for a non-scriptable app is a no-op at runtime.
func ScanScriptable(dirs []string) []App {
	all := Scan(dirs)
	out := all[:0] // reuse backing array; we shrink-in-place
	for _, a := range all {
		if a.Scriptable {
			out = append(out, a)
		}
	}
	return out
}

// parseBundle reads the bundle's Info.plist and a few Resources files to
// build an App entry. Returns ok=false when the bundle has no readable
// Info.plist or no CFBundleIdentifier (both are required — an unidentified
// bundle is useless for our allowlist purpose).
func parseBundle(path string) (App, bool) {
	infoPath := filepath.Join(path, "Contents", "Info.plist")
	data, err := os.ReadFile(infoPath) //nolint:gosec // G304: path derived from directory scan of allowlisted roots
	if err != nil {
		return App{}, false
	}

	var info map[string]any
	if _, err := plist.Unmarshal(data, &info); err != nil {
		return App{}, false
	}

	bundleID, _ := info["CFBundleIdentifier"].(string)
	bundleID = strings.TrimSpace(bundleID)
	if bundleID == "" {
		return App{}, false
	}

	name, _ := info["CFBundleDisplayName"].(string)
	if name == "" {
		name, _ = info["CFBundleName"].(string)
	}
	if name == "" {
		name = strings.TrimSuffix(filepath.Base(path), ".app")
	}

	return App{
		Name:       name,
		BundleID:   bundleID,
		Path:       path,
		Scriptable: isScriptable(info, path),
	}, true
}

// isScriptable returns true when the app declares any AppleScript support.
// Three independent signals — any one is sufficient because Apple has used
// all three idioms across macOS versions:
//   - NSAppleScriptEnabled boolean=true
//   - OSAScriptingDefinition key (string path to a .sdef relative to Resources/)
//   - A .sdef file exists in Contents/Resources/ even when the key is absent
//     (older apps and some Apple system apps register scripting via the
//     filename convention rather than the explicit Info.plist key)
func isScriptable(info map[string]any, bundlePath string) bool {
	if v, ok := info["NSAppleScriptEnabled"].(bool); ok && v {
		return true
	}
	// Some apps store the boolean as a string "Yes"/"YES"/"true" — Apple's
	// own Finder is in this category as of macOS 14.
	if s, ok := info["NSAppleScriptEnabled"].(string); ok {
		switch strings.ToLower(strings.TrimSpace(s)) {
		case "true", "yes":
			return true
		}
	}
	if s, ok := info["OSAScriptingDefinition"].(string); ok && strings.TrimSpace(s) != "" {
		return true
	}
	resources := filepath.Join(bundlePath, "Contents", "Resources")
	entries, err := os.ReadDir(resources)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".sdef") {
			return true
		}
	}
	return false
}

// expandHome replaces a leading "~/" with the homeDir argument. Returns the
// path unchanged when homeDir is empty (test environments) or the prefix
// doesn't apply.
func expandHome(path, homeDir string) string {
	if homeDir == "" {
		// In tests with HOME unset, a path of literally "~/..." is unresolvable;
		// returning it unchanged means ReadDir will fail and we skip it cleanly.
		return path
	}
	if strings.HasPrefix(path, "~/") {
		return filepath.Join(homeDir, path[2:])
	}
	if path == "~" {
		return homeDir
	}
	return path
}

// BundleIDRe matches a syntactically valid CFBundleIdentifier — identical
// charset to agent.IsValidBundleID. Picker UIs use this to detect when a
// filter input is itself a valid bundle ID (legacy alias entry, e.g.
// "com.apple.iChat") that should be offered as a synthetic selection row
// even though no .app on disk has that identifier.
//
// Re-exposed here to keep the package self-contained — the picker imports
// macapps but should not need to import internal/agent just to validate
// a user-typed string against the same charset. The two regexes must stay
// in sync; tests in both packages exercise the same boundary cases.
func IsValidBundleID(s string) bool {
	if s == "" {
		return false
	}
	first := s[0]
	if !isAlnum(first) {
		return false
	}
	for i := 1; i < len(s); i++ {
		c := s[i]
		if !isAlnum(c) && c != '.' && c != '-' {
			return false
		}
	}
	return true
}

func isAlnum(b byte) bool {
	return (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z') || (b >= '0' && b <= '9')
}

// FilterByText returns apps whose Name or BundleID contains query as a
// case-insensitive substring. Empty query returns the input unchanged.
// Used by the picker UI; placed here so all the matching logic lives in
// one package and the picker stays thin.
func FilterByText(apps []App, query string) []App {
	query = strings.TrimSpace(strings.ToLower(query))
	if query == "" {
		return apps
	}
	out := make([]App, 0, len(apps))
	for _, a := range apps {
		if strings.Contains(strings.ToLower(a.Name), query) ||
			strings.Contains(strings.ToLower(a.BundleID), query) {
			out = append(out, a)
		}
	}
	return out
}

// Format renders an app as "Name — bundle.id" with an em-dash separator.
// Centralized so the picker, debug logging, and any future consumer agree.
func (a App) Format() string {
	return fmt.Sprintf("%s — %s", a.Name, a.BundleID)
}
