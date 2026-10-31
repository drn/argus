package tui

import "github.com/drn/argus/internal/config"

func (sv *SettingsView) cycleBackendTierAccount() bool {
	idx := sv.selectedTierIndex()
	if idx < 0 || !sv.backendTierEditable() {
		return false
	}
	cfg := sv.database.Config()
	tier := sv.backendTiers[idx]
	names := append([]string{""}, cfg.AccountNamesFor(cfg.Backends[tier.Backend].Command)...)
	next := names[0]
	for i, name := range names {
		if name == tier.Account {
			next = names[(i+1)%len(names)]
			break
		}
	}
	return sv.mutateBackendTiers(func(tiers []config.BackendTier) []config.BackendTier { tiers[idx].Account = next; return tiers })
}
