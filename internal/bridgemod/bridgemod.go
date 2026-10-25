// Package bridgemod embeds the argus-bridge Claude Code mod (source under
// mod/argus-bridge, a TypeScript plugin) and materializes it to a
// content-addressed directory under ~/.argus/cache for Claude's --plugin-dir flag.
// The mod reports a session's state to the daemon as structured events; see
// openspec/specs/argus-bridge.
package bridgemod

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// modFS embeds ONLY the runtime files, by explicit pattern: a directory
// pattern would skip the dot-directory .claude-plugin/ (and, if made
// recursive with `all:`, would try to embed the gitignored
// .claude-plugin/types symlink and node_modules). The tests/ and tsconfig.json
// development files are deliberately absent.
//
//go:embed mod/argus-bridge/.claude-plugin/plugin.json mod/argus-bridge/hooks/hooks.json mod/argus-bridge/hooks/*.ts
var modFS embed.FS

const (
	embedRoot = "mod/argus-bridge"
	// workspace is the directory (relative to ~/.argus) materialized mods live
	// in. Under cache/ on purpose: a sandboxed session may write only
	// ~/.argus/cache, and Claude's hot-reload writes .claude-plugin/types/ into
	// every --plugin-dir folder it loads, so a read-only location would make
	// that write fail inside the sandbox.
	workspace = "cache/bridge-mod"
	// modDirName is the directory --plugin-dir points at; its name is the
	// plugin name's folder so the loaded plugin is identified as argus-bridge.
	modDirName = "argus-bridge"
	// completeMarker is written last; a hash directory without it is a
	// half-written one and is rebuilt.
	completeMarker = ".complete"
)

// Files returns the embedded files keyed by their path relative to the mod
// root (e.g. "hooks/register.ts").
func Files() (map[string][]byte, error) {
	out := map[string][]byte{}
	err := fs.WalkDir(modFS, embedRoot, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := modFS.ReadFile(p)
		if err != nil {
			return err
		}
		out[strings.TrimPrefix(p, embedRoot+"/")] = data
		return nil
	})
	return out, err
}

// Hash is the content hash of the embedded mod (first 16 hex chars of the
// sha256 over sorted path+content), the name of its materialized directory.
func Hash() (string, error) {
	files, err := Files()
	if err != nil {
		return "", err
	}
	return hashFiles(files), nil
}

func hashFiles(files map[string][]byte) string {
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	h := sha256.New()
	for _, n := range names {
		_, _ = fmt.Fprintf(h, "%s\x00%d\x00", n, len(files[n]))
		h.Write(files[n])
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// EnsureBridgeMod materializes the embedded mod under ~/.argus/cache/bridge-mod
// idempotently and returns the directory to pass to --plugin-dir. A new binary
// with different mod content gets a new directory, so a running session's
// hot-reloading Claude never sees a directory change under it. Returns
// ("", nil) under `go test`, mirroring skills.EnsureBuiltinSkills, so BuildCmd
// tests never depend on or write to the real ~/.argus.
func EnsureBridgeMod() (string, error) {
	if isTestBinary() {
		return "", nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("no home dir: %w", err)
	}
	return Materialize(filepath.Join(home, ".argus", filepath.FromSlash(workspace)))
}

// Materialize writes the embedded files to <root>/<hash>/argus-bridge and
// returns that directory. The write goes to a sibling temp directory that is
// renamed into place, so a concurrent launch sees all of the mod or none of it.
func Materialize(root string) (string, error) {
	files, err := Files()
	if err != nil {
		return "", fmt.Errorf("read embedded bridge mod: %w", err)
	}
	hashDir := filepath.Join(root, hashFiles(files))
	target := filepath.Join(hashDir, modDirName)
	if _, err := os.Stat(filepath.Join(hashDir, completeMarker)); err == nil {
		return target, nil
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", fmt.Errorf("create bridge mod workspace: %w", err)
	}
	tmp, err := os.MkdirTemp(root, ".tmp-")
	if err != nil {
		return "", fmt.Errorf("create temp bridge mod dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	for name, data := range files {
		dst := filepath.Join(tmp, modDirName, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(dst, data, 0o644); err != nil {
			return "", err
		}
	}
	if err := os.WriteFile(filepath.Join(tmp, completeMarker), nil, 0o644); err != nil {
		return "", err
	}
	// The marker was written into tmp before this rename, so a hash directory
	// without it cannot exist: never remove an existing one. Two launches racing
	// to the same hash would otherwise delete the directory the other just
	// returned (reproduced: callers handed a path missing hooks/register.ts).
	if err := os.Rename(tmp, hashDir); err != nil {
		// Lost the race: the winner's copy is identical (same hash), so use it.
		if _, serr := os.Stat(filepath.Join(hashDir, completeMarker)); serr == nil {
			return target, nil
		}
		return "", fmt.Errorf("install bridge mod: %w", err)
	}
	return target, nil
}

// isTestBinary reports whether the current process is a Go test executable.
func isTestBinary() bool {
	return strings.HasSuffix(path.Base(filepath.ToSlash(os.Args[0])), ".test")
}
