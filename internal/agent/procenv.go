package agent

import (
	"bytes"
	"encoding/binary"
	"strings"

	"github.com/drn/argus/internal/sessiontag"
)

// procArgs2Env extracts the environment block from a darwin KERN_PROCARGS2
// buffer: int32 argc, the exec path, NUL padding, argc NUL-terminated argv
// strings, then NUL-terminated env strings ending at an empty string. Returns
// nil for a truncated/malformed buffer.
func procArgs2Env(buf []byte) []string {
	if len(buf) < 4 {
		return nil
	}
	argc := int(binary.LittleEndian.Uint32(buf[:4]))
	rest := buf[4:]
	i := bytes.IndexByte(rest, 0)
	if i < 0 {
		return nil
	}
	rest = rest[i:]
	for len(rest) > 0 && rest[0] == 0 {
		rest = rest[1:]
	}
	for n := 0; n < argc; n++ {
		i = bytes.IndexByte(rest, 0)
		if i < 0 {
			return nil
		}
		rest = rest[i+1:]
	}
	return splitNULEnv(rest)
}

// splitNULEnv splits a NUL-separated env block (linux /proc/<pid>/environ, or
// the tail of a procargs2 buffer), stopping at the first empty entry.
func splitNULEnv(rest []byte) []string {
	var env []string
	for len(rest) > 0 {
		i := bytes.IndexByte(rest, 0)
		if i == 0 {
			break
		}
		if i < 0 {
			env = append(env, string(rest))
			break
		}
		env = append(env, string(rest[:i]))
		rest = rest[i+1:]
	}
	return env
}

// sessionTagFromEnv returns the value of the session tag entry, if present.
func sessionTagFromEnv(env []string) (string, bool) {
	prefix := sessiontag.EnvKey + "="
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, prefix); ok && v != "" {
			return v, true
		}
	}
	return "", false
}

// inheritAncestorTags assigns each untagged process the tag of its nearest
// tagged ancestor (via ppid), mutating tagged in place. Covers descendants
// whose env can't be read — macOS hides KERN_PROCARGS2 env for Apple platform
// binaries (/bin/sh, /bin/sleep) even to the same user — but which still sit
// under a readable tagged process. Cycle-safe via a bounded walk.
func inheritAncestorTags(ppid map[int]int, tagged map[int]string) {
	inherited := map[int]string{}
	for pid := range ppid {
		if _, ok := tagged[pid]; ok {
			continue
		}
		cur := pid
		for steps := 0; steps < len(ppid); steps++ {
			parent, ok := ppid[cur]
			if !ok || parent <= 1 {
				break
			}
			if tag, ok := tagged[parent]; ok {
				inherited[pid] = tag
				break
			}
			cur = parent
		}
	}
	for pid, tag := range inherited {
		tagged[pid] = tag
	}
}
