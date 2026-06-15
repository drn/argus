package daemon

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
)

// BinaryHashFile returns the hex-encoded SHA-256 of the file at path.
//
// It is the staleness signal the TUI compares against the daemon's boot-time
// hash. mtime is a poor signal: `go install` rewrites the binary (bumping its
// mtime) on every run, even when the source is unchanged and the resulting
// binary is byte-identical, because Go builds are deterministic. That produced
// spurious "Daemon out of date" prompts on every launch for anyone who
// reinstalls as part of their workflow. A content hash only differs when the
// code actually changed, so the prompt now fires only on a real rebuild.
func BinaryHashFile(path string) (string, error) {
	f, err := os.Open(path) //nolint:gosec // path is os.Executable(), not user input
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
