package agent

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/drn/argus/internal/claudesession"
	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/profiles"
	"github.com/drn/argus/internal/review"
	"github.com/drn/argus/internal/skills"
	"github.com/drn/argus/internal/uxlog"
	_ "modernc.org/sqlite"
)

// codexStateDB is the filename of codex's local state database.
// The _5 suffix is codex's schema version; bump this if codex migrates to state_6.sqlite.
const codexStateDB = "state_5.sqlite"

// codexResumeCmd is the base resume command for codex backends.
const codexResumeCmd = "codex resume --dangerously-bypass-approvals-and-sandbox"

// codexSessionIDRe validates that a captured session ID looks like a UUID v7.
var codexSessionIDRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// piSessionFileRe matches pi's session filenames: <timestamp>_<uuid>.jsonl.
// Pi writes sessions to ~/.pi/agent/sessions/--<encoded-cwd>--/<ts>_<uuid>.jsonl.
var piSessionFileRe = regexp.MustCompile(`_([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})\.jsonl$`)

// opencodeSessionIDRe validates a captured opencode session ID. opencode mints
// IDs as "ses_" + 12 hex + 14 base62; we accept the prefix plus any base62 tail
// rather than pinning the exact length, so a future length tweak doesn't reject
// a valid ID.
var opencodeSessionIDRe = regexp.MustCompile(`^ses_[0-9A-Za-z]+$`)

// ResolveSandboxConfig returns the effective sandbox config for a task.
// Per-project settings are merged on top of the global config:
//   - project Enabled (non-nil) overrides the global Enabled flag
//   - project DenyRead paths are appended to the global list
//   - project ExtraWrite paths are appended to the global list
//   - project AllowAppleEvents bundle IDs are appended to the global list
func ResolveSandboxConfig(task *model.Task, cfg config.Config) config.SandboxConfig {
	result := cfg.Sandbox
	if task.Project != "" {
		if proj, ok := cfg.Projects[task.Project]; ok {
			if proj.Sandbox.Enabled != nil {
				result.Enabled = *proj.Sandbox.Enabled
			}
			result.DenyRead = append(append([]string{}, result.DenyRead...), proj.Sandbox.DenyRead...)
			result.ExtraWrite = append(append([]string{}, result.ExtraWrite...), proj.Sandbox.ExtraWrite...)
			result.AllowAppleEvents = append(append([]string{}, result.AllowAppleEvents...), proj.Sandbox.AllowAppleEvents...)
		}
	}
	return result
}

// ResolveCacheDirs returns the effective shared-cache-directory mapping for a
// task: global cfg.CacheDirs overlaid with the task's project CacheDirs (a
// project entry wins on a key the global config also defines, and adds any
// key the global config doesn't). The returned map is a fresh copy — neither
// input map is mutated. See the CacheDirs field doc in internal/config for
// why this exists: BuildCmd creates each resolved subdir under
// db.DataDir()/cache and exports TARGET=<dir> on the spawned process, so a
// disposable worktree can share a multi-GB toolchain cache (Android SDK,
// CocoaPods repo, ...) instead of re-provisioning it from scratch.
func ResolveCacheDirs(task *model.Task, cfg config.Config) map[string]string {
	result := make(map[string]string, len(cfg.CacheDirs))
	for k, v := range cfg.CacheDirs {
		result[k] = v
	}
	if task.Project != "" {
		if proj, ok := cfg.Projects[task.Project]; ok {
			for k, v := range proj.CacheDirs {
				result[k] = v
			}
		}
	}
	return result
}

// isValidCacheSubdir rejects a cache_dirs subdir value that is empty,
// absolute, or escapes db.DataDir()/cache via a ".." path segment — a
// config.toml entry the user typo'd or (in principle) crafted maliciously
// should never redirect a cache dir outside the argus cache root.
func isValidCacheSubdir(subdir string) bool {
	if subdir == "" || filepath.IsAbs(subdir) {
		return false
	}
	clean := filepath.Clean(subdir)
	if clean == "." || clean == ".." {
		return false
	}
	for _, part := range strings.Split(clean, string(filepath.Separator)) {
		if part == ".." {
			return false
		}
	}
	return true
}

// IsTaskSandboxed returns whether a task would run sandboxed given the
// current config. Combines sandbox config resolution with platform
// availability. Callers should persist the result on task.Sandboxed
// at creation time.
func IsTaskSandboxed(task *model.Task, cfg config.Config) bool {
	sb := ResolveSandboxConfig(task, cfg)
	return sb.Enabled && IsSandboxAvailable()
}

// ResolveBackend returns the backend config for a task.
// Priority: task.Backend > project.Backend > cfg.Defaults.Backend.
func ResolveBackend(task *model.Task, cfg config.Config) (config.Backend, error) {
	name := cfg.Defaults.Backend

	if task.Project != "" {
		if proj, ok := cfg.Projects[task.Project]; ok && proj.Backend != "" {
			name = proj.Backend
		}
	}

	if task.Backend != "" {
		name = task.Backend
	}

	if name == "" {
		return config.Backend{}, fmt.Errorf("no backend configured")
	}

	backend, ok := cfg.Backends[name]
	if !ok {
		return config.Backend{}, fmt.Errorf("backend %q not found in config", name)
	}

	return backend, nil
}

