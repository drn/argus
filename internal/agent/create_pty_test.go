package agent

import (
	"testing"
	"time"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/testutil"
)

func TestCreateAndStart_ReportedPTYSize(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repo := initGitRepo(t)
	d := createTestDB(t, repo)
	testutil.NoError(t, d.PublishInitialPTYLayout(db.InitialPTYLayout{ReportedAt: time.Now(), Active: db.PTYSize{Rows: 30, Cols: 99}, Coordinator: db.PTYSize{Rows: 30, Cols: 98}, Worker: db.PTYSize{Rows: 30, Cols: 99}}))
	for _, tc := range []struct {
		name                           string
		role                           db.HeraRoleKind
		rows, cols, wantRows, wantCols uint16
	}{
		{"coordinator", db.HeraKindCoordinator, 0, 0, 30, 98},
		{"worker", db.HeraKindWorker, 0, 0, 30, 99},
		{"headless", "", 0, 0, 30, 99},
		{"explicit", db.HeraKindWorker, 40, 120, 40, 120},
		{"partial", db.HeraKindCoordinator, 40, 0, 40, 98},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &fakeRunner{sessionPID: 202}
			_, _, err := CreateAndStart(d, r, CreateInput{Name: tc.name, Project: "proj", PTYRole: tc.role, Rows: tc.rows, Cols: tc.cols})
			testutil.NoError(t, err)
			testutil.Equal(t, r.startRows, tc.wantRows)
			testutil.Equal(t, r.startCols, tc.wantCols)
			testutil.Equal(t, ShouldKickRerender(true, int(r.startCols), 0, int(tc.wantCols), true, false, false), RerenderSkip)
		})
	}
}

func TestHeraSpawn_ReportedPTYSize(t *testing.T) {
	repo := initGitRepo(t)
	d := createTestDB(t, repo)
	testutil.NoError(t, d.PublishInitialPTYLayout(db.InitialPTYLayout{ReportedAt: time.Now(), Active: db.PTYSize{Rows: 30, Cols: 150}, Coordinator: db.PTYSize{Rows: 30, Cols: 98}, Worker: db.PTYSize{Rows: 30, Cols: 99}}))
	r := &fakeRunner{sessionPID: 202}
	coord, err := SpawnHeraCoordinator(d, r, HeraCoordinatorSpawnInput{OrchestratorBaseName: "sized", CoordRoleName: "coord", Project: "proj", Backend: "test", TaskPrompt: "coordinate"})
	testutil.NoError(t, err)
	testutil.Equal(t, r.startCols, uint16(98))
	_, err = SpawnHeraWorker(d, r, HeraWorkerSpawnInput{OrchestratorID: coord.Orchestrator.ID, BaseName: "worker", Project: "proj", Backend: "test", TaskPrompt: "work"})
	testutil.NoError(t, err)
	testutil.Equal(t, r.startCols, uint16(99))
}

func TestHeraMaterialize_ReportedPTYSize(t *testing.T) {
	for _, subcoord := range []bool{false, true} {
		name := "worker"
		if subcoord {
			name = "subcoord"
		}
		t.Run(name, func(t *testing.T) {
			repo := initGitRepo(t)
			d := createTestDB(t, repo)
			testutil.NoError(t, d.PublishInitialPTYLayout(db.InitialPTYLayout{ReportedAt: time.Now(), Active: db.PTYSize{Rows: 30, Cols: 150}, Coordinator: db.PTYSize{Rows: 30, Cols: 98}, Worker: db.PTYSize{Rows: 30, Cols: 99}}))
			orch, err := d.CreateHeraOrchestrator("sized", "")
			testutil.NoError(t, err)
			kind := db.HeraNodeKindWorker
			if subcoord {
				kind = db.HeraNodeKindSubCoord
			}
			role, err := d.CreateHeraPlannedRole(db.CreateHeraRoleInput{OrchestratorID: orch.ID, Name: name, ArgusProject: "proj", Prompt: "work", NodeKind: kind})
			testutil.NoError(t, err)
			r := &fakeRunner{sessionPID: 202}
			in := HeraMaterializeInput{Role: role, TaskPrompt: "work", Project: "proj"}
			want := uint16(99)
			if subcoord {
				_, err = MaterializeHeraSubCoordinator(d, r, in)
				want = 98
			} else {
				_, err = MaterializeHeraWorker(d, r, in)
			}
			testutil.NoError(t, err)
			testutil.Equal(t, r.startCols, want)
		})
	}
}
