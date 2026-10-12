package tui

import (
	"strings"
	"testing"

	"github.com/drn/argus/internal/hostsleep"
	"github.com/drn/argus/internal/testutil"
)

func TestSettingsAPIDetail_HostSleepWarning(t *testing.T) {
	atRisk := hostsleep.Report{Status: hostsleep.AtRisk, Message: "This Mac sleeps after 1 min idle on AC.", Fix: hostsleep.PermanentFix}

	tests := []struct {
		name      string
		enabled   bool
		report    hostsleep.Report
		wantWarn  bool
		wantLabel bool
	}{
		{"enabled and at risk warns", true, atRisk, true, true},
		{"api disabled never warns", false, atRisk, false, false},
		{"enabled and safe is silent", true, hostsleep.Report{Status: hostsleep.Safe}, false, false},
		{"enabled and held is silent", true, hostsleep.Report{Status: hostsleep.Held}, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sv := testSettingsView(t)
			sv.hostSleepMon = hostsleep.NewMonitorForTest(func(bool, int) hostsleep.Report { return tt.report })
			sv.apiEnabled = tt.enabled
			sv.apiPort = 7743
			sv.hostSleep = tt.report

			sim := drawSim(t)
			sv.renderAPIDetail(sim, 0, 0, 80, 20)

			col, _ := findScreenText(sim, "Host may sleep")
			testutil.Equal(t, col >= 0, tt.wantWarn)
			fixCol, _ := findScreenText(sim, "sudo pmset")
			testutil.Equal(t, fixCol >= 0, tt.wantWarn)
		})
	}
}

func TestSettingsAPIRow_HostSleepLabel(t *testing.T) {
	sv := testSettingsView(t)
	sv.apiEnabled = true
	sv.hostSleep = hostsleep.Report{Status: hostsleep.AtRisk}
	sv.setCategory(catRemoteAPI)
	sv.rebuildRows()

	var label string
	for _, r := range sv.rows {
		if r.kind == srAPI {
			label = r.label
		}
	}
	testutil.True(t, strings.Contains(label, "host may sleep"))
}

func TestWrapSettingsText(t *testing.T) {
	testutil.DeepEqual(t, wrapSettingsText("aaa bbb ccc", 7), []string{"aaa bbb", "ccc"})
	testutil.DeepEqual(t, wrapSettingsText("supercalifragilistic x", 5), []string{"supercalifragilistic", "x"})
	testutil.Equal(t, len(wrapSettingsText("", 10)), 0)
	testutil.Equal(t, len(wrapSettingsText("abc", 0)), 0)
}
