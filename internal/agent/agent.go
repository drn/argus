package agent

import (
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/model"
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

// ResolveSandboxConfig returns the effective sandbox config for a task.
// Per-project settings are merged on top of the global config:
//   - project Enabled (non-nil) overrides the global Enabled flag
//   - project DenyRead paths are appended to the global list
//   - project ExtraWrite paths are appended to the global list
func ResolveSandboxConfig(task *model.Task, cfg config.Config) config.SandboxConfig {
	result := cfg.Sandbox
	if task.Project != "" {
		if proj, ok := cfg.Projects[task.Project]; ok {
			if proj.Sandbox.Enabled != nil {
				result.Enabled = *proj.Sandbox.Enabled
			}
			result.DenyRead = append(append([]string{}, result.DenyRead...), proj.Sandbox.DenyRead...)
			result.ExtraWrite = append(append([]string{}, result.ExtraWrite...), proj.Sandbox.ExtraWrite...)
		}
	}
	return result
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

// CaptureSessionID dispatches to the backend-specific post-exit capture
// function based on the resolved backend command. Returns ("", nil) for
// backends that mint their session ID at start (Claude-style) — there's
// nothing to capture for those, the ID was set on task.SessionID before
// the agent ran. Used by both the TUI (handleSessionExitUI) and the daemon
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
	default:
		return "", nil
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

	if resume {
		// Codex resumes by replacing the base command unconditionally — that's
		// codex's contract and TestBuildCmd_Resume pins it. Claude and pi only
		// append their resume flag when SessionID is non-empty, mirroring the
		// original behavior pinned by TestBuildCmd_ResumeNoSessionIDClaude:
		// resume=true with an empty SessionID silently starts fresh rather
		// than emitting an obviously-broken `--resume ''` flag.
		switch {
		case isCodex:
			cmdStr = codexResumeCmd + " " + shellQuote(task.SessionID)
		case isPi && task.SessionID != "":
			// Pi-style: append --session <UUID> (pi accepts partial UUIDs).
			cmdStr += " --session " + shellQuote(task.SessionID)
		case !isPi && task.SessionID != "":
			// Claude-style: append --resume flag.
			cmdStr += " --resume " + shellQuote(task.SessionID)
		}
	} else {
		// New session — only pin session ID for Claude-style backends.
		// Codex and pi don't support --session-id; their IDs are captured post-exit.
		if !isCodex && !isPi && task.SessionID != "" {
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
