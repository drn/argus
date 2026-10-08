// Package sessiontag defines the per-spawn environment tag Argus stamps into
// every agent session so the session's whole process tree — including
// descendants that setsid'd away (Playwright browsers launch detached) or were
// reparented to launchd — can be found and reaped after the session ends.
//
// A tag has the form "<ownerPID>-<random>": the owner is the process that
// spawned (and reaps) the session. A startup sweep treats a tag whose owner is
// no longer alive as orphaned. See context/knowledge/gotchas/daemon-rpc.md.
package sessiontag

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"strconv"
	"strings"
)

// EnvKey is the environment variable carrying the tag.
const EnvKey = "ARGUS_SESSION_TAG"

// New mints a fresh tag owned by the current process.
func New() string {
	return NewForOwner(os.Getpid())
}

// NewForOwner mints a fresh tag owned by pid.
func NewForOwner(pid int) string {
	var b [8]byte
	_, _ = rand.Read(b[:]) // crypto/rand.Read never returns an error on supported platforms
	return strconv.Itoa(pid) + "-" + hex.EncodeToString(b[:])
}

// OwnerPID parses the owner PID out of a tag. ok is false for a malformed tag.
func OwnerPID(tag string) (pid int, ok bool) {
	head, _, found := strings.Cut(tag, "-")
	if !found {
		return 0, false
	}
	pid, err := strconv.Atoi(head)
	if err != nil || pid <= 0 {
		return 0, false
	}
	return pid, true
}

// StripEnv returns env with every EnvKey entry removed. Used both before
// stamping a fresh tag (an argus launched from inside an agent inherits that
// agent's tag) and by the daemon/supervisor auto-start forks, so a long-lived
// argus process is never tagged as part of an agent's tree.
func StripEnv(env []string) []string {
	out := make([]string, 0, len(env))
	prefix := EnvKey + "="
	for _, kv := range env {
		if strings.HasPrefix(kv, prefix) {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// WithTag returns env (or os.Environ() when env is nil, matching exec.Cmd's
// nil-Env semantics) with any existing tag replaced by tag.
func WithTag(env []string, tag string) []string {
	if env == nil {
		env = os.Environ()
	}
	return append(StripEnv(env), EnvKey+"="+tag)
}