// ResolvedProfile is the daemon-side outcome of diligence-profile resolution at
// spawn. It is non-nil ONLY when a bound profile loaded, validated, and
// contributed a model that is valid for the task's resolved backend. BuildCmd
// exports ARGUS_PROFILE/ARGUS_ARCHETYPE/ARGUS_MODEL from it so the in-repo
// hera/DAG skill is profile-aware; when nil, none of those vars are exported.
type ResolvedProfile struct {
	Name      string // bound profile name (e.g. "default", "lean")
	Archetype string // the task's archetype that selected the model
	Model     string // the profile-selected model, validated for the backend
}

// ResolveModel returns the effective model for a task and, when a diligence
// profile actively drove the choice, the resolution metadata for env export.
//
// Precedence: task.Model override → profile[task.Archetype].model → project /
// backend default → "" (no --model). The profile is consulted ONLY when
// task.Model is unset AND the task carries an archetype; the per-archetype model
// is used only when the project's bound profile loads, validates, and the model
// is valid for the resolved backend. Any miss (no archetype, missing/invalid
// profile, archetype absent from the profile, or model not valid for the
// backend) falls open to the backend default — never a hard error. Empty model
// means "let the CLI pick its own default"; BuildCmd injects no --model flag.
//
// Resolution reads ~/.argus/profiles and the worktree's .argus/profiles, so it
// MUST run daemon-side (outside the sandbox, where global ~/.argus reads EPERM).
func ResolveModel(task *model.Task, backend config.Backend, cfg config.Config) (string, *ResolvedProfile) {
	if m := strings.TrimSpace(task.Model); m != "" {
		return m, nil
	}
	if rp := resolveProfile(task, backend, cfg); rp != nil {
		return rp.Model, rp
	}
	return strings.TrimSpace(backend.Model), nil
}

// resolveProfile loads and validates the task's bound diligence profile and
// returns the per-archetype model when it is valid for the resolved backend, or
// nil to signal "fall open to the backend default". It short-circuits (no disk
// access) when the task carries no archetype, keeping archetype-less spawns
// (the common case) hermetic and free of profile I/O.
func resolveProfile(task *model.Task, backend config.Backend, cfg config.Config) *ResolvedProfile {
	arch := strings.TrimSpace(task.Archetype)
	if arch == "" {
		return nil
	}

	// Per-spawn override takes precedence over the project's bound profile.
	// task.Profile is non-empty only when the operator explicitly picked a
	// different profile for this one spawn (e.g. "run one coord lean").
	profName := strings.TrimSpace(task.Profile)
	if profName == "" {
		profName = "default"
		if task.Project != "" {
			if proj, ok := cfg.Projects[task.Project]; ok {
				profName = proj.ResolveProfileName()
			}
		}
	}

	loader := &profiles.Loader{LibraryDir: filepath.Join(db.DataDir(), "profiles")}
	if task.Worktree != "" {
		loader.RepoDir = filepath.Join(task.Worktree, ".argus", "profiles")
	}

	// KnownModels is injected as the union allow-list seed; this keeps the
	// dependency direction agent → profiles (profiles never imports agent).
	// The panel-grammar validator is injected the same way (agent → review;
	// profiles never imports review either) — this call runs daemon-side
	// (see the doc comment above), so it applies the real grammar rather than
	// a nil/structural-only fallback.
	p, errs := loader.ValidateName(profName, cfg, KnownModels, review.NewValidator(cfg))
	if p == nil || len(errs) > 0 {
		uxlog.Log("[profiles] task %q: profile %q missing or invalid (%d error(s)); resolving with no --model", task.ID, profName, len(errs))
		return nil
	}

	m := strings.TrimSpace(p.Archetype[arch].Model)
	if m == "" {
		return nil
	}
	if !backendAllowsModel(m, backend) {
		uxlog.Log("[profiles] task %q: profile %q model %q not valid for resolved backend; falling through to default", task.ID, profName, m)
		return nil
	}
	return &ResolvedProfile{Name: p.Name, Archetype: arch, Model: m}
}

// backendAllowsModel reports whether m is selectable for the resolved backend —
// a member of the backend's configured Models override or, absent that, the
// built-in KnownModels for its command. Unknown/custom backends with no Models
// list have no allow-list, so a profile model can never be validated for them
// and resolution falls open (no --model).
func backendAllowsModel(m string, backend config.Backend) bool {
	for _, cand := range BackendModels(backend) {
		if cand == m {
			return true
		}
	}
	return false
}

// KnownModels returns the curated list of selectable model identifiers for a
// backend command, used to populate the new-task model selector. The Claude
// entries are the stable `claude` CLI aliases (opus / sonnet / haiku / fable)
// that always map to the current models, so the list does not churn per model
// release; the Codex entries are the current Codex CLI model names. Unknown,
// Pi, and custom backends return nil — the model selector then offers only its
// "default" and "custom…" options, so any model is still reachable by typing.
// A fresh slice is returned each call (callers may mutate / append).
func KnownModels(command string) []string {
	switch {
	case IsClaudeBackend(command):
		return []string{"opus", "sonnet", "haiku", "fable"}
	case IsCodexBackend(command):
		return []string{"gpt-5-codex", "gpt-5"}
	default:
		// opencode is intentionally custom-only: its --model takes a
		// provider/model identifier whose valid set depends on which providers
		// the user has authenticated, so a curated list would name models the
		// user may not have. The selector offers default + custom… typing, and
		// power users can pin a list via the backend's `models` config field.
		return nil
	}
}

