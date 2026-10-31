package voice

import (
	"os/exec"

	"github.com/drn/argus/internal/uxlog"
)

// The title and body travel as argv items so notification text is never
// interpolated into AppleScript source.
var notifyScript = []string{
	"-e", "on run argv",
	"-e", "display notification (item 2 of argv) with title (item 1 of argv)",
	"-e", "end run",
}

var runOsascript = func(args ...string) error {
	return exec.Command("osascript", args...).Run()
}

// NotifyMac posts a macOS user notification.
func NotifyMac(title, body string) {
	args := append(append([]string{}, notifyScript...), title, body)
	if err := runOsascript(args...); err != nil {
		uxlog.Log("[voice] notification failed title=%q: %v", title, err)
		return
	}
	uxlog.Log("[voice] notification posted title=%q", title)
}
