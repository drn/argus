package agent

import (
	"os"
	"path/filepath"
	"strings"
)

var sensitiveWritePaths = []string{
	"~/.ssh", "~/.aws", "~/.gnupg", "~/.config/gcloud", "~/.netrc",
	"~/Library/Keychains", "~/.argus", "~/.claude", "~/.codex",
}

var broadWriteRoots = []string{
	"~", "~/Library", "~/Library/Application Support", "/Users", "/Library", "/System",
	"/etc", "/var", "/usr", "/private", "/private/etc", "/private/var", "/Volumes",
}

// SandboxPathWarning returns a human-readable warning when path (literally or
// after resolving symlinks) is, contains, or lies inside a credential location
// or is a broad root, else "". It never rejects: sensitive grants are accepted
// but flagged for the approver. Matching is case-insensitive (APFS default).
func SandboxPathWarning(path string) string {
	p := strings.TrimRight(strings.TrimSpace(path), "/")
	if w := literalPathWarning(normalizeHomePath(p)); w != "" {
		return w
	}
	if resolved := resolveForWarning(p); resolved != "" && resolved != p {
		if w := literalPathWarning(normalizeHomePath(resolved)); w != "" {
			return "resolves to " + resolved + ", which " + w
		}
	}
	return ""
}

func literalPathWarning(p string) string {
	lp := strings.ToLower(p)
	for _, s := range sensitiveWritePaths {
		ls := strings.ToLower(s)
		if pathWithin(lp, ls) || pathWithin(ls, lp) {
			return "touches a sensitive location (" + s + ")"
		}
	}
	for _, b := range broadWriteRoots {
		if lp == strings.ToLower(b) {
			return "is an overly broad directory"
		}
	}
	return ""
}

// resolveForWarning expands ~ and resolves symlinks, returning "" when the
// path cannot be resolved (e.g. it does not exist yet).
func resolveForWarning(p string) string {
	abs := p
	if strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return ""
		}
		abs = filepath.Join(home, p[2:])
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return ""
	}
	return resolved
}

func pathWithin(child, parent string) bool {
	return child == parent || strings.HasPrefix(child, parent+"/")
}

// normalizeHomePath rewrites a path under $HOME (or its symlink-resolved form,
// e.g. /var vs /private/var on macOS) to its ~/ spelling.
func normalizeHomePath(p string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return p
	}
	homes := []string{home}
	if r, err := filepath.EvalSymlinks(home); err == nil && r != home {
		homes = append(homes, r)
	}
	for _, h := range homes {
		if strings.EqualFold(p, h) {
			return "~"
		}
		if len(p) > len(h) && strings.EqualFold(p[:len(h)+1], h+"/") {
			return "~" + p[len(h):]
		}
	}
	return p
}
