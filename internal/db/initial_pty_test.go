package db

import (
	"testing"
	"time"

	"github.com/drn/argus/internal/testutil"
)

func TestInitialPTYSize(t *testing.T) {
	d, err := OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })
	now := time.Date(2026, 10, 17, 0, 0, 0, 0, time.UTC)
	layout := InitialPTYLayout{ReportedAt: now, Active: PTYSize{Rows: 30, Cols: 99}, Coordinator: PTYSize{Rows: 30, Cols: 98}, Worker: PTYSize{Rows: 30, Cols: 99}}
	for _, tc := range []struct {
		name string
		role HeraRoleKind
		at   time.Time
		seed bool
		want PTYSize
	}{
		{"missing report", HeraKindWorker, now, false, PTYSize{}},
		{"coordinator", HeraKindCoordinator, now, true, layout.Coordinator},
		{"worker", HeraKindWorker, now, true, layout.Worker},
		{"freelance", HeraKindFreelance, now, true, layout.Worker},
		{"headless active", "", now, true, layout.Active},
		{"expired", HeraKindWorker, now.Add(InitialPTYReportTTL), true, PTYSize{}},
		{"clock moved backward", HeraKindWorker, now.Add(-time.Second), true, PTYSize{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.seed {
				testutil.NoError(t, d.PublishInitialPTYLayout(layout))
			}
			got, err := d.InitialPTYSize(tc.role, tc.at)
			testutil.NoError(t, err)
			testutil.Equal(t, got, tc.want)
		})
	}
	testutil.NoError(t, d.PublishInitialPTYLayout(InitialPTYLayout{ReportedAt: now, Active: layout.Active}))
	got, err := d.InitialPTYSize(HeraKindCoordinator, now)
	testutil.NoError(t, err)
	testutil.Equal(t, got, layout.Active)
	testutil.NoError(t, d.SetConfigValue(initialPTYLayoutKey, "bad-json"))
	_, err = d.InitialPTYSize(HeraKindWorker, now)
	testutil.Error(t, err)
	testutil.Error(t, d.PublishInitialPTYLayout(InitialPTYLayout{}))
}
