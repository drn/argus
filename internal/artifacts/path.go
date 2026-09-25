// Package artifacts resolves registered artifact files inside their task directory.
package artifacts

import (
	"path/filepath"
	"strings"

	"github.com/drn/argus/internal/agent"
)

// ResolvePath returns the real path of a direct child of the task's artifact
// directory. Callers must first look up the filename in the task manifest.
func ResolvePath(taskID, filename string) (string, bool) {
	dir := agent.ArtifactsDir(taskID)
	full := filepath.Join(dir, filename)
	cleanDir := filepath.Clean(dir)
	if full != filepath.Join(cleanDir, filepath.Base(filename)) ||
		!strings.HasPrefix(full, cleanDir+string(filepath.Separator)) {
		return "", false
	}
	realPath, err := filepath.EvalSymlinks(full)
	if err != nil {
		return full, true // missing bytes are reported by os.Open
	}
	realDir, err := filepath.EvalSymlinks(cleanDir)
	if err != nil || (realPath != realDir && !strings.HasPrefix(realPath, realDir+string(filepath.Separator))) {
		return "", false
	}
	return realPath, true
}
