package doctor

import (
	"errors"
	"strings"
	"testing"

	"github.com/drn/argus/internal/testutil"
)

func TestDiagnoseDevStackOrphans(t *testing.T) {
	tests := []struct {
		name       string
		orphans    []DevStackOrphan
		scanErr    error
		wantStatus DevStackOrphanStatus
		wantCount  int
	}{
		{
			name:       "none: scan ran cleanly, nothing found",
			wantStatus: DevStackOrphanNone,
		},
		{
			name:       "found: one or more orphans",
			orphans:    []DevStackOrphan{{PID: 123, Name: "mysqld", WorktreePath: "/x/deleted"}},
			wantStatus: DevStackOrphanFound,
			wantCount:  1,
		},
		{
			name:       "unknown: scan itself failed",
			scanErr:    errors.New("exec: \"pgrep\": executable file not found in $PATH"),
			orphans:    []DevStackOrphan{{PID: 123, Name: "mysqld", WorktreePath: "/x/deleted"}},
			wantStatus: DevStackOrphanUnknown,
			wantCount:  0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, orphans := DiagnoseDevStackOrphans(tt.orphans, tt.scanErr)
			testutil.Equal(t, status, tt.wantStatus)
			testutil.Equal(t, len(orphans), tt.wantCount)
		})
	}
}

func TestRenderDevStackOrphans(t *testing.T) {
	tests := []struct {
		name         string
		status       DevStackOrphanStatus
		orphans      []DevStackOrphan
		wantContains []string
	}{
		{
			name:         "none found",
			status:       DevStackOrphanNone,
			wantContains: []string{"NONE FOUND"},
		},
		{
			name:   "found lists pid, name, and worktree path",
			status: DevStackOrphanFound,
			orphans: []DevStackOrphan{
				{PID: 4242, Name: "mysqld", WorktreePath: "/Users/aaron/.argus/worktrees/proj/deleted-task"},
			},
			wantContains: []string{"FOUND (1)", "4242", "mysqld", "/Users/aaron/.argus/worktrees/proj/deleted-task"},
		},
		{
			name:         "unknown names the scanning mechanism",
			status:       DevStackOrphanUnknown,
			wantContains: []string{"UNKNOWN", "pgrep"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RenderDevStackOrphans(tt.status, tt.orphans)
			for _, want := range tt.wantContains {
				if !strings.Contains(got, want) {
					t.Errorf("RenderDevStackOrphans(%v, %v) = %q, want substring %q", tt.status, tt.orphans, got, want)
				}
			}
		})
	}
}
