package agent

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/drn/argus/internal/config"
)

// srtDetectTimeout is the maximum time to wait for npx to check srt availability.
const srtDetectTimeout = 5 * time.Second

// srtBinary is the resolved path to the sandbox-runtime binary.
// Cached after the first successful lookup.
var (
	srtOnce   sync.Once
	srtPath   string
	srtExists bool
)

// defaultDenyRead lists credential directories that are always blocked.
var defaultDenyRead = []string{
	"~/.ssh",
	"~/.gnupg",
	"~/.aws",
	"~/.kube",
	"~/.config/gcloud",
}

// defaultAllowedDomains lists domains needed for Claude Code to function.
var defaultAllowedDomains = []string{
	"api.anthropic.com",
	"statsig.anthropic.com",
	"sentry.io",
}

// srtSettings mirrors the JSON structure expected by @anthropic-ai/sandbox-runtime v1.0.0.
// Fields are top-level (not nested under "sandbox").
type srtSettings struct {
	Network    srtNetwork    `json:"network"`
	Filesystem srtFilesystem `json:"filesystem"`
	AllowPty   bool          `json:"allowPty"`
}

type srtFilesystem struct {
	AllowWrite []string `json:"allowWrite"`
	DenyRead   []string `json:"denyRead"`
	DenyWrite  []string `json:"denyWrite"`
}

type srtNetwork struct {
	AllowedDomains []string `json:"allowedDomains"`
	DeniedDomains  []string `json:"deniedDomains"`
}

// IsSandboxAvailable checks whether the sandbox-runtime (srt) binary is installed.
// The result is cached after the first call. Only checks locally installed binaries —
// does not download or install anything.
func IsSandboxAvailable() bool {
	srtOnce.Do(func() {
		// Try direct binary first (fastest)
		if p, err := exec.LookPath("srt"); err == nil {
			srtPath = p
			srtExists = true
			return
		}
		// Try npx resolution with a short timeout — does NOT auto-install (no --yes).
		// This only succeeds if the package is already in the local npx cache.
		ctx, cancel := context.WithTimeout(context.Background(), srtDetectTimeout)
		defer cancel()
		out, err := exec.CommandContext(ctx, "npx", "--no", "@anthropic-ai/sandbox-runtime", "--version").CombinedOutput()
		if err == nil && len(out) > 0 {
			srtPath = "npx"
			srtExists = true
		}
	})
	return srtExists
}

// ResetSandboxCache clears the cached sandbox availability. For testing only.
func ResetSandboxCache() {
	srtOnce = sync.Once{}
	srtPath = ""
	srtExists = false
}

// GenerateSandboxConfig creates a temporary srt settings file for a task.
// The worktreePath is granted write access. The projectDir (if set) is read-only.
// Returns the path to the temp file and a cleanup function.
func GenerateSandboxConfig(worktreePath string, cfg config.Config) (string, func(), error) {
	// Build allowWrite: worktree + /tmp + any user-configured extra paths.
	// srt uses "//" prefix for absolute paths (e.g., "//home/user/wt" = /home/user/wt).
	allowWrite := []string{
		normalizeSrtPath(worktreePath),
		"//tmp",
	}
	for _, p := range cfg.Sandbox.ExtraWrite {
		allowWrite = append(allowWrite, normalizeSrtPath(p))
	}

	// Build denyRead: defaults + any user-configured deny paths
	denyRead := make([]string, len(defaultDenyRead))
	copy(denyRead, defaultDenyRead)
	for _, p := range cfg.Sandbox.DenyRead {
		denyRead = append(denyRead, normalizeSrtPath(p))
	}

	// Build allowedDomains: defaults + user-configured
	domains := make([]string, len(defaultAllowedDomains))
	copy(domains, defaultAllowedDomains)
	for _, d := range cfg.Sandbox.AllowedDomains {
		d = strings.TrimSpace(d)
		if d != "" && !containsString(domains, d) {
			domains = append(domains, d)
		}
	}

	settings := srtSettings{
		Filesystem: srtFilesystem{
			AllowWrite: allowWrite,
			DenyRead:   denyRead,
			DenyWrite:  []string{},
		},
		Network: srtNetwork{
			AllowedDomains: domains,
			DeniedDomains:  []string{},
		},
		AllowPty: true,
	}

	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return "", nil, err
	}

	f, err := os.CreateTemp("", "argus-sandbox-*.json")
	if err != nil {
		return "", nil, err
	}
	path := f.Name()

	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(path)
		return "", nil, err
	}
	f.Close()

	cleanup := func() {
		os.Remove(path)
	}

	return path, cleanup, nil
}

// WrapWithSandbox prepends the srt command to the given command string.
func WrapWithSandbox(cmdStr, settingsPath string) string {
	if srtPath == "npx" {
		return "npx @anthropic-ai/sandbox-runtime --settings " + shellQuote(settingsPath) + " -- " + cmdStr
	}
	return shellQuote(srtPath) + " --settings " + shellQuote(settingsPath) + " -- " + cmdStr
}

// normalizeSrtPath converts a path to srt's path format.
// Absolute paths get "//" prefix (e.g., /home/user → //home/user).
// "~/" paths stay as-is. Already-prefixed "//" paths are unchanged.
func normalizeSrtPath(p string) string {
	p = strings.TrimSpace(p)
	if strings.HasPrefix(p, "~/") || strings.HasPrefix(p, "//") {
		return p
	}
	if strings.HasPrefix(p, "/") {
		// Strip leading "/" so "/home/user" becomes "//home/user" (not "///home/user")
		return "//" + strings.TrimPrefix(p, "/")
	}
	return p
}

func containsString(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}
