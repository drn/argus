package doctor

import (
	"errors"
	"strings"
	"testing"

	"github.com/drn/argus/internal/testutil"
)

func TestDiagnoseCleanupPeriod(t *testing.T) {
	tests := []struct {
		name    string
		days    *int
		readErr error
		want    CleanupPeriodStatus
	}{
		{name: "above default is OK", days: new(90), want: CleanupPeriodOK},
		{name: "unset is LOW", days: nil, want: CleanupPeriodLow},
		{name: "at default is LOW", days: new(30), want: CleanupPeriodLow},
		{name: "below default is LOW", days: new(1), want: CleanupPeriodLow},
		{name: "read error is UNKNOWN even with a value", days: new(90), readErr: errors.New("boom"), want: CleanupPeriodUnknown},
		{name: "read error is UNKNOWN", readErr: errors.New("boom"), want: CleanupPeriodUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DiagnoseCleanupPeriod(tt.days, tt.readErr)
			testutil.Equal(t, got, tt.want)
		})
	}
}

func TestRenderCleanupPeriod(t *testing.T) {
	tests := []struct {
		name         string
		status       CleanupPeriodStatus
		days         *int
		wantContains []string
		wantExcludes []string
	}{
		{
			name:         "OK",
			status:       CleanupPeriodOK,
			days:         new(3650),
			wantContains: []string{"OK", "3650"},
			wantExcludes: []string{"LOW", "UNKNOWN"},
		},
		{
			name:         "LOW unset",
			status:       CleanupPeriodLow,
			days:         nil,
			wantContains: []string{"LOW", "cleanupPeriodDays"},
			wantExcludes: []string{"UNKNOWN"},
		},
		{
			name:         "LOW explicit",
			status:       CleanupPeriodLow,
			days:         new(30),
			wantContains: []string{"LOW", "30"},
		},
		{
			name:         "UNKNOWN",
			status:       CleanupPeriodUnknown,
			wantContains: []string{"UNKNOWN"},
			wantExcludes: []string{"LOW", "OK"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RenderCleanupPeriod(tt.status, tt.days)
			for _, want := range tt.wantContains {
				if !strings.Contains(got, want) {
					t.Errorf("RenderCleanupPeriod(%v, %v) = %q, want substring %q", tt.status, tt.days, got, want)
				}
			}
			for _, unwanted := range tt.wantExcludes {
				if strings.Contains(got, unwanted) {
					t.Errorf("RenderCleanupPeriod(%v, %v) = %q, must NOT contain %q", tt.status, tt.days, got, unwanted)
				}
			}
		})
	}
}
