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
	layout := InitialPTYLayout{ReportedAt: now, Active: PTYSize{Rows: 30, Cols: 150}, Coordinator: PTYSize{Rows: 30, Cols: 98}, Worker: PTYSize{Rows: 30, Cols: 99}}
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
	for _, role := range []HeraRoleKind{HeraKindCoordinator, HeraKindWorker, HeraKindFreelance} {
		t.Run("active only "+string(role), func(t *testing.T) {
			got, err := d.InitialPTYSize(role, now)
			testutil.NoError(t, err)
			testutil.Equal(t, got, PTYSize{})
		})
	}
	layout.Coordinator = PTYSize{Rows: 4, Cols: 98}
	layout.Worker = PTYSize{Rows: 30, Cols: 19}
	testutil.NoError(t, d.PublishInitialPTYLayout(layout))
	for _, role := range []HeraRoleKind{HeraKindCoordinator, HeraKindWorker, HeraKindFreelance} {
		t.Run("invalid role size "+string(role), func(t *testing.T) {
			got, err := d.InitialPTYSize(role, now)
			testutil.NoError(t, err)
			testutil.Equal(t, got, PTYSize{})
		})
	}
	testutil.NoError(t, d.SetConfigValue(initialPTYLayoutKey, "bad-json"))
	_, err = d.InitialPTYSize(HeraKindWorker, now)
	testutil.Error(t, err)
	testutil.Error(t, d.PublishInitialPTYLayout(InitialPTYLayout{}))
}

func TestInitialPTYValidation(t *testing.T) {
	testutil.Equal(t, InitialPTYReportTTL, 2*time.Minute)
	for _, tc := range []struct {
		name  string
		size  PTYSize
		valid bool
	}{
		{"minimum", PTYSize{5, 20}, true},
		{"short", PTYSize{4, 20}, false},
		{"narrow", PTYSize{5, 19}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			testutil.Equal(t, tc.size.Valid(), tc.valid)
			d, err := OpenInMemory()
			testutil.NoError(t, err)
			t.Cleanup(func() { _ = d.Close() })
			err = d.PublishInitialPTYLayout(InitialPTYLayout{Active: tc.size, ReportedAt: time.Now()})
			if tc.valid {
				testutil.NoError(t, err)
			} else {
				testutil.Error(t, err)
			}
		})
	}
	d, err := OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })
	testutil.Error(t, d.PublishInitialPTYLayout(InitialPTYLayout{Active: PTYSize{5, 20}}))
}
