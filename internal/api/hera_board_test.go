package api

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"testing"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/testutil"
)

func TestHandleHeraBoard(t *testing.T) {
	s, d := testServer(t)
	o, err := d.CreateHeraOrchestrator("board", "")
	testutil.NoError(t, err)
	c, err := d.CreateHeraRole(db.CreateHeraRoleInput{OrchestratorID: o.ID, Name: "coord", Kind: db.HeraKindCoordinator})
	testutil.NoError(t, err)
	w, err := d.CreateHeraRole(db.CreateHeraRoleInput{OrchestratorID: o.ID, Name: "worker", Kind: db.HeraKindWorker})
	testutil.NoError(t, err)
	i, err := d.PostHeraBoardItem(c.ID, "fyi", "Ready", "Body")
	testutil.NoError(t, err)
	for _, tc := range []struct {
		name, path string
		want       int
	}{
		{"valid", fmt.Sprintf("/api/hera/roles/%d/board", c.ID), 200},
		{"worker", fmt.Sprintf("/api/hera/roles/%d/board", w.ID), 400},
		{"missing", "/api/hera/roles/99999/board", 404},
		{"invalid", "/api/hera/roles/no/board", 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			s.routes().ServeHTTP(rr, authedReq("GET", tc.path, ""))
			testutil.Equal(t, rr.Code, tc.want)
			if tc.want == 200 {
				var got struct {
					Items  []*db.HeraBoardItem `json:"items"`
					Unread int                 `json:"unread_count"`
				}
				testutil.NoError(t, json.Unmarshal(rr.Body.Bytes(), &got))
				testutil.Equal(t, got.Unread, 1)
				testutil.Equal(t, got.Items[0].ID, i.ID)
			}
		})
	}
	roster := getHera(t, s)
	testutil.Equal(t, roster.Orchestrators[0].Roles[0].BoardUnread, 1)
	testutil.NoError(t, d.ReadHeraBoardItem(c.ID, i.ID))
	testutil.Equal(t, getHera(t, s).Orchestrators[0].Roles[0].BoardUnread, 0)
	testutil.NoError(t, d.ResolveHeraBoardItem(c.ID, i.ID, "agent", "done"))
	rr := httptest.NewRecorder()
	s.routes().ServeHTTP(rr, authedReq("GET", fmt.Sprintf("/api/hera/roles/%d/board?include_resolved=true", c.ID), ""))
	testutil.Contains(t, rr.Body.String(), `"resolved":true`)
}

func TestHandleHeraBoardUnavailable(t *testing.T) {
	s, d := testServer(t)
	o, err := d.CreateHeraOrchestrator("board", "")
	testutil.NoError(t, err)
	c, err := d.CreateHeraRole(db.CreateHeraRoleInput{OrchestratorID: o.ID, Name: "coord", Kind: db.HeraKindCoordinator})
	testutil.NoError(t, err)
	// Removing the table represents a store/schema failure, not an empty board.
	testutil.NoError(t, d.WithTx(func(tx *sql.Tx) error { _, err := tx.Exec(`DROP TABLE hera_board_items`); return err }))
	rr := httptest.NewRecorder()
	s.routes().ServeHTTP(rr, authedReq("GET", fmt.Sprintf("/api/hera/roles/%d/board", c.ID), ""))
	testutil.Equal(t, rr.Code, 500)
	testutil.Contains(t, rr.Body.String(), "board unavailable")
}
