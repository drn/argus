package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/skills"
	"github.com/drn/argus/internal/uxlog"
)

// codexAuthOverrideEnv are API-key sources Codex can use instead of the
// account's own stored login, so an inherited value would bill another identity.
var codexAuthOverrideEnv = []string{"OPENAI_API_KEY", "CODEX_API_KEY"}

// BootstrapCodexHome creates an account's Codex home (0700) when missing.
// Nothing is inherited from ~/.codex: a fresh account logs in with
// `codex login` inside a session that runs under this CODEX_HOME.
func BootstrapCodexHome(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating codex account home %s: %w", dir, err)
	}
	return nil
}

// resolveSpawnCodexHome returns the explicit Codex home a Codex task spawns
// under, bootstrapping it, or "" for the default account and for non-Codex
// backends. An unknown/removed stored account is an error: a spawn must never
// silently fall back to the default identity.
func resolveSpawnCodexHome(task *model.Task, cfg config.Config, isCodex bool) (string, error) {
	if !isCodex || task.Account == "" || task.Account == config.DefaultAccountName {
		return "", nil
	}
	dir, explicit, err := cfg.CodexHome(task.Account)
	if err != nil {
		uxlog.Log("[account] task %q: %v", task.ID, err)
		return "", fmt.Errorf("codex account %q: %w", task.Account, err)
	}
	if !explicit {
		return "", nil
	}
	if err := BootstrapCodexHome(dir); err != nil {
		uxlog.Log("[account] task %q codex bootstrap failed: %v", task.ID, err)
		return "", fmt.Errorf("codex account %q: %w", task.Account, err)
	}
	return dir, nil
}

// CodexHomeForTask resolves the source Codex home whose state_5.sqlite holds a
// task's sessions: the account's codex_home, or "" for the default account
// (meaning the process-default resolution in CaptureCodexSessionIDIn). It
// errors when the stored account is no longer configured.
func CodexHomeForTask(task *model.Task, cfg config.Config) (string, error) {
	if task == nil {
		return "", fmt.Errorf("codex home: nil task")
	}
	if task.Account == "" || task.Account == config.DefaultAccountName {
		return "", nil
	}
	dir, explicit, err := cfg.CodexHome(task.Account)
	if err != nil {
		return "", err
	}
	if !explicit {
		return "", nil
	}
	return dir, nil
}

// sandboxWithCodexHome appends the task's own account Codex home to ExtraWrite
// when it lies outside ~/.codex (already writable), so login and session state
// written through the overlay's symlinks do not EPERM.
func sandboxWithCodexHome(sc config.SandboxConfig, dir string) config.SandboxConfig {
	if dir == "" {
		return sc
	}
	def := config.DefaultCodexHome()
	if dir == def || (def != "" && strings.HasPrefix(dir, def+string(filepath.Separator))) {
		return sc
	}
	sc.ExtraWrite = append(append([]string(nil), sc.ExtraWrite...), dir)
	return sc
}

const (
	codexLoginStatusTimeout = 3 * time.Second
	codexLoginStatusTTL     = time.Minute
)

// codexLoginCmd builds the `codex login status` probe; tests replace it.
var codexLoginCmd = func(ctx context.Context, codexHome string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "codex", "login", "status")
	env := make([]string, 0, len(os.Environ())+1)
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if name == "CODEX_HOME" || slices.Contains(codexAuthOverrideEnv, name) {
			continue
		}
		env = append(env, kv)
	}
	cmd.Env = append(env, "CODEX_HOME="+codexHome)
	return cmd
}

type codexLoginEntry struct {
	loggedIn bool
	at       time.Time
}

var (
	codexLoginMu    sync.Mutex
	codexLoginCache = map[string]codexLoginEntry{}
)

// errCodexLoginUnknown is returned when the sign-in state is not probed.
var errCodexLoginUnknown = errors.New("codex login status not probed")

// CodexLoginStatus is codexLoginStatus, inert under go test so no test ever
// runs the real codex binary.
func CodexLoginStatus(ctx context.Context, source string) (bool, error) {
	if isTestBinary() {
		return false, errCodexLoginUnknown
	}
	return codexLoginStatus(ctx, source)
}

// codexLoginStatus reports whether `codex login status` succeeds for the Argus
// overlay of the given source Codex home ("" = the default home), i.e. the
// CODEX_HOME an Argus session actually sees. Only the exit status is used:
// output is discarded unread because it can echo a masked API key. Results are
// cached briefly per home.
func codexLoginStatus(ctx context.Context, source string) (bool, error) {
	home, err := codexLoginHome(source)
	if err != nil {
		return false, err
	}
	codexLoginMu.Lock()
	if e, ok := codexLoginCache[home]; ok && time.Since(e.at) < codexLoginStatusTTL {
		codexLoginMu.Unlock()
		return e.loggedIn, nil
	}
	codexLoginMu.Unlock()

	ctx, cancel := context.WithTimeout(ctx, codexLoginStatusTimeout)
	defer cancel()
	cmd := codexLoginCmd(ctx, home)
	cmd.Stdout, cmd.Stderr, cmd.Stdin = nil, nil, nil
	runErr := cmd.Run()
	if runErr != nil {
		if ctx.Err() != nil {
			return false, fmt.Errorf("codex login status: %w", ctx.Err())
		}
		var exitErr *exec.ExitError
		if !errors.As(runErr, &exitErr) {
			return false, fmt.Errorf("codex login status: %w", runErr)
		}
	}
	loggedIn := runErr == nil
	codexLoginMu.Lock()
	codexLoginCache[home] = codexLoginEntry{loggedIn: loggedIn, at: time.Now()}
	codexLoginMu.Unlock()
	return loggedIn, nil
}

// codexLoginHome picks the overlay home when Argus has built one, since a
// login performed inside an Argus session lands there; else the source.
// skills/ marks a built overlay: the default overlay root also exists merely
// as the parent of every account overlay.
func codexLoginHome(source string) (string, error) {
	overlay, err := skills.ArgusCodexHomeFor(source)
	if err == nil {
		if info, serr := os.Stat(filepath.Join(overlay, "skills")); serr == nil && info.IsDir() {
			return overlay, nil
		}
	}
	if strings.TrimSpace(source) != "" {
		return filepath.Abs(source)
	}
	return skills.UserCodexHome()
}
