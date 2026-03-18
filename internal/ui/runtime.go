package ui

import (
	"fmt"
	"strings"
)

type Runtime string

const (
	RuntimeBubbleTea Runtime = "bubbletea"
	RuntimeTCell     Runtime = "tcell"
)

func ParseRuntime(v string) (Runtime, error) {
	normalized := strings.TrimSpace(strings.ToLower(v))
	if normalized == "" {
		return RuntimeBubbleTea, nil
	}

	switch Runtime(normalized) {
	case RuntimeBubbleTea:
		return RuntimeBubbleTea, nil
	case RuntimeTCell:
		return RuntimeTCell, nil
	default:
		return "", fmt.Errorf("unsupported ARGUS_UI_RUNTIME %q (expected bubbletea or tcell)", v)
	}
}