// BackendModels returns the model options for a backend's new-task selector:
// the backend's configured Models override when non-empty, otherwise the
// built-in KnownModels for its command. A fresh slice is returned each call.
func BackendModels(b config.Backend) []string {
	if len(b.Models) > 0 {
		out := make([]string, len(b.Models))
		copy(out, b.Models)
		return out
	}
	return KnownModels(b.Command)
}

// ResolveDir returns the working directory for a task.
// Returns the project path if configured, otherwise empty string.
func ResolveDir(task *model.Task, cfg config.Config) string {
	if task.Project == "" {
		return ""
	}
	if proj, ok := cfg.Projects[task.Project]; ok {
		return proj.Path
	}
	return ""
}

// IsCodexBackend reports whether a backend command is codex-based.
// Detection uses the basename of the first word to handle both bare names ("codex")
// and absolute paths ("/usr/local/bin/codex"). Only the exact name "codex" matches.
func IsCodexBackend(command string) bool {
	fields := strings.Fields(command)
	return len(fields) > 0 && filepath.Base(fields[0]) == "codex"
}

// IsPiBackend reports whether a backend command is pi-based (pi.dev coding agent).
// Detection uses the basename of the first word to handle both bare names ("pi")
// and absolute paths.
func IsPiBackend(command string) bool {
	fields := strings.Fields(command)
	return len(fields) > 0 && filepath.Base(fields[0]) == "pi"
}

// IsClaudeBackend reports whether a backend command is Claude Code. Detection
// uses the basename of the first word to handle both bare names ("claude") and
// absolute paths ("/usr/local/bin/claude"). Only the exact name "claude"
// matches — kept strict so unknown/custom backends stay capture no-ops and rely
// on their pinned --session-id (see NeedsSessionRecapture / CaptureSessionID).
// Permission-mode flag injection is also scoped to this check so custom/bare
// commands (e.g. a bash- or sleep-backed test backend) never receive Claude-only flags.
func IsClaudeBackend(command string) bool {
	fields := strings.Fields(command)
	return len(fields) > 0 && filepath.Base(fields[0]) == "claude"
}

// IsOpencodeBackend reports whether a backend command is opencode-based.
// Detection uses the basename of the first word to handle both bare names
// ("opencode") and absolute paths. opencode is a capture-style backend: like
// codex and pi it mints its own session ID (no start-time --session-id) and
// resumes via `--session <id>`.
func IsOpencodeBackend(command string) bool {
	fields := strings.Fields(command)
	return len(fields) > 0 && filepath.Base(fields[0]) == "opencode"
}

// hasPermissionFlags reports whether a backend command already specifies a
// Claude permission flag. When true, BuildCmd does NOT inject the configured
// PermissionMode flags — a hand-edited command always wins, and we never
// emit a conflicting/duplicate --permission-mode.
func hasPermissionFlags(command string) bool {
	return strings.Contains(command, "--permission-mode") ||
		strings.Contains(command, "--dangerously-skip-permissions") ||
		strings.Contains(command, "--allow-dangerously-skip-permissions")
}

// hasModelFlag reports whether a backend command already names the --model
// flag as a standalone token ("--model <x>", "--model=<x>", or a trailing
// "--model"). A bare substring check would also match hypothetical flags
// like --model-format and wrongly suppress injection.
func hasModelFlag(command string) bool {
	return strings.Contains(command, "--model ") ||
		strings.Contains(command, "--model=") ||
		strings.HasSuffix(command, "--model")
}

// piEncodeCwd mirrors pi's getDefaultSessionDir(): strip exactly ONE leading
// slash or backslash (matching pi's `cwd.replace(/^[/\\]/, "")` — NOT a
// TrimLeft), then replace remaining /, \, : with -, then wrap in --…--.
// Diverging from pi's exact semantics here would point Argus at the wrong
// session directory and break post-exit UUID capture.
func piEncodeCwd(cwd string) string {
	trimmed := cwd
	if len(trimmed) > 0 && (trimmed[0] == '/' || trimmed[0] == '\\') {
		trimmed = trimmed[1:]
	}
	replacer := strings.NewReplacer("/", "-", "\\", "-", ":", "-")
	return "--" + replacer.Replace(trimmed) + "--"
}

// CapturePiSessionID finds the most recent pi session file for the given
// worktree path under ~/.pi/agent/sessions/--<encoded-cwd>--/ and extracts the
// UUID from its filename. Returns the session UUID or an error if none is found.
func CapturePiSessionID(worktreePath string) (string, error) {
	if worktreePath == "" {
		return "", fmt.Errorf("CapturePiSessionID: worktree path is empty")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("CapturePiSessionID: home dir: %w", err)
	}
	dir := filepath.Join(home, ".pi", "agent", "sessions", piEncodeCwd(worktreePath))
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("CapturePiSessionID: read dir %s: %w", dir, err)
	}

	var newestID string
	var newestMod int64
	for _, e := range entries {
		if e.IsDir() {
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
		if mod := info.ModTime().UnixNano(); mod > newestMod {
			newestMod = mod
			newestID = m[1]
		}
	}
	if newestID == "" {
		return "", fmt.Errorf("CapturePiSessionID: no session files in %s", dir)
	}
	return newestID, nil
}

