package tui

import (
	"os/exec"
	"strings"
)

// pbcopyWriter is the production OS clipboard writer wired into `App.clipboardWriter`
// by `New()`. Isolated in its own file so it can be excluded from the coverage
// gate (see `coverage-ignore.txt`) — every code path through it shells out to
// the real `pbcopy`, which is exactly what tests must NOT do.
//
// macOS-only: pbcopy is the same fence the existing TUI clipboard precedent
// (OnCopyPrompt) lives behind; cross-platform support is a follow-up.
func pbcopyWriter(text string) error {
	cmd := exec.Command("pbcopy")
	cmd.Stdin = strings.NewReader(text)
	return cmd.Run()
}
