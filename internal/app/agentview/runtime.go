package agentview

import (
	"fmt"
	"strings"
)

// UIRuntime identifies which UI backend is active.
type UIRuntime string

const (
	// RuntimeBubbleTea is the current Bubble Tea + lipgloss renderer.
	RuntimeBubbleTea UIRuntime = "bubbletea"

	// RuntimeTcell is the planned tcell/tview renderer with native terminal
	// passthrough for the agent pane.
	RuntimeTcell UIRuntime = "tcell"
)

// EnvUIRuntime is the environment variable that selects the UI runtime.
const EnvUIRuntime = "ARGUS_UI_RUNTIME"

// ParseRuntime parses a runtime string value. Empty or unset defaults to
// RuntimeBubbleTea. Set to "tcell" for the native terminal passthrough runtime.
// Unknown values return an error (fail-fast).
// Input is normalized to lowercase with whitespace trimmed.
func ParseRuntime(v string) (UIRuntime, error) {
	normalized := strings.TrimSpace(strings.ToLower(v))
	if normalized == "" {
		return RuntimeBubbleTea, nil
	}

	switch UIRuntime(normalized) {
	case RuntimeBubbleTea:
		return RuntimeBubbleTea, nil
	case RuntimeTcell:
		return RuntimeTcell, nil
	default:
		return "", fmt.Errorf("unsupported %s %q (expected bubbletea or tcell)", EnvUIRuntime, v)
	}
}
