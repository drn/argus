package agent

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"

	"github.com/drn/argus/internal/config"
)

// sandboxExecPath is the canonical path to macOS sandbox-exec.
const sandboxExecPath = "/usr/bin/sandbox-exec"

var (
	sandboxOnce   sync.Once
	sandboxExists bool
)

// sandboxProfileBase is the base SBPL profile template for sandbox-exec.
// It denies everything by default and selectively allows what agents need.
const sandboxProfileBase = `(version 1)
(deny default)
(allow process*)
(allow signal)
(allow mach*)
(allow ipc*)
(allow sysctl*)
(allow system*)
(allow job-creation)
(allow network*)
(allow file-read*)
(deny file-read* (subpath (string-append (param "HOME") "/.ssh")))
(deny file-read* (subpath (string-append (param "HOME") "/.gnupg")))
(deny file-read* (subpath (string-append (param "HOME") "/.aws")))
(deny file-read* (subpath (string-append (param "HOME") "/.kube")))
(deny file-read* (subpath (string-append (param "HOME") "/.config/gcloud")))
(allow file-ioctl)
(allow file-write* (subpath (param "WORKTREE")))
(allow file-write* (subpath "/private/tmp"))
(allow file-write* (subpath "/tmp"))
(allow file-write* (literal "/dev/null"))
`

// IsSandboxAvailable checks whether sandbox-exec is available on this system.
// The result is cached after the first call.
func IsSandboxAvailable() bool {
	sandboxOnce.Do(func() {
		// Check canonical macOS path first
		if _, err := os.Stat(sandboxExecPath); err == nil {
			sandboxExists = true
			return
		}
		// Fallback: check PATH
		if _, err := exec.LookPath("sandbox-exec"); err == nil {
			sandboxExists = true
		}
	})
	return sandboxExists
}

// ResetSandboxCache clears the cached sandbox availability. For testing only.
func ResetSandboxCache() {
	sandboxOnce = sync.Once{}
	sandboxExists = false
}

// GenerateSandboxConfig creates a temporary SBPL profile file for a task.
// The worktreePath is granted write access. Custom deny/allow paths from cfg
// are appended to the base profile.
// Returns the profile path, params slice (HOME=..., WORKTREE=...), cleanup func, and error.
func GenerateSandboxConfig(worktreePath string, cfg config.Config) (string, []string, func(), error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", nil, nil, fmt.Errorf("getting home dir: %w", err)
	}

	var profile strings.Builder
	profile.WriteString(sandboxProfileBase)

	// Append user-configured deny read paths
	for _, p := range cfg.Sandbox.DenyRead {
		p = expandHomePath(strings.TrimSpace(p), homeDir)
		if p != "" {
			profile.WriteString(fmt.Sprintf("(deny file-read* (subpath %s))\n", sbplQuote(p)))
		}
	}

	// Append user-configured extra write paths
	for _, p := range cfg.Sandbox.ExtraWrite {
		p = expandHomePath(strings.TrimSpace(p), homeDir)
		if p != "" {
			profile.WriteString(fmt.Sprintf("(allow file-write* (subpath %s))\n", sbplQuote(p)))
		}
	}

	f, err := os.CreateTemp("", "argus-sandbox-*.sb")
	if err != nil {
		return "", nil, nil, err
	}
	path := f.Name()

	if _, err := f.WriteString(profile.String()); err != nil {
		f.Close()
		os.Remove(path)
		return "", nil, nil, err
	}
	f.Close()

	params := []string{
		"HOME=" + homeDir,
		"WORKTREE=" + worktreePath,
	}

	cleanup := func() {
		os.Remove(path)
	}

	return path, params, cleanup, nil
}

// WrapWithSandbox wraps cmdStr with the sandbox-exec invocation.
// params is a slice of "KEY=value" strings passed as -D flags.
func WrapWithSandbox(cmdStr, profilePath string, params []string) string {
	var b strings.Builder
	b.WriteString(sandboxExecPath)
	for _, p := range params {
		b.WriteString(" -D ")
		b.WriteString(shellQuote(p))
	}
	b.WriteString(" -f ")
	b.WriteString(shellQuote(profilePath))
	b.WriteString(" sh -c ")
	b.WriteString(shellQuote(cmdStr))
	return b.String()
}

// expandHomePath replaces a leading "~/" with the actual home directory.
func expandHomePath(p, homeDir string) string {
	if strings.HasPrefix(p, "~/") {
		return homeDir + p[1:]
	}
	return p
}

// sbplQuote wraps a string in SBPL double-quotes with minimal escaping.
func sbplQuote(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return `"` + s + `"`
}

