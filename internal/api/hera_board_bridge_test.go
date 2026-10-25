package api

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/drn/argus/internal/bridge"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/notify"
	"github.com/drn/argus/internal/testutil"
)

func TestBoardDismissalNotifyTick(t *testing.T) {
	for _, live := range []bool{true, false} {
		t.Run(fmt.Sprintf("bridge=%t", live), func(t *testing.T) {
			s, _, task := bridgeAuthorityServer(t)
			orch, err := s.db.CreateHeraOrchestrator("board", "")
			testutil.NoError(t, err)
			role, _, err := s.db.CreateHeraRoleWithBinding(db.CreateHeraRoleInput{OrchestratorID: orch.ID, Name: "coord", Kind: db.HeraKindCoordinator}, task.ID, task.Worktree)
			testutil.NoError(t, err)
			item, err := s.db.PostHeraBoardItem(role.ID, "decision", "Choose a release", "body")
			testutil.NoError(t, err)
			// Commit before notifier creation: the outbox must survive this gap.
			testutil.NoError(t, s.db.ResolveHeraBoardItem(role.ID, item.ID, "human", ""))
			testutil.NoError(t, s.db.ResolveHeraBoardItem(role.ID, item.ID, "human", ""))
			sess := &notifyIdleSession{}
			s.SetNotifier(notify.New(notify.AdaptRunner(func(string) notify.SessionHandleIface { return sess }), notifyNoFocus{}))
			if live {
				s.bridgeHub.Ingest(task.ID, []bridge.Event{{Type: bridge.TypeTurnComplete, Generation: "launch", SessionID: "session", Boot: "b", Seq: 1}}, time.Now())
			}
			s.idleWatcherTick(newIdleWatcherState())
			mail, err := s.db.HeraInbox(role.ID)
			testutil.NoError(t, err)
			testutil.Equal(t, len(mail), 1)
			testutil.Equal(t, mail[0].DeliveryMode, db.HeraDeliveryIdleSubmit)
			if live {
				claim := s.notifier.ClaimBridge(task.ID, "launch", "b", time.Now())
				testutil.NotNil(t, claim)
				testutil.Contains(t, claim.Text, "[hera from Aaron (board)]")
				testutil.Contains(t, claim.Text, fmt.Sprintf("board decision #%d", item.ID))
				testutil.Equal(t, len(sess.writes), 0)
			} else {
				testutil.Equal(t, len(sess.writes), 3)
				testutil.Contains(t, string(sess.writes[1]), "[hera from Aaron (board)]")
			}
			w := httptest.NewRecorder()
			s.routes().ServeHTTP(w, authedReq("GET", "/api/tasks/"+task.ID+"/bridge/ui", ""))
			testutil.Equal(t, w.Code, 200)
			var got bridgeUISnapshot
			testutil.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
			testutil.Equal(t, got.Messages[0].Sender, "Aaron (board)")
			testutil.Equal(t, got.Unread, 1)
			testutil.Equal(t, got.Bindings[0].BoardUnread, 0)
		})
	}
}

func TestBridgeUIBoardSeparateFromMail(t *testing.T) {
	s, _, task := bridgeAuthorityServer(t)
	orch, err := s.db.CreateHeraOrchestrator("board", "")
	testutil.NoError(t, err)
	role, _, err := s.db.CreateHeraRoleWithBinding(db.CreateHeraRoleInput{OrchestratorID: orch.ID, Name: "coord", Kind: db.HeraKindCoordinator}, task.ID, task.Worktree)
	testutil.NoError(t, err)
	first, err := s.db.PostHeraBoardItem(role.ID, "decision", "Older", "body")
	testutil.NoError(t, err)
	_, err = s.db.PostHeraBoardItem(role.ID, "fyi", "Newer", "body")
	testutil.NoError(t, err)
	_, err = s.db.UpdateHeraBoardItem(role.ID, first.ID, "decision", "Newest edit", "updated")
	testutil.NoError(t, err)
	read, err := s.db.PostHeraBoardItem(role.ID, "fyi", "Read item", "body")
	testutil.NoError(t, err)
	testutil.NoError(t, s.db.ReadHeraBoardItem(role.ID, read.ID))
	resolved, err := s.db.PostHeraBoardItem(role.ID, "fyi", "Resolved item", "body")
	testutil.NoError(t, err)
	testutil.NoError(t, s.db.ResolveHeraBoardItem(role.ID, resolved.ID, "agent", ""))
	for i := 0; i < 2; i++ {
		w := httptest.NewRecorder()
		s.routes().ServeHTTP(w, authedReq("GET", "/api/tasks/"+task.ID+"/bridge/ui", ""))
		testutil.Equal(t, w.Code, 200)
		var got bridgeUISnapshot
		testutil.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
		testutil.Equal(t, got.Unread, 0)
		testutil.Equal(t, got.Bindings[0].BoardUnread, 2)
		testutil.Equal(t, got.Bindings[0].BoardSubject, "Newest edit")
	}
	testutil.NoError(t, s.db.WithTx(func(tx *sql.Tx) error { _, err := tx.Exec(`DROP TABLE hera_board_items`); return err }))
	w := httptest.NewRecorder()
	s.routes().ServeHTTP(w, authedReq("GET", "/api/tasks/"+task.ID+"/bridge/ui", ""))
	testutil.Equal(t, w.Code, 200)
	var got bridgeUISnapshot
	testutil.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	testutil.Equal(t, got.Bindings[0].BoardUnread, 0)
	testutil.Equal(t, got.Bindings[0].BoardSubject, "")
}
