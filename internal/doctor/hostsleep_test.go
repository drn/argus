package doctor

import (
	"strings"
	"testing"

	"github.com/drn/argus/internal/hostsleep"
	"github.com/drn/argus/internal/testutil"
)

func TestRenderHostSleep(t *testing.T) {
	tests := []struct {
		name    string
		api     bool
		report  hostsleep.Report
		want    []string
		notWant []string
	}{
		{"api disabled is fine", false, hostsleep.Report{Status: hostsleep.AtRisk}, []string{"N/A (remote API disabled)"}, []string{"AT RISK", "pmset"}},
		{"not macos", true, hostsleep.Report{Status: hostsleep.NotApplicable}, []string{"N/A (not macOS)"}, nil},
		{"safe", true, hostsleep.Report{Status: hostsleep.Safe}, []string{"OK"}, []string{"pmset"}},
		{"held", true, hostsleep.Report{Status: hostsleep.Held, Message: "held msg"}, []string{"HELD", "held msg", hostsleep.PermanentFix}, nil},
		{"at risk", true, hostsleep.Report{Status: hostsleep.AtRisk, Message: "risk msg"}, []string{"AT RISK", "risk msg", hostsleep.PermanentFix, hostsleep.TemporaryFix}, nil},
		{"unknown", true, hostsleep.Report{Status: hostsleep.Unknown, Message: "unk msg"}, []string{"UNKNOWN", "unk msg"}, []string{"AT RISK"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := RenderHostSleep(tt.api, tt.report)
			for _, w := range tt.want {
				testutil.Contains(t, out, w)
			}
			for _, w := range tt.notWant {
				testutil.False(t, strings.Contains(out, w))
			}
		})
	}
}
