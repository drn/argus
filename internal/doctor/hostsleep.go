package doctor

import (
	"fmt"
	"strings"

	"github.com/drn/argus/internal/hostsleep"
)

// RenderHostSleep builds the host-sleep status block printed by `argus doctor`.
// Advisory only: it never affects the exit code, and a disabled remote API is a
// legitimate configuration, not a warning.
func RenderHostSleep(apiEnabled bool, r hostsleep.Report) string {
	var b strings.Builder
	b.WriteString("\nHost sleep (remote API / web app): ")
	switch {
	case !apiEnabled:
		b.WriteString("N/A (remote API disabled)\n")
	case r.Status == hostsleep.NotApplicable:
		b.WriteString("N/A (not macOS)\n")
	case r.Status == hostsleep.Safe:
		b.WriteString("OK (AC sleep disabled)\n")
	case r.Status == hostsleep.Held:
		fmt.Fprintf(&b, "HELD\n%s\nMake it permanent: %s\n", r.Message, hostsleep.PermanentFix)
	case r.Status == hostsleep.AtRisk:
		fmt.Fprintf(&b, "AT RISK\n%s\nPermanent fix: %s\nUntil next daemon restart: %s\n", r.Message, hostsleep.PermanentFix, hostsleep.TemporaryFix)
	default:
		fmt.Fprintf(&b, "UNKNOWN\n%s\n", r.Message)
	}
	return b.String()
}
