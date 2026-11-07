package tui

// noteWorkloadKey stands in for the user's keystroke reaching handleGlobalKey.
// Baseline (pre-change) code has no key signal, so it is a no-op there.
func noteWorkloadKey(*App) {}
