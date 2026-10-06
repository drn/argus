// Package artifacts resolves registered artifact files inside their task directory.
package artifacts

import (
	"path/filepath"
	"strings"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/model"
)

// ResolvePath returns the real path of a file inside the task's artifact
// directory; filename is a slash-separated relative path ("x.png" or
// "folder/x.png"). Callers must first look up the filename in the task manifest.
func ResolvePath(taskID, filename string) (string, bool) {
	if _, err := model.ValidateArtifactRelPath(filename); err != nil {
		return "", false
	}
	dir := agent.ArtifactsDir(taskID)
	full := filepath.Join(dir, filepath.FromSlash(filename))
	cleanDir := filepath.Clean(dir)
	if !strings.HasPrefix(full, cleanDir+string(filepath.Separator)) {
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
