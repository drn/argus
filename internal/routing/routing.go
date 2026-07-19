// Package routing embeds argus's builtin hera/argus-task routing content — the
// injection-side counterpart to internal/skills' builtin skill bodies — and
// materializes it to a stable path under ~/.argus for Claude Code's
// --append-system-prompt-file flag.
package routing

import (
	"bytes"
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// builtinFS embeds argus's own routing-content sources — the single source of
// truth for the hera/argus-task orientation text injected into every spawned
// Claude backend session (the manual claude/snippets/*.md + install script
// path this superseded is retired).
//
//go:embed builtin
var builtinFS embed.FS

// builtinRoot is the embedded root directory name.
const builtinRoot = "builtin"

// managedWorkspace is the directory materialized routing content lives under,
// inside ~/.argus.
const managedWorkspace = "routing"

// systemPromptFilename is the materialized, concatenated routing content file
// passed to Claude Code's --append-system-prompt-file flag.
const systemPromptFilename = "system-prompt.md"

// BuiltinContent returns the concatenated content of argus's builtin routing
// snippets, sorted by filename for determinism. Reads only the embedded FS —
// no filesystem or network access.
func BuiltinContent() ([]byte, error) {
	entries, err := fs.ReadDir(builtinFS, builtinRoot)
	if err != nil {
		return nil, fmt.Errorf("read embedded builtin dir: %w", err)
	}

	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	var buf bytes.Buffer
	for i, name := range names {
		data, err := fs.ReadFile(builtinFS, filepath.Join(builtinRoot, name))
		if err != nil {
			return nil, fmt.Errorf("read embedded %s: %w", name, err)
		}
		if i > 0 {
			buf.WriteString("\n")
		}
		buf.Write(data)
	}
	return buf.Bytes(), nil
}

// EnsureBuiltinRouting materializes the embedded routing content to the
// managed workspace (~/.argus/routing/system-prompt.md) idempotently. Returns
// the materialized file's path on success — suitable for Claude Code's
// --append-system-prompt-file flag — or an error on failure. Errors are
// non-fatal for launch (callers log but continue). Returns ("", nil) when
// running under `go test`, mirroring skills.EnsureBuiltinSkills: dozens of
// BuildCmd tests assert exact command strings with no HOME override, so
// materializing a real, environment-dependent path here would break them.
func EnsureBuiltinRouting() (string, error) {
	if isTestBinary() {
		return "", nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("no home dir: %w", err)
	}
	return materialize(filepath.Join(home, ".argus", managedWorkspace))
}

// materialize writes the concatenated builtin routing content to
// <workspaceRoot>/system-prompt.md, touching disk only if the content
// differs from what's already there, and returns the file's path.
func materialize(workspaceRoot string) (string, error) {
	content, err := BuiltinContent()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(workspaceRoot, 0755); err != nil {
		return "", fmt.Errorf("create routing workspace dir: %w", err)
	}
	target := filepath.Join(workspaceRoot, systemPromptFilename)
	if err := atomicWriteIfDifferent(target, content); err != nil {
		return "", err
	}
	return target, nil
}

// atomicWriteIfDifferent writes data to path only if the current file content
// differs. Uses a temp file + rename pattern to avoid partial writes.
func atomicWriteIfDifferent(path string, data []byte) error {
	if current, err := os.ReadFile(path); err == nil && bytes.Equal(current, data) {
		return nil
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// isTestBinary returns true if the current process is a test executable.
func isTestBinary() bool {
	return strings.HasSuffix(os.Args[0], ".test")
}
