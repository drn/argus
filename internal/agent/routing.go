package agent

import (
	"github.com/drn/argus/internal/backendtier"
	"github.com/drn/argus/internal/config"
)

// resolveTaskChoice shares backend precedence and carries the scored account
// into creation. Explicit/project accounts override tiers; global defaults do
// not prevent a tier from choosing an overflow identity.
func resolveTaskChoice(explicit, project string, cfg config.Config, account string) backendtier.Choice {
	backend := explicit
	if backend == "" {
		backend = cfg.Projects[project].Backend
	}
	pin := account
	if pin == "" {
		if p := cfg.ProjectAccounts[project]; p != "" && cfg.ValidateAccount(p) == nil {
			pin = p
		}
	}
	choice := backendtier.ResolveChoice(cfg, backend, pin)
	if choice.Backend != "" {
		return choice
	}
	if backend == "" {
		backend = cfg.Defaults.Backend
	}
	return backendtier.Choice{Backend: backend, Account: pin, Reason: "explicit/project/default backend; no eligible tier"}
}
