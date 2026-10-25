package bridge

import "sort"

// Override replaces only live bridge tasks in a scraper set. States must be
// resolved by the daemon first so all clients use the same input corroboration.
// Other backends and unavailable bridges retain their fallback behavior.
func Override(ids []string, states map[string]State, value func(State) bool) []string {
	if len(states) == 0 {
		return ids
	}
	set := make(map[string]bool, len(ids)+len(states))
	for _, id := range ids {
		set[id] = true
	}
	for id, st := range states {
		if value(st) {
			set[id] = true
		} else {
			delete(set, id)
		}
	}
	out := make([]string, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// Resolve corroborates only uncertain permission input. Busy remains owned by
// the live bridge, including while a hook-approved tool is still executing.
// scraperNeeds must be raw inference, never an earlier resolved verdict.
func Resolve(st State, scraperNeeds bool) State {
	if UncertainPermission(st) {
		st.NeedsInput = st.NeedsInput && scraperNeeds
	}
	return st
}

// UncertainPermission includes older reports without confidence. An exact
// permission signal is authoritative just like a question or plan dialog.
func UncertainPermission(st State) bool {
	return st.NeedsInputKind == KindPermission && st.NeedsInputConfidence != ConfidenceExact
}

// Quiescent matches the existing idle UI semantics around parked dialogs.
func Quiescent(st State) bool     { return !st.Busy || st.NeedsInput }
func AwaitingInput(st State) bool { return st.NeedsInput }
func Working(st State) bool       { return st.Busy && !st.NeedsInput }
