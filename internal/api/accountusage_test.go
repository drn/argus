package api

import (
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/drn/argus/internal/testutil"
)

func postMeasure(t *testing.T, s *Server, taskID string, seq int, limits string) {
	t.Helper()
	body := fmt.Sprintf(`{"events":[{"type":"session.measure","generation":"launch","session_id":"session","boot":"b","seq":%d,"rate_limits":%s}]}`, seq, limits)
	w := httptest.NewRecorder()
	s.routes().ServeHTTP(w, authedReq("POST", "/api/tasks/"+taskID+"/bridge/events", body))
	testutil.Equal(t, w.Code, 200)
}

func TestBridgeMeasureFeedsAccountLedger(t *testing.T) {
	t.Run("default account", func(t *testing.T) {
		s, _, task := bridgeAuthorityServer(t)
		fiveReset := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
		weekReset := time.Now().Add(72 * time.Hour).UTC().Format(time.RFC3339)
		postMeasure(t, s, task.ID, 1, `[{"kind":"five_hour","percent_used":100,"resets_at":"`+fiveReset+`"},{"kind":"seven_day","percent_used":75,"resets_at":"`+weekReset+`"}]`)

		snap := s.AccountUsage()
		u, ok := snap["default"]
		testutil.True(t, ok)
		testutil.NotNil(t, u.FiveHour)
		testutil.Equal(t, u.FiveHour.Pct, 100.0)
		testutil.Equal(t, u.FiveHour.ResetsAt.Format(time.RFC3339), fiveReset)
		testutil.NotNil(t, u.Weekly)
		testutil.Equal(t, u.Weekly.Pct, 75.0)
	})

	t.Run("named account, bad readings dropped", func(t *testing.T) {
		s, _, task := bridgeAuthorityServer(t)
		task.Account = "personal"
		testutil.NoError(t, s.db.Update(task))
		postMeasure(t, s, task.ID, 1, `[{"kind":"five_hour","percent_used":12,"resets_at":"`+time.Now().Add(time.Hour).UTC().Format(time.RFC3339)+`"},{"kind":"seven_day","percent_used":5,"resets_at":"not-a-time"}]`)

		snap := s.AccountUsage()
		_, hasDefault := snap["default"]
		testutil.False(t, hasDefault)
		testutil.Equal(t, snap["personal"].FiveHour.Pct, 12.0)
		testutil.Nil(t, snap["personal"].Weekly)
	})
}

func TestAccountUsage_NilServerSafe(t *testing.T) {
	var s *Server
	testutil.Equal(t, len(s.AccountUsage()), 0)
}