// CaptureClaudeSessionID returns the session UUID of the most recently active
// Claude transcript for the given worktree. It delegates to claudesession.List
// — the single source of truth for discovering and parsing
// ~/.claude/projects/<encoded-cwd>/*.jsonl — which orders newest-activity-first,
// and returns its top entry's ID.
//
// Newest-first is the active conversation: each Argus task owns a unique
// worktree, so its project directory is scoped to just that task's sessions, and
// after a Claude /clear the freshly-minted UUID's transcript sorts first.
// Sub-agent (Task tool) records interleave into the main transcript file rather
// than spawning separate files, so no sidechain filtering is needed.
//
// Returns an error when the worktree path is empty, the project directory is
// missing, or it holds no UUID-named transcript; callers treat the error as
// "nothing to capture — leave the pinned SessionID intact".
func CaptureClaudeSessionID(worktreePath string) (string, error) {
	sessions, err := claudesession.List(worktreePath)
	if err != nil {
		return "", fmt.Errorf("CaptureClaudeSessionID: %w", err)
	}
	if len(sessions) == 0 {
		return "", fmt.Errorf("CaptureClaudeSessionID: no Claude transcript for %s", worktreePath)
	}
	return sessions[0].ID, nil
}

// CaptureCodexSessionID looks up the most recent codex session for the given
// worktree path in codex's local state database (~/.codex/state_5.sqlite).
// Returns the session UUID or an error if none is found.
// The returned ID is validated as a UUID before being returned.
func CaptureCodexSessionID(worktreePath string) (string, error) {
	if worktreePath == "" {
		return "", fmt.Errorf("CaptureCodexSessionID: worktree path is empty")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("CaptureCodexSessionID: home dir: %w", err)
	}
	dbPath := filepath.Join(home, ".codex", codexStateDB)
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return "", fmt.Errorf("CaptureCodexSessionID: open db: %w", err)
	}
	defer db.Close()

	var id string
	err = db.QueryRow(
		`SELECT id FROM threads WHERE cwd = ? ORDER BY updated_at DESC LIMIT 1`,
		worktreePath,
	).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("CaptureCodexSessionID: query: %w", err)
	}
	if !codexSessionIDRe.MatchString(id) {
		return "", fmt.Errorf("CaptureCodexSessionID: unexpected session ID format: %q", id)
	}
	return id, nil
}

// opencodeDataDir returns opencode's data root: $XDG_DATA_HOME/opencode when
// XDG_DATA_HOME is set, else ~/.local/share/opencode. This mirrors opencode's
// own xdg-basedir resolution (Global.Path.data = xdgData/opencode).
func opencodeDataDir() (string, error) {
	if xdg := strings.TrimSpace(os.Getenv("XDG_DATA_HOME")); xdg != "" {
		return filepath.Join(xdg, "opencode"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("opencodeDataDir: home dir: %w", err)
	}
	return filepath.Join(home, ".local", "share", "opencode"), nil
}

// canonPath returns the symlink-resolved absolute form of a path, falling back
// to the cleaned absolute path when resolution fails (e.g. the path no longer
// exists). opencode stores session directories as resolved absolute paths, so
// matching requires canonicalizing both sides.
func canonPath(p string) string {
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		return resolved
	}
	if abs, err := filepath.Abs(p); err == nil {
		return filepath.Clean(abs)
	}
	return filepath.Clean(p)
}

// CaptureOpencodeSessionID recovers the most recently updated opencode session
// that ran in the given worktree. opencode keys sessions by git root-commit
// (shared across every worktree of a repo), so the worktree is identified by
// the per-session "directory" field, not by the storage bucket. Current
// opencode (v1.14+) keeps sessions in SQLite (~/.local/share/opencode/opencode.db,
// table "session"); older opencode used JSON files under
// storage/session/<projectID>/<ses_id>.json. We read SQLite first and fall back
// to the JSON walk, returning the validated ses_… ID. Returns an error when no
// matching session is found — callers treat that as "nothing to capture", and
// because opencode never pins an ID the effect is simply that the next start is
// a fresh session.
func CaptureOpencodeSessionID(worktreePath string) (string, error) {
	if worktreePath == "" {
		return "", fmt.Errorf("CaptureOpencodeSessionID: worktree path is empty")
	}
	dataDir, err := opencodeDataDir()
	if err != nil {
		return "", err
	}
	want := canonPath(worktreePath)

	// SQLite first (current opencode).
	dbPath := filepath.Join(dataDir, "opencode.db")
	if _, statErr := os.Stat(dbPath); statErr == nil {
		if id, qerr := captureOpencodeFromSQLite(dbPath, want); qerr == nil && id != "" {
			return id, nil
		}
		// fall through to JSON on miss/error
	}

	// Legacy JSON store fallback (opencode <= ~v1.13).
	if id := captureOpencodeFromJSON(filepath.Join(dataDir, "storage", "session"), want); id != "" {
		return id, nil
	}

	return "", fmt.Errorf("CaptureOpencodeSessionID: no session for worktree %s", worktreePath)
}

