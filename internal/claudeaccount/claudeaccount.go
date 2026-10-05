// Package claudeaccount reports the login state of a Claude Code config
// directory by shelling out to `claude auth status`. It never reads or writes
// credential files; Claude itself answers the question.
package claudeaccount

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/drn/argus/internal/uxlog"
)

// Status is the identity behind one Claude config directory.
type Status struct {
	LoggedIn bool
	Email    string
	Org      string
	Plan     string
}

const (
	statusTimeout = 5 * time.Second
	// CacheTTL bounds how long CachedAuthStatus reuses a result.
	CacheTTL = 60 * time.Second
)

// runAuthStatus is the exec seam: it returns the raw JSON from
// `claude auth status` run under dir.
var runAuthStatus = func(ctx context.Context, dir string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "claude", "auth", "status")
	cmd.Env = probeEnv(os.Environ(), dir, defaultClaudeDir())
	return cmd.Output()
}

// authOverrideEnv mirrors what BuildCmd strips for explicit accounts, so the
// label reports the login the spawned session will actually use.
var authOverrideEnv = []string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "CLAUDE_CODE_OAUTH_TOKEN"}

// probeEnv builds the probe environment. The default dir runs with
// CLAUDE_CONFIG_DIR unset, because setting it at all (even to ~/.claude)
// switches Claude Code to a dir-keyed Keychain entry.
func probeEnv(environ []string, dir, defaultDir string) []string {
	explicit := dir != "" && filepath.Clean(dir) != filepath.Clean(defaultDir)
	out := make([]string, 0, len(environ)+1)
	for _, kv := range environ {
		name, _, _ := strings.Cut(kv, "=")
		if name == "CLAUDE_CONFIG_DIR" || (explicit && slices.Contains(authOverrideEnv, name)) {
			continue
		}
		out = append(out, kv)
	}
	if explicit {
		out = append(out, "CLAUDE_CONFIG_DIR="+dir)
	}
	return out
}

func defaultClaudeDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude")
}

var nowFunc = time.Now

type authJSON struct {
	LoggedIn         bool   `json:"loggedIn"`
	Email            string `json:"email"`
	OrgName          string `json:"orgName"`
	SubscriptionType string `json:"subscriptionType"`
}

// AuthStatus runs `claude auth status` against dir with a 5s timeout. A
// logged-out account is reported as Status{LoggedIn:false} with a nil error
// when the command still emits parseable JSON.
func AuthStatus(ctx context.Context, dir string) (Status, error) {
	ctx, cancel := context.WithTimeout(ctx, statusTimeout)
	defer cancel()
	out, err := runAuthStatus(ctx, dir)
	var a authJSON
	if jerr := json.Unmarshal(out, &a); jerr != nil {
		if err != nil {
			return Status{}, fmt.Errorf("claude auth status: %w", err)
		}
		return Status{}, fmt.Errorf("claude auth status: parse output: %w", jerr)
	}
	return Status{LoggedIn: a.LoggedIn, Email: a.Email, Org: a.OrgName, Plan: a.SubscriptionType}, nil
}

type cacheEntry struct {
	status Status
	at     time.Time
}

var (
	cacheMu sync.Mutex
	cache   = map[string]cacheEntry{}
)

// CachedAuthStatus is AuthStatus with a short in-memory TTL keyed by dir.
// Failures are not cached.
func CachedAuthStatus(ctx context.Context, dir string) (Status, error) {
	cacheMu.Lock()
	if e, ok := cache[dir]; ok && nowFunc().Sub(e.at) < CacheTTL {
		cacheMu.Unlock()
		return e.status, nil
	}
	cacheMu.Unlock()

	st, err := AuthStatus(ctx, dir)
	if err != nil {
		uxlog.Log("[account] auth status failed for %s: %v", dir, err)
		return Status{}, err
	}
	cacheMu.Lock()
	cache[dir] = cacheEntry{status: st, at: nowFunc()}
	cacheMu.Unlock()
	return st, nil
}

// ResetCache drops all cached statuses.
func ResetCache() {
	cacheMu.Lock()
	cache = map[string]cacheEntry{}
	cacheMu.Unlock()
}
