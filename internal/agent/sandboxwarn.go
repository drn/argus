package agent

import (
	"os"
	"strings"
)

var sensitiveWritePaths = []string{
	"~/.ssh", "~/.aws", "~/.gnupg", "~/.config/gcloud", "~/.netrc",
	"~/Library/Keychains", "~/.argus", "~/.claude", "~/.codex",
}

var broadWriteRoots = []string{"~/Library", "/Users", "/Library", "/System", "/etc", "/var", "/usr"}

// SandboxPathWarning returns a human-readable warning when path is, contains,
// or lies inside a credential location or is a broad root, else "". It never
// rejects: sensitive grants are accepted but flagged for the approver.
func SandboxPathWarning(path string) string {
	p := normalizeHomePath(strings.TrimRight(strings.TrimSpace(path), "/"))
	for _, s := range sensitiveWritePaths {
		if pathWithin(p, s) || pathWithin(s, p) {
			return "touches a sensitive location (" + s + ")"
		}
	}
	for _, b := range broadWriteRoots {
		if p == b {
			return "overly broad directory"
		}
	}
	if p == "~" {
		return "overly broad directory"
	}
	return ""
}

func pathWithin(child, parent string) bool {
	return child == parent || strings.HasPrefix(child, parent+"/")
}

func normalizeHomePath(p string) string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if p == home {
			return "~"
		}
		if strings.HasPrefix(p, home+"/") {
			return "~" + strings.TrimPrefix(p, home)
		}
	}
	return p
}
