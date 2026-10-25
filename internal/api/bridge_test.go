package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/drn/argus/internal/bridge"
	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

func TestHandleBridgeEvents(t *testing.T) {
	srv, d := testServer(t)
	mux := srv.routes()
	task := &model.Task{Name: "b", Status: model.StatusInProgress}
	testutil.NoError(t, d.Add(task))
	base := "/api/tasks/" + task.ID + "/bridge"

	post := func(url, body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, authedReq("POST", url, body))
		return w
	}

	t.Run("valid batch recorded", func(t *testing.T) {
		w := post(base+"/events", `{"events":[
			{"type":"session.start","seq":1,"boot":"b1","ts":1,"session_id":"s1","model":"opus","cwd":"/w"},
			{"type":"turn.start","seq":2,"boot":"b1","ts":2,"session_id":"s1","turn_id":"t1"}]}`)
		testutil.Equal(t, w.Code, http.StatusOK)
		var resp map[string]int
		testutil.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		testutil.Equal(t, resp["accepted"], 2)
		testutil.Equal(t, resp["received"], 2)

		gw := httptest.NewRecorder()
		mux.ServeHTTP(gw, authedReq("GET", base, ""))
		testutil.Equal(t, gw.Code, http.StatusOK)
		var st struct {
			bridge.State
			Live bool `json:"live"`
		}
		testutil.NoError(t, json.Unmarshal(gw.Body.Bytes(), &st))
		testutil.True(t, st.Busy)
		testutil.False(t, st.Live) // reports without a running matching launch are diagnostic only
		testutil.Equal(t, st.SessionID, "s1")
		testutil.Equal(t, st.Model, "opus")
	})

	t.Run("duplicates dropped", func(t *testing.T) {
		w := post(base+"/events", `{"events":[{"type":"turn.complete","seq":2,"boot":"b1"}]}`)
		testutil.Equal(t, w.Code, http.StatusOK)
		var resp map[string]int
		testutil.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		testutil.Equal(t, resp["accepted"], 0)
	})

	t.Run("unknown task", func(t *testing.T) {
		testutil.Equal(t, post("/api/tasks/nope/bridge/events", `{"events":[]}`).Code, http.StatusNotFound)
	})

	t.Run("malformed body", func(t *testing.T) {
		testutil.Equal(t, post(base+"/events", `{not json`).Code, http.StatusBadRequest)
	})

	t.Run("no state yet", func(t *testing.T) {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, authedReq("GET", "/api/tasks/other/bridge", ""))
		testutil.Equal(t, w.Code, http.StatusNotFound)
	})
}

func TestHandleBridgeEvents_RequiresAuth(t *testing.T) {
	srv, d := testServer(t)
	task := &model.Task{Name: "b", Status: model.StatusInProgress}
	testutil.NoError(t, d.Add(task))
	h := authMiddleware(srv.token, srv.db, srv.push, srv.routes(), "/")
	req := httptest.NewRequest("POST", "/api/tasks/"+task.ID+"/bridge/events", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	testutil.Equal(t, w.Code, http.StatusUnauthorized)
}

func TestHandleBridgeShadow(t *testing.T) {
	srv, _ := testServer(t)
	mux := srv.routes()
	srv.bridgeShadow.Observe(time.Now(), []string{"x"}, nil, nil) // no bridge for x
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, authedReq("GET", "/api/bridge/shadow", ""))
	testutil.Equal(t, w.Code, http.StatusOK)
	var st bridge.Stats
	testutil.NoError(t, json.Unmarshal(w.Body.Bytes(), &st))
	testutil.Equal(t, st.Signals[bridge.SignalIdle].NoBridge, int64(1))
	testutil.Equal(t, st.Signals[bridge.SignalNeedsInput].NoBridge, int64(1))
	testutil.Equal(t, st.SettleSeconds, bridge.DefaultSettle.Seconds())
}

func TestObserveBridge_RespectsConfigSwitch(t *testing.T) {
	srv, d := testServer(t)
	now := time.Now()

	srv.observeBridge(now, []string{"a"}, nil, nil)
	testutil.Equal(t, srv.bridgeShadow.Stats(now).Signals[bridge.SignalIdle].NoBridge, int64(1))

	testutil.NoError(t, d.SetConfigValue("bridge.enabled", "false"))
	testutil.False(t, d.Config().Bridge.Enabled)
	srv.observeBridge(now, []string{"a"}, nil, nil)
	testutil.Equal(t, srv.bridgeShadow.Stats(now).Signals[bridge.SignalIdle].NoBridge, int64(1))
}

// TestIdleWatcherTick_ObservesBridge pins the wiring AND the "shadow mode
// changes no behavior" contract: a tick with a running session hands it to the
// comparator (counted as no_bridge since the mod never reported), and the
// watcher's own verdict state and the runner's published needs-input set are
// identical to a tick with the bridge disabled.
func TestIdleWatcherTick_ObservesBridge(t *testing.T) {
	if testing.Short() {
		t.Skip("starts a real PTY-backed sleep; skipped in -short")
	}
	srv, d, _ := pushTestServer(t)
	testutil.NoError(t, d.SetBackend("sh-sleep", config.Backend{Command: "sleep 30"}))
	task := &model.Task{Name: "bridge-tick", Status: model.StatusInProgress, Backend: "sh-sleep", Worktree: t.TempDir()}
	testutil.NoError(t, d.Add(task))
	sess, err := srv.runner.Start(task, d.Config(), 24, 80, false)
	testutil.NoError(t, err)
	t.Cleanup(func() {
		_ = srv.runner.Stop(task.ID)
		<-sess.Done()
	})

	// Same session, same instant: one tick observed by the bridge, one not.
	observed := newIdleWatcherState()
	srv.idleWatcherTick(observed)
	st := srv.bridgeShadow.Stats(time.Now())
	testutil.Equal(t, st.Signals[bridge.SignalIdle].NoBridge, int64(1))
	testutil.Equal(t, st.Signals[bridge.SignalNeedsInput].NoBridge, int64(1))
	_, observedIdle, observedNeeds := srv.sessionStateMaps()

	testutil.NoError(t, d.SetConfigValue("bridge.enabled", "false"))
	plain := newIdleWatcherState()
	srv.idleWatcherTick(plain)
	_, plainIdle, plainNeeds := srv.sessionStateMaps()
	// The disabled tick must not have been observed...
	testutil.Equal(t, srv.bridgeShadow.Stats(time.Now()).Signals[bridge.SignalIdle].NoBridge, int64(1))
	// ...and every verdict the watcher itself keeps is identical either way.
	testutil.DeepEqual(t, observed.idleNow, plain.idleNow)
	testutil.DeepEqual(t, observed.seenBefore, plain.seenBefore)
	testutil.DeepEqual(t, observed.needsInputNow, plain.needsInputNow)
	testutil.DeepEqual(t, observedIdle, plainIdle)
	testutil.DeepEqual(t, observedNeeds, plainNeeds)
}

func TestHandleBridgeEvents_OversizeBodyRejected(t *testing.T) {
	srv, d := testServer(t)
	mux := srv.routes()
	task := &model.Task{Name: "b", Status: model.StatusInProgress}
	testutil.NoError(t, d.Add(task))
	big := `{"events":[{"type":"heartbeat","seq":1,"boot":"b","session_id":"` + strings.Repeat("x", maxBridgeBody) + `"}]}`
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, authedReq("POST", "/api/tasks/"+task.ID+"/bridge/events", big))
	testutil.Equal(t, w.Code, http.StatusBadRequest)
	_, ok := srv.bridgeHub.Get(task.ID)
	testutil.False(t, ok)
}
