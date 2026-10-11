// Package atomicfile writes the agent CLI config files Argus edits
// (Claude, Codex, OpenCode, Antigravity) via a unique temp file plus rename,
// so a concurrent reader never sees a partial file and concurrent writers
// never clobber each other's temp file.
package atomicfile

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Write atomically replaces path with data. label prefixes every error
// (e.g. "inject codex") so callers keep their own error vocabulary. A
// symlinked path (a dotfiles-managed config) is written through to its
// target so the link survives instead of being replaced by a regular file.
func Write(path string, data []byte, label string) error {
	if target, err := filepath.EvalSymlinks(path); err == nil {
		path = target
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".argus-*.tmp")
	if err != nil {
		return fmt.Errorf("%s: create temp: %w", label, err)
	}
	tmpName := tmp.Name()
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

// WriteJSON marshals v as two-space-indented JSON with a trailing newline and
// writes it atomically.
func WriteJSON(path string, v any, label string) error {
	out, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("%s: marshal: %w", label, err)
	}
	return Write(path, append(out, '\n'), label)
}

// ReadJSONObject reads path as a JSON object. A missing file yields an empty
// map; an unparseable file is an error so callers leave the user's file
// untouched.
func ReadJSONObject(path, label string) (map[string]any, error) {
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return map[string]any{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("%s: read %s: %w", label, path, err)
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