// captureOpencodeFromSQLite queries opencode's session table for the
// most-recently-updated session whose directory matches the worktree. Opened
// read-only (mode=ro) so we never mutate opencode's DB and still read the WAL —
// immutable=1 would skip the -wal file and miss an uncheckpointed newest row.
// The DSN is built via url.URL so a data-dir path containing '?' or '#' can't
// corrupt the query string.
//
// opencode stores `directory` as a resolved-absolute path, so the indexed
// exact-match query handles the common case in O(log n). Only when that misses
// do we fall back to a full scan that symlink-resolves each stored directory —
// covering the rare case where the stored path and the worktree differ only by
// a symlink. Malformed (non-ses_) ids are skipped, not fatal, so one bad row
// never hides an older valid session.
func captureOpencodeFromSQLite(dbPath, want string) (string, error) {
	dsn := (&url.URL{Scheme: "file", Path: dbPath, RawQuery: "mode=ro"}).String()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return "", fmt.Errorf("captureOpencodeFromSQLite: open: %w", err)
	}
	defer func() { _ = db.Close() }()

	// Fast path: indexed exact-match on the resolved-absolute directory.
	var id string
	err = db.QueryRow(
		`SELECT id FROM session WHERE directory = ? ORDER BY time_updated DESC LIMIT 1`,
		want,
	).Scan(&id)
	if err == nil && opencodeSessionIDRe.MatchString(id) {
		return id, nil
	}

	// Fallback: scan newest-first, symlink-resolving each stored directory,
	// skipping malformed ids.
	rows, err := db.Query(`SELECT id, directory FROM session ORDER BY time_updated DESC`)
	if err != nil {
		return "", fmt.Errorf("captureOpencodeFromSQLite: query: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var rid, dir string
		if scanErr := rows.Scan(&rid, &dir); scanErr != nil {
			continue
		}
		if !opencodeSessionIDRe.MatchString(rid) {
			continue
		}
		if dir == want || canonPath(dir) == want {
			return rid, nil
		}
	}
	if rerr := rows.Err(); rerr != nil {
		return "", fmt.Errorf("captureOpencodeFromSQLite: scan: %w", rerr)
	}
	return "", fmt.Errorf("captureOpencodeFromSQLite: no session for %s", want)
}

// captureOpencodeFromJSON walks the legacy JSON session store
// (<sessionRoot>/<projectID>/<ses_id>.json) and returns the id of the
// most-recently-updated session whose directory matches the worktree. Returns
// "" when no match is found (the caller falls open).
func captureOpencodeFromJSON(sessionRoot, want string) string {
	projectDirs, err := os.ReadDir(sessionRoot)
	if err != nil {
		return ""
	}
	var bestID string
	var bestUpdated float64
	for _, pd := range projectDirs {
		if !pd.IsDir() {
			continue
		}
		files, err := os.ReadDir(filepath.Join(sessionRoot, pd.Name()))
		if err != nil {
			continue
		}
		for _, f := range files {
			if f.IsDir() || !strings.HasSuffix(f.Name(), ".json") {
				continue
			}
			raw, err := os.ReadFile(filepath.Join(sessionRoot, pd.Name(), f.Name()))
			if err != nil {
				continue
			}
			var s struct {
				ID        string `json:"id"`
				Directory string `json:"directory"`
				Time      struct {
					Updated float64 `json:"updated"`
				} `json:"time"`
			}
			if json.Unmarshal(raw, &s) != nil {
				continue
			}
			if s.ID == "" || !opencodeSessionIDRe.MatchString(s.ID) {
				continue
			}
			if s.Directory != want && canonPath(s.Directory) != want {
				continue
			}
			if s.Time.Updated >= bestUpdated {
				bestUpdated = s.Time.Updated
				bestID = s.ID
			}
		}
	}
	return bestID
}

// CaptureSessionID dispatches to the backend-specific post-exit capture
// function based on the resolved backend command. Codex/Pi scan their own state
// (SQLite / session files); Claude scans its transcript directory so a /clear
// (which mints a fresh UUID) is honored on the next resume. Returns ("", nil)
// for unknown backends, which pre-mint and pin their ID via --session-id with
// nothing to scan for. Used by both the TUI (handleSessionExitUI) and the daemon
// (onFinish) so headless / PWA-only users still get resume support.
func CaptureSessionID(task *model.Task, cfg config.Config) (string, error) {
	backend, err := ResolveBackend(task, cfg)
	if err != nil {
		return "", err
	}
	switch {
	case IsCodexBackend(backend.Command):
		return CaptureCodexSessionID(task.Worktree)
	case IsPiBackend(backend.Command):
		return CapturePiSessionID(task.Worktree)
	case IsOpencodeBackend(backend.Command):
		return CaptureOpencodeSessionID(task.Worktree)
	case IsClaudeBackend(backend.Command):
		return CaptureClaudeSessionID(task.Worktree)
	default:
		return "", nil
	}
}

