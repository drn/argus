package agent

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/mcplimits"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/uxlog"
)

const (
	ompMCPCacheKind    = "omp-mcp"
	ompConfigCacheKind = "omp-config"
	ompPluginVersion   = "1.0.0"
	ompOverlayFile     = "argus.yml"
)

func ompSessionsDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("omp sessions dir: home dir: %w", err)
	}
	return filepath.Join(home, ".omp", "agent", "sessions"), nil
}

var ompPathSepReplacer = strings.NewReplacer("/", "-", "\\", "-", ":", "-")

// ompRelInside mirrors omp's own test, which also rejects names like "..foo".
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

// ompAbsEncoded is Pi's `--<abs>--` name, still used outside $HOME and the temp dir.
func ompAbsEncoded(resolved string) string {
	trimmed := resolved
	if len(trimmed) > 0 && (trimmed[0] == '/' || trimmed[0] == '\\') {
		trimmed = trimmed[1:]
	}
	return "--" + ompPathSepReplacer.Replace(trimmed) + "--"
}

// ompEncodeCwd mirrors omp's session-dir naming; the temp dir outranks $HOME.
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

// CaptureOmpSessionID also checks the absolute encoding, which omp migrates from.
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
	dirs := []string{filepath.Join(sessions, ompEncodeCwd(worktreePath, home, tmp))}
	if abs := filepath.Join(sessions, ompAbsEncoded(canonPath(worktreePath))); abs != dirs[0] {
		dirs = append(dirs, abs)
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

// ompPermissionFlags asks explicitly for default and plan modes: omp's own default is yolo.
func ompPermissionFlags(mode string) string {
	switch mode {
	case config.PermissionModeAcceptEdits:
		return "--approval-mode write"
	case config.PermissionModeDefault, config.PermissionModePlan, config.PermissionModeBypassAllow:
		return "--approval-mode always-ask"
	case config.PermissionModeBypassActive:
		return "--approval-mode yolo"
	default:
		return ""
	}
}

// MaterializeOmpMCPPlugin returns a --plugin-dir plugin whose .mcp.json declares Argus's server.
func MaterializeOmpMCPPlugin(root, url string) (string, error) {
	manifest, err := json.MarshalIndent(map[string]any{"name": "argus", "version": ompPluginVersion, "description": "Argus MCP server"}, "", "  ")
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

// MaterializeOmpConfigOverlay returns a --config overlay adding skillsDir to omp's skill dirs.
func MaterializeOmpConfigOverlay(root, skillsDir string) (string, error) {
	body := "skills:\n  customDirectories:\n    - " + strconv.Quote(skillsDir) + "\n"
	dir, err := materializeContentAddressed(root, map[string][]byte{ompOverlayFile: []byte(body)})
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, ompOverlayFile), nil
}

// Inert under `go test` so BuildCmd tests never write the real ~/.argus; tests swap them.
var ensureOmpMCPPluginFn = func(url string) (string, error) {
	if isTestBinary() {
		return "", nil
	}
	root, err := argusCacheDir(ompMCPCacheKind)
	if err != nil {
		return "", err
	}
	return MaterializeOmpMCPPlugin(root, url)
}

var ensureOmpConfigOverlayFn = func(skillsDir string) (string, error) {
	if isTestBinary() {
		return "", nil
	}
	root, err := argusCacheDir(ompConfigCacheKind)
	if err != nil {
		return "", err
	}
	return MaterializeOmpConfigOverlay(root, skillsDir)
}

func ompMCPFlag(url string) string {
	dir, err := ensureOmpMCPPluginFn(url)
	if err != nil {
		uxlog.Log("[mcp] omp plugin materialize failed (continuing without Argus MCP): %v", err)
		slog.Warn("[mcp] omp plugin materialize failed (continuing without Argus MCP)", "err", err)
		return ""
	}
	if dir == "" {
		return ""
	}
	return " --plugin-dir " + shellQuote(dir)
}
