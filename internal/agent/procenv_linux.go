//go:build linux

package agent

import (
	"os"
	"path/filepath"
	"strconv"
)

// listTaggedProcs returns pid → session tag for every readable process whose
// environment carries a session tag (/proc/<pid>/environ is only readable for
// same-user processes; others are skipped).
func listTaggedProcs() (map[int]string, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	out := map[int]string{}
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid <= 0 {
			continue
		}
		buf, err := os.ReadFile(filepath.Join("/proc", e.Name(), "environ"))
		if err != nil {
			continue
		}
		if tag, ok := sessionTagFromEnv(splitNULEnv(buf)); ok {
			out[pid] = tag
		}
	}
	return out, nil
}
