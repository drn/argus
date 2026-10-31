package api

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/drn/argus/internal/accountusage"
	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/claudeaccount"
	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/uxlog"
)

// claudeAuthStatus is the sign-in lookup seam; tests replace it.
var claudeAuthStatus = claudeaccount.CachedAuthStatus

// codexLoginStatus is the Codex sign-in lookup seam; tests replace it.
var codexLoginStatus = agent.CodexLoginStatus

const claudeAuthLookupTimeout = 3 * time.Second

// accountSupportsJSON reports which tools an account can run.
type accountSupportsJSON struct {
	Claude bool `json:"claude"`
	Codex  bool `json:"codex"`
}

// accountJSON is one entry of GET /api/accounts. It carries identity labels
// only, never credentials; logged_in/email/org/plan describe the Claude
// sign-in and stay empty for an account without a Claude config dir.
// codex_logged_in is the Codex sign-in, absent when unknown or unsupported.
type accountJSON struct {
	Name            string              `json:"name"`
	Label           string              `json:"label"`
	ClaudeConfigDir string              `json:"claude_config_dir"`
	CodexHome       string              `json:"codex_home"`
	Supports        accountSupportsJSON `json:"supports"`
	IsDefault       bool                `json:"is_default"`
	LoggedIn        bool                `json:"logged_in"`
	Email           string              `json:"email,omitempty"`
	Org             string              `json:"org,omitempty"`
	Plan            string              `json:"plan,omitempty"`
	CodexLoggedIn   *bool               `json:"codex_logged_in,omitempty"`
	Usage           *accountUsageJSON   `json:"usage,omitempty"`
}

// accountUsageJSON is an account's currently valid Claude plan usage from the
// daemon's ledger; a window with no valid reading is omitted.
type accountUsageJSON struct {
	FiveHourPct      *float64 `json:"five_hour_pct,omitempty"`
	FiveHourResetsAt string   `json:"five_hour_resets_at,omitempty"`
	WeeklyPct        *float64 `json:"weekly_pct,omitempty"`
	WeeklyResetsAt   string   `json:"weekly_resets_at,omitempty"`
}

// autoAccountLabel labels the "auto" pseudo-account in pickers.
const autoAccountLabel = "Auto (most headroom)"

func usageJSON(u accountusage.Usage) *accountUsageJSON {
	if u.FiveHour == nil && u.Weekly == nil {
		return nil
	}
	out := &accountUsageJSON{}
	if r := u.FiveHour; r != nil {
		pct := r.Pct
		out.FiveHourPct, out.FiveHourResetsAt = &pct, r.ResetsAt.UTC().Format(time.RFC3339)
	}
	if r := u.Weekly; r != nil {
		pct := r.Pct
		out.WeeklyPct, out.WeeklyResetsAt = &pct, r.ResetsAt.UTC().Format(time.RFC3339)
	}
	return out
}

// handleListAccounts lists the built-in default account followed by the
// configured ones. is_default marks the account a new task would resolve to
// with no explicit choice: for ?project=<name> that project's default, else the
// global default. Claude sign-in lookups run in parallel under a short
// timeout and degrade to logged_in:false.
func (s *Server) handleListAccounts(w http.ResponseWriter, r *http.Request) {
	cfg := s.db.Config()
	names := cfg.AccountNames()
	defName := cfg.ResolveAccount("", r.URL.Query().Get("project"))
	out := make([]accountJSON, len(names))

	ctx, cancel := context.WithTimeout(r.Context(), claudeAuthLookupTimeout)
	defer cancel()

	var wg sync.WaitGroup
	for i, name := range names {
		if err := cfg.ValidateAccount(name); err != nil {
			uxlog.Log("[account] list: skipping %q: %v", name, err)
			continue
		}
		label := cfg.Accounts[name].Label
		if label == "" {
			label = name
		}
		claudeDir, _, claudeErr := cfg.ClaudeConfigDir(name)
		codexHome, _, codexErr := cfg.CodexHome(name)
		out[i] = accountJSON{
			Name:            name,
			Label:           label,
			ClaudeConfigDir: claudeDir,
			CodexHome:       codexHome,
			Supports:        accountSupportsJSON{Claude: claudeErr == nil, Codex: codexErr == nil},
			IsDefault:       name == defName,
		}
		if codexErr == nil {
			source := ""
			if name != config.DefaultAccountName {
				source = codexHome
			}
			wg.Add(1)
			go func(i int, source string) {
				defer wg.Done()
				ok, err := codexLoginStatus(ctx, source)
				if err != nil {
					uxlog.Log("[account] list: codex sign-in lookup failed for %q: %v", out[i].Name, err)
					return
				}
				out[i].CodexLoggedIn = &ok
			}(i, source)
		}
		if claudeErr != nil {
			continue
		}
		wg.Add(1)
		go func(i int, dir string) {
			defer wg.Done()
			st, err := claudeAuthStatus(ctx, dir)
			if err != nil {
				uxlog.Log("[account] list: sign-in lookup failed for %q: %v", out[i].Name, err)
				return
			}
			out[i].LoggedIn, out[i].Email, out[i].Org, out[i].Plan = st.LoggedIn, st.Email, st.Org, st.Plan
		}(i, claudeDir)
	}
	wg.Wait()

	usage := s.AccountUsage()
	res := make([]accountJSON, 0, len(out)+1)
	for _, a := range out {
		if a.Name == "" {
			continue
		}
		a.Usage = usageJSON(usage[accountusage.NormalizeAccount(a.Name)])
		res = append(res, a)
		if a.Name == config.DefaultAccountName && len(names) > 1 {
			res = append(res, autoAccountJSON(cfg, names, defName))
		}
	}
	writeJSON(w, http.StatusOK, res)
}

// autoAccountJSON is the "auto" pseudo-account entry: no directories, and it
// supports a tool when any configured account does.
func autoAccountJSON(cfg config.Config, names []string, defName string) accountJSON {
	a := accountJSON{Name: config.AutoAccountName, Label: autoAccountLabel, IsDefault: config.IsAutoAccount(defName)}
	for _, n := range names {
		if n == config.DefaultAccountName {
			continue
		}
		if _, _, err := cfg.ClaudeConfigDir(n); err == nil {
			a.Supports.Claude = true
		}
		if _, _, err := cfg.CodexHome(n); err == nil {
			a.Supports.Codex = true
		}
	}
	return a
}