// NeedsSessionRecapture reports whether a finished task should re-run
// CaptureSessionID. Codex/Pi mint their ID lazily and keep it stable across
// resumes, so they capture once — only while SessionID is still empty. Claude
// mints a fresh UUID on every /clear, so its stored ID goes stale and must be
// refreshed on every exit. Unknown backends never recapture (they rely on the
// pinned --session-id). Both exit sites (daemon captureSessionIDPostExit and TUI
// handleSessionExitUI) gate on this so they stay in lockstep.
func NeedsSessionRecapture(task *model.Task, cfg config.Config) bool {
	backend, err := ResolveBackend(task, cfg)
	if err != nil {
		return false
	}
	switch {
	case IsClaudeBackend(backend.Command):
		return true
	case IsCodexBackend(backend.Command), IsPiBackend(backend.Command), IsOpencodeBackend(backend.Command):
		return task.SessionID == ""
	default:
		return false
	}
}

// BuildCmd constructs the exec.Cmd for running an agent on a task.
// If the task has a SessionID, the command uses --resume to reconnect.
// If resume is false and SessionID is set, it uses --session-id for a new session with a known ID.
// When sandbox is enabled and available, the command is wrapped with sandbox-exec.
// The returned cleanup function removes the sandbox config temp file (nil if no sandbox).
func BuildCmd(task *model.Task, cfg config.Config, resume bool) (*exec.Cmd, func(), error) {
	backend, err := ResolveBackend(task, cfg)
	if err != nil {
		return nil, nil, err
	}

	cmdStr := backend.Command

	isCodex := IsCodexBackend(backend.Command)
	isPi := IsPiBackend(backend.Command)
	isOpencode := IsOpencodeBackend(backend.Command)

	// Inject the configured permission mode for claude backends only. Scoped to
	// IsClaudeBackend (not "not codex/pi") so custom/bare commands never receive
	// Claude-only flags. Skipped when the command already names a permission
	// flag (command wins) so we never double-inject. Injected before
	// resume/session-id/prompt suffixes so the flags precede the "--" separator.
	if IsClaudeBackend(backend.Command) && !hasPermissionFlags(backend.Command) {
		if flags := config.PermissionModeFlags(cfg.Defaults.PermissionMode); flags != "" {
			cmdStr += " " + flags
		}
	}

	// Inject the resolved model (task override > diligence profile > backend
	// default) for known backend CLIs — claude, codex, and pi all accept
	// --model. Scoped like permission-mode injection so custom/bare commands
	// never receive the flag, and skipped when the command already names --model
	// (a hand-edited command always wins). Computed once here because the codex
	// resume branch below replaces cmdStr and must re-append the flag itself.
	// resolvedProfile is non-nil only when a bound profile actively contributed
	// the model; its fields drive the ARGUS_PROFILE/ARCHETYPE/MODEL env export.
	resolvedModel, resolvedProfile := ResolveModel(task, backend, cfg)
	modelFlag := ""
	if resolvedModel != "" &&
		(IsClaudeBackend(backend.Command) || isCodex || isPi || isOpencode) &&
		!hasModelFlag(backend.Command) {
		modelFlag = " --model " + shellQuote(resolvedModel)
	}
	cmdStr += modelFlag

	// Make argus's own builtin skills (archive, hera, ...) available to Claude
	// backends by materializing them and appending --add-dir. Claude Code loads
	// .claude/skills/ from a --add-dir directory as a documented exception to
	// --add-dir otherwise granting file access only. Additive to any --add-dir
	// already present in the backend command — repeatable flag, no conflict.
	// Materialization failure is logged and skipped rather than blocking launch.
	if IsClaudeBackend(backend.Command) {
		if root, err := skills.EnsureBuiltinSkills(); err != nil {
			uxlog.Log("[skills] builtin skills materialize failed (continuing without them): %v", err)
		} else if root != "" {
			cmdStr += " --add-dir " + shellQuote(root)
		}
	}

	// Make argus's own builtin routing content (hera coordination, argus-task
	// self-management) reach every Claude backend session by materializing it
	// and appending --append-system-prompt-file — the injection-side
	// counterpart to the --add-dir skills block above. Unconditional across
	// every session kind (coordinator, worker, freelance) and NOT gated on
	// cfg.Hera.Enabled: the content is self-gating at read time (each section
	// checks ARGUS_TASK_ID/$PWD sandbox residency), so injecting it into a
	// non-argus spawn is inert. Materialization failure is logged and skipped
	// rather than blocking launch.
	if IsClaudeBackend(backend.Command) {
		if path, err := ensureBuiltinRoutingFn(); err != nil {
			uxlog.Log("[routing] builtin routing content materialize failed (continuing without it): %v", err)
		} else if path != "" {
			cmdStr += " --append-system-prompt-file " + shellQuote(path)
		}
	}

	if resume {
		// Codex resumes by replacing the base command unconditionally — that's
		// codex's contract and TestBuildCmd_Resume pins it. Claude and pi only
		// append their resume flag when SessionID is non-empty, mirroring the
		// original behavior pinned by TestBuildCmd_ResumeNoSessionIDClaude:
		// resume=true with an empty SessionID silently starts fresh rather
		// than emitting an obviously-broken `--resume ''` flag.
		switch {
		case isCodex:
			// Flags must precede the positional session-id argument.
			cmdStr = codexResumeCmd + modelFlag + " " + shellQuote(task.SessionID)
		case (isPi || isOpencode) && task.SessionID != "":
			// Pi / opencode style: append --session <ID> (pi accepts partial
			// UUIDs; opencode takes its ses_… ID). Checked before the
			// Claude-style branch because opencode is not pi but must NOT take
			// --resume.
			cmdStr += " --session " + shellQuote(task.SessionID)
		case !isPi && !isOpencode && task.SessionID != "":
			// Claude-style: append --resume flag.
			cmdStr += " --resume " + shellQuote(task.SessionID)
		}
	} else {
		// New session — only pin session ID for Claude-style backends.
		// Codex, pi, and opencode don't support --session-id; their IDs are
		// captured post-exit.
		if !isCodex && !isPi && !isOpencode && task.SessionID != "" {
			cmdStr += " --session-id " + shellQuote(task.SessionID)
		}
		if task.Prompt != "" {
			switch {
			case backend.PromptFlag != "":
				cmdStr += " " + backend.PromptFlag + " " + shellQuote(task.Prompt)
			case isPi:
				// Pi's argv parser does not honor "--" as end-of-flags; pass the
				// prompt as a positional argument. Prompts beginning with "-" or
				// "@" trigger pi's flag/file-include parsing (the @ behavior is
				// pi's documented file-inclusion feature, not a bug).
				cmdStr += " " + shellQuote(task.Prompt)
			default:
				// Use -- to separate options from the prompt argument.
				// Without this, prompts starting with "-" are parsed as CLI flags.
				cmdStr += " -- " + shellQuote(task.Prompt)
			}
		}
	}

	// Wrap with sandbox if enabled (effective config merges global + per-project overrides).
	var sandboxCleanup func()
	effectiveSandbox := ResolveSandboxConfig(task, cfg)
	if effectiveSandbox.Enabled && IsSandboxAvailable() && task.Worktree != "" {
		profilePath, params, cleanup, serr := GenerateSandboxConfig(task.Worktree, effectiveSandbox)
		if serr == nil {
			cmdStr = WrapWithSandbox(cmdStr, profilePath, params)
			sandboxCleanup = cleanup
		}
		// If sandbox config generation fails, fall through to unsandboxed
	}

	// On any error return below, run sandboxCleanup so the temp profile file
	// isn't leaked. On success, the caller (runner.Start) takes ownership of
	// the cleanup func and runs it after the session exits.
	committed := false
	defer func() {
		if !committed && sandboxCleanup != nil {
			sandboxCleanup()
		}
	}()

	// Every task must have a worktree — never run in the project directory.
	if task.Worktree == "" {
		return nil, nil, fmt.Errorf("task %q has no worktree set — refusing to start without worktree isolation", task.Name)
	}

	// Pre-flight: confirm the worktree directory actually exists. Without this,
	// a missing path surfaces post-fork as "fork/exec /bin/sh: no such file or
	// directory" — Go's forkExec reports the chdir failure using the exec path,
	// which is misleading. Fail early with an actionable message instead.
	//
	// This narrows but does not eliminate the race: a concurrent worktree
	// removal (orphan sweeper, manual rm) between this stat and cmd.Start can
	// still produce the original cryptic error. Callers should not assume a
	// successful BuildCmd guarantees the directory still exists at exec time.
	if _, statErr := os.Stat(task.Worktree); statErr != nil {
		if errors.Is(statErr, fs.ErrNotExist) {
			return nil, nil, fmt.Errorf("worktree path missing: %s (delete the task or recreate the worktree)", task.Worktree)
		}
		return nil, nil, fmt.Errorf("worktree path unreachable: %s: %w", task.Worktree, statErr)
	}

	cmd := exec.Command("sh", "-c", cmdStr)
	cmd.Dir = task.Worktree
	// Inherit the parent's env so PATH, HOME, and friends survive, then force
	// the terminal-capability variables. The agent's controlling terminal is
	// argus's PTY, rendered by the in-process x/vt emulator (truecolor-capable)
	// — NOT whatever terminal the daemon happened to inherit. A launchd-started
	// daemon has no TERM at all (the LaunchAgent plist only sets PATH), which
	// made every agent it spawned render colorless after a daemon restart,
	// while TUI-auto-started daemons (forked from the user's shell) produced
	// colored agents. Forcing both keys makes agent color detection independent
	// of daemon provenance; appending wins over earlier duplicates per
	// exec.Cmd.Env semantics.
	//
	// Also force GOCACHE and PLAYWRIGHT_BROWSERS_PATH out from under
	// ~/Library/Caches (their tool defaults) and into ~/.argus/cache/. macOS
	// TCC gates writes under ~/Library/{Application Support,Containers,Caches}
	// behind an "access data from other apps" prompt attributed to the
	// responsible argus process, even when a spawned agent's build/test tool
	// (not argus itself) is the actual writer — so heavy concurrent build/test
	// activity across worktrees kept re-triggering the prompt regardless of
	// how the argus binary itself was signed. Both tools fully honor the
	// override.
	cmd.Env = append(os.Environ(),
		"TERM=xterm-256color",
		"COLORTERM=truecolor",
		"GOCACHE="+filepath.Join(db.DataDir(), "cache", "go-build"),
		"PLAYWRIGHT_BROWSERS_PATH="+filepath.Join(db.DataDir(), "cache", "ms-playwright"),
	)
	// Surface the task ID to the agent process so MCP sub-tasks (task_complete,
	// task_set_result, argus_clipboard_set, …) can target it explicitly
	// instead of resolving by cwd. Empty task.ID can only happen pre-Add (which
	// CreateAndStart guards against), but we skip the export defensively
	// rather than emit a literal "ARGUS_TASK_ID=" with no value.
	if task.ID != "" {
		cmd.Env = append(cmd.Env, "ARGUS_TASK_ID="+task.ID)
	}

	// Diligence-profile env export (add-diligence-profiles). When a bound
	// profile actively contributed a backend-valid model, surface the resolution
	// to the agent so the in-repo hera/DAG skill is profile-aware. All three are
	// exported together (ARGUS_MODEL is always meaningful here) or omitted
	// entirely — never exported empty. Mirrors the ARGUS_TASK_ID export above.
	if resolvedProfile != nil {
		cmd.Env = append(cmd.Env,
			"ARGUS_PROFILE="+resolvedProfile.Name,
			"ARGUS_ARCHETYPE="+resolvedProfile.Archetype,
			"ARGUS_MODEL="+resolvedProfile.Model,
		)
	}

	// Project-configurable shared cache directories (cache-dir-config): opt-in
	// generalization of the GOCACHE/PLAYWRIGHT_BROWSERS_PATH redirect above to
	// any OTHER toolchain a project's builds depend on (Android SDK, CocoaPods
	// repo cache, Yarn/npm cache, ...) that is expensive to re-provision from
	// scratch in every disposable worktree. Each cfg.CacheDirs / project
	// CacheDirs entry maps a target env var to a subdir under
	// db.DataDir()/cache, shared across every worktree of every task for that
	// var — never per-worktree. Unlike backend.EnvVars this never carries a
	// secret value, only a shared directory PATH, so it's safe to log and to
	// persist in config.toml in the clear. An invalid entry (empty/"="-bearing
	// target, or a subdir that's absolute or escapes via "..") is skipped with
	// a log line rather than failing the spawn; sorted for deterministic env
	// ordering across repeated spawns.
	cacheDirs := ResolveCacheDirs(task, cfg)
	cacheTargets := make([]string, 0, len(cacheDirs))
	for target := range cacheDirs {
		cacheTargets = append(cacheTargets, target)
	}
	sort.Strings(cacheTargets)
	for _, target := range cacheTargets {
		subdir := cacheDirs[target]
		if target == "" || strings.Contains(target, "=") || !isValidCacheSubdir(subdir) {
			uxlog.Log("[cache] skipping invalid cache_dirs entry %q=%q", target, subdir)
			continue
		}
		dir := filepath.Join(db.DataDir(), "cache", subdir)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			uxlog.Log("[cache] failed to create shared cache dir %s for %s (continuing without it): %v", dir, target, err)
			continue
		}
		cmd.Env = append(cmd.Env, target+"="+dir)
	}

	// Per-backend credential env mapping. backend.EnvVars maps a TARGET env var
	// (set in the child) to a SOURCE descriptor resolved at spawn time. A
	// bare-string or env://-prefixed source keeps resolving through the
	// existing pluggable secretResolver var directly, unmemoized, exactly as
	// before this registry existed (preserving SetSecretResolver's contract
	// unchanged); only a source whose scheme (per splitSecretScheme,
	// secretregistry.go) is something OTHER than "env" — keychain:// or op://
	// — dispatches instead through the secrets-resolution registry, built
	// fresh from cfg.Secrets so a [secrets] config edit takes effect on the
	// very next spawn — never wired once at daemon/supervisor startup.
	// splitSecretScheme is reused here rather than re-parsed so this dispatch
	// predicate can never drift from the registry's own scheme splitting. The
	// registry resolver is constructed only when there's an EnvVars mapping to
	// resolve, skipping a closure allocation a plain spawn has no stake in —
	// ResolverFor itself is cheap either way (it only closes over cfg.Secrets;
	// no PATH lookup or config read happens at construction time, only later,
	// on an actual scheme-prefixed resolve). The mapping carries NO secret
	// value — only the descriptor. A resolved value is appended to the child
	// env (later entries win per exec.Cmd.Env semantics); an unresolved source
	// sets nothing and logs a non-sensitive warning naming ONLY the variable,
	// never the value. We never log the resolved value.
	if len(backend.EnvVars) > 0 {
		registryResolve := ResolverFor(cfg.Secrets)
		for target, source := range backend.EnvVars {
			if target == "" || source == "" {
				continue
			}
			scheme, rest := splitSecretScheme(source)
			resolve, resolveInput := secretResolver, rest
			if scheme != "env" {
				resolve, resolveInput = registryResolve, source
			}
			value, ok := resolve(resolveInput)
			if !ok {
				uxlog.Log("[agent] backend %q: credential source %q did not resolve; %q left unset in child env", backend.Command, source, target)
				continue
			}
			cmd.Env = append(cmd.Env, target+"="+value)
		}
	}

	committed = true
	return cmd, sandboxCleanup, nil
}

// shellQuote wraps a string in single quotes with proper escaping.
// Embedded single quotes are replaced with the four-character sequence
// close-quote, backslash, single-quote, open-quote (see the literal
// replacement string below).
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
