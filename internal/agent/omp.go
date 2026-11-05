package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/mcplimits"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/uxlog"
)

// ompSessionsDir returns ~/.omp/agent/sessions, omp's default session store.
func ompSessionsDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("omp sessions dir: home dir: %w", err)
	}
	return filepath.Join(home, ".omp", "agent", "sessions"), nil
}

var ompPathSepReplacer = strings.NewReplacer("/", "-", "\\", "-", ":", "-")

// ompRelInside mirrors omp's containment test: rel is "" or neither starts
// with ".." nor is absolute.
func ompRelInside(base, target string) (string, bool) {
	rel, err := filepath.Rel(base, target)
	if err != nil {
		return "", false
	}
	if rel == "." {
		rel = ""
	}
	if rel != "" && (strings.HasPrefix(rel, "..") || filepath.IsAbs(rel)) {
		return "", false
	}
	return rel, true
}

// ompJoinEncoded mirrors omp's prefix join: "-tmp" + "a/b" → "-tmp-a-b",
// "-" + "a/b" → "-a-b", and an empty rel leaves the prefix alone.
func ompJoinEncoded(prefix, rel string) string {
	enc := ompPathSepReplacer.Replace(rel)
	if enc == "" {
		return prefix
	}
	if strings.HasSuffix(prefix, "-") {
		return prefix + enc
	}
	return prefix + "-" + enc
}

// ompAbsEncoded is Pi's legacy `--<abs>--` session dir name, which omp still
// uses for a cwd outside both $HOME and the temp dir.
func ompAbsEncoded(resolved string) string {
	trimmed := resolved
	if len(trimmed) > 0 && (trimmed[0] == '/' || trimmed[0] == '\\') {
		trimmed = trimmed[1:]
	}
	return "--" + ompPathSepReplacer.Replace(trimmed) + "--"
}

// ompEncodeCwd mirrors omp's session-dir naming (temp dir first, then $HOME,
// else absolute) on realpaths, as omp computes it.
func ompEncodeCwd(cwd, home, tmp string) string {
	resolved := canonPath(cwd)
	if tmp != "" {
		if rel, ok := ompRelInside(canonPath(tmp), resolved); ok {
			return ompJoinEncoded("-tmp", rel)
		}
	}
	if home != "" {
		if rel, ok := ompRelInside(canonPath(home), resolved); ok {
			return ompJoinEncoded("-", rel)
		}
	}
	return ompAbsEncoded(resolved)
}

// CaptureOmpSessionID returns the UUID of the newest top-level session file
// in the worktree's omp session directory. Subagent subdirectories and lock
// files are skipped; the legacy absolute encoding is tried as a fallback.
func CaptureOmpSessionID(worktreePath string) (string, error) {
	if worktreePath == "" {
		return "", fmt.Errorf("CaptureOmpSessionID: worktree path is empty")
	}
	sessions, err := ompSessionsDir()
	if err != nil {
		return "", err
	}
	home, _ := os.UserHomeDir()
	tmp := os.TempDir()
	var dirs []string
	for _, p := range []string{worktreePath, filepath.Clean(worktreePath)} {
		for _, name := range []string{ompEncodeCwd(p, home, tmp), ompAbsEncoded(canonPath(p))} {
			d := filepath.Join(sessions, name)
			if !slices.Contains(dirs, d) {
				dirs = append(dirs, d)
			}
		}
	}
	var bestID string
	var bestMod int64
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() || strings.HasPrefix(e.Name(), ".") {
				continue
			}
			m := piSessionFileRe.FindStringSubmatch(e.Name())
			if m == nil {
				continue
			}
			info, err := e.Info()
			if err != nil {
				continue
			}
			if mod := info.ModTime().UnixNano(); bestID == "" || mod > bestMod {
				bestID, bestMod = m[1], mod
			}
		}
	}
	if bestID == "" {
		return "", fmt.Errorf("CaptureOmpSessionID: no session files for worktree %s", worktreePath)
	}
	return bestID, nil
}

func captureOmp(task *model.Task, _ config.Config) (string, error) {
	return CaptureOmpSessionID(task.Worktree)
}

