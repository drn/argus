package bridge

import "sort"

// Override replaces only the live bridge tasks in a scraper set. Other
// backends and unavailable bridges retain their complete fallback behavior.
func Override(ids []string, states map[string]State, value func(State) bool) []string {
	if len(states) == 0 {
		return ids
	}
	set := make(map[string]bool, len(ids)+len(states))
	for _, id := range ids {
		set[id] = true
	}
	for id, st := range states {
		if !Authoritative(st) {
			continue
		}
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

// Authoritative excludes permission intervals: tool.call also spans execution
// after approval, so only the scraper can corroborate that dialog's lifetime.
func Authoritative(st State) bool { return !st.NeedsInput || st.NeedsInputKind != KindPermission }

// Quiescent matches the existing idle UI semantics around parked dialogs.
func Quiescent(st State) bool     { return !st.Busy || st.NeedsInput }
func AwaitingInput(st State) bool { return st.NeedsInput }
func Working(st State) bool       { return st.Busy && !st.NeedsInput }
