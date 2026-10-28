package agent

import (
	"path/filepath"
	"strings"
)

const defaultSpawnPath = "/usr/bin:/bin:/usr/sbin:/sbin"

// ensureLocalBinOnPath appends ~/.local/bin to the PATH entry when it is not
// already present, so every spawned session (new, resumed, recycled) can run
// user-installed tools regardless of how the daemon or supervisor was started.
// Appended rather than prepended so an inherited PATH ordering is never changed.
func ensureLocalBinOnPath(env []string, home string) []string {
	if home == "" {
		return env
	}
	localBin := filepath.Join(home, ".local", "bin")
	for i, kv := range env {
		val, ok := strings.CutPrefix(kv, "PATH=")
		if !ok {
			continue
		}
		for _, dir := range filepath.SplitList(val) {
			if dir == localBin {
				return env
			}
		}
		out := append([]string(nil), env...)
		if val == "" {
			out[i] = "PATH=" + localBin
		} else {
			out[i] = "PATH=" + val + ":" + localBin
		}
		return out
	}
	return append(env, "PATH="+localBin+":"+defaultSpawnPath)
}