// ompPermissionFlags maps Argus's permission modes onto omp's --approval-mode.
// omp has no plan-without-model-switch mode, so plan modes ask before writes.
func ompPermissionFlags(mode string) string {
	switch mode {
	case config.PermissionModeAcceptEdits:
		return "--approval-mode write"
	case config.PermissionModePlan, config.PermissionModeBypassAllow:
		return "--approval-mode always-ask"
	case config.PermissionModeBypassActive:
		return "--approval-mode yolo"
	default:
		return ""
	}
}

const ompCompleteMarker = ".argus-complete"

// materializeContentAddressed writes files into <root>/<hash>/ (hash over
// sorted name+content) via a renamed temp dir, so concurrent launches see all
// of it or none. An existing complete hash dir is reused, never removed.
func materializeContentAddressed(root string, files map[string][]byte) (string, error) {
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
	hashDir := filepath.Join(root, hex.EncodeToString(h.Sum(nil))[:16])
	if _, err := os.Stat(filepath.Join(hashDir, ompCompleteMarker)); err == nil {
		return hashDir, nil
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", err
	}
	tmp, err := os.MkdirTemp(root, ".tmp-")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	for _, n := range names {
		dst := filepath.Join(tmp, filepath.FromSlash(n))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(dst, files[n], 0o644); err != nil {
			return "", err
		}
	}
	if err := os.WriteFile(filepath.Join(tmp, ompCompleteMarker), nil, 0o644); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, hashDir); err != nil {
		if _, serr := os.Stat(filepath.Join(hashDir, ompCompleteMarker)); serr == nil {
			return hashDir, nil
		}
		return "", err
	}
	return hashDir, nil
}

func ompCacheRoot(kind string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("no home dir: %w", err)
	}
	return filepath.Join(home, ".argus", "cache", kind), nil
}

// MaterializeOmpMCPPlugin writes a Claude-style plugin declaring Argus's MCP
// server under root and returns the directory to pass to --plugin-dir.
func MaterializeOmpMCPPlugin(root, url string) (string, error) {
	manifest, err := json.MarshalIndent(map[string]any{"name": "argus", "version": "1.0.0", "description": "Argus MCP server"}, "", "  ")
	if err != nil {
		return "", err
	}
	servers, err := json.MarshalIndent(map[string]any{"mcpServers": map[string]any{
		"argus": map[string]any{"type": "http", "url": url, "timeout": mcplimits.ClientTimeoutMilliseconds},
	}}, "", "  ")
	if err != nil {
		return "", err
	}
	return materializeContentAddressed(root, map[string][]byte{
		".claude-plugin/plugin.json": manifest,
		".mcp.json":                  servers,
	})
}

// MaterializeOmpConfigOverlay writes a config.yml overlay pointing omp's skill
// discovery at skillsDir and returns the file to pass to --config.
func MaterializeOmpConfigOverlay(root, skillsDir string) (string, error) {
	body := "skills:\n  customDirectories:\n    - " + strconv.Quote(skillsDir) + "\n"
	dir, err := materializeContentAddressed(root, map[string][]byte{"argus.yml": []byte(body)})
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "argus.yml"), nil
}

// ensureOmpMCPPluginFn / ensureOmpConfigOverlayFn are inert under `go test`
// (like skills.EnsureBuiltinSkills) so BuildCmd tests never write the real
// ~/.argus; tests swap them.
var ensureOmpMCPPluginFn = func(url string) (string, error) {
	if isTestBinary() {
		return "", nil
	}
	root, err := ompCacheRoot("omp-mcp")
	if err != nil {
		return "", err
	}
	return MaterializeOmpMCPPlugin(root, url)
}

var ensureOmpConfigOverlayFn = func(skillsDir string) (string, error) {
	if isTestBinary() {
		return "", nil
	}
	root, err := ompCacheRoot("omp-config")
	if err != nil {
		return "", err
	}
	return MaterializeOmpConfigOverlay(root, skillsDir)
}

func ompMCPFlag(url string) string {
	dir, err := ensureOmpMCPPluginFn(url)
	if err != nil {
		uxlog.Log("[omp] MCP plugin materialize failed (continuing without Argus MCP): %v", err)
		slog.Warn("[omp] MCP plugin materialize failed (continuing without Argus MCP)", "err", err)
		return ""
	}
	if dir == "" {
		return ""
	}
	return " --plugin-dir " + shellQuote(dir)
}
