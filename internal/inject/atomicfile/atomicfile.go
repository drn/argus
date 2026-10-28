// Package atomicfile writes the agent CLI config files Argus edits
// (Claude, Codex, OpenCode, Antigravity) via a unique temp file plus rename,
// so a concurrent reader never sees a partial file and concurrent writers
// never clobber each other's temp file.
package atomicfile

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

// Write atomically replaces path with data, keeping an existing regular
// file's permission bits (new files get 0600). label prefixes every error
// (e.g. "inject codex") so callers keep their own error vocabulary. A
// symlink at path is replaced, never followed: a sandboxed agent can write
// these config dirs and must not be able to aim the daemon's write elsewhere.
func Write(path string, data []byte, label string) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".argus-*.tmp")
	if err != nil {
		return fmt.Errorf("%s: create temp: %w", label, err)
	}
	tmpName := tmp.Name()
	if info, err := os.Lstat(path); err == nil && info.Mode().IsRegular() {
		if err := tmp.Chmod(info.Mode().Perm()); err != nil {
			tmp.Close()        //nolint:errcheck
			os.Remove(tmpName) //nolint:errcheck
			return fmt.Errorf("%s: chmod temp: %w", label, err)
		}
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()        //nolint:errcheck
		os.Remove(tmpName) //nolint:errcheck
		return fmt.Errorf("%s: write temp: %w", label, err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName) //nolint:errcheck
		return fmt.Errorf("%s: close temp: %w", label, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName) //nolint:errcheck
		return fmt.Errorf("%s: rename: %w", label, err)
	}
	return nil
}

// WriteIfDifferent is Write that leaves the file untouched when it already
// holds exactly data.
func WriteIfDifferent(path string, data []byte, label string) error {
	if current, err := os.ReadFile(path); err == nil && bytes.Equal(current, data) {
		return nil
	}
	return Write(path, data, label)
}

// WriteJSON marshals v as two-space-indented JSON with a trailing newline and
// writes it atomically.
func WriteJSON(path string, v any, label string) error {
	out, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("%s: marshal: %w", label, err)
	}
	return Write(path, append(out, '\n'), label)
}

// maxJSONSize bounds ReadJSONObject so a planted oversized file can't exhaust
// daemon memory.
const maxJSONSize = 16 << 20

// ReadJSONObject reads path as a JSON object. A missing file yields an empty
// map; an unparseable, oversized, symlinked, or non-regular file (a FIFO
// planted by a sandboxed agent would otherwise block the daemon forever) is an
// error so callers leave the user's file untouched.
func ReadJSONObject(path, label string) (map[string]any, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0)
	if os.IsNotExist(err) {
		return map[string]any{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("%s: read %s: %w", label, path, err)
	}
	defer f.Close() //nolint:errcheck
	info, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("%s: read %s: %w", label, path, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s: read %s: not a regular file", label, path)
	}
	raw, err := io.ReadAll(io.LimitReader(f, maxJSONSize+1))
	if err != nil {
		return nil, fmt.Errorf("%s: read %s: %w", label, path, err)
	}
	if len(raw) > maxJSONSize {
		return nil, fmt.Errorf("%s: read %s: larger than %d bytes", label, path, maxJSONSize)
	}
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil, fmt.Errorf("%s: cannot parse %s: %w", label, path, err)
	}
	if data == nil {
		data = map[string]any{}
	}
	return data, nil
}
