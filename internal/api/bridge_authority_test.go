package api

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/bridge"
	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
	"github.com/drn/argus/internal/uxlog"
)

type bridgeTestSession struct {
	agent.SessionHandle
	generation string
	tail       []byte
}

func (s *bridgeTestSession) BridgeGeneration() string    { return s.generation }
func (*bridgeTestSession) Alive() bool                   { return true }
func (*bridgeTestSession) LastInput() time.Time          { return time.Time{} }
func (*bridgeTestSession) LastUserInput() time.Time      { return time.Time{} }
func (s *bridgeTestSession) RecentOutputTail(int) []byte { return s.tail }

type bridgeTestRunner struct {
	agent.SessionRunner
	id      string
	session *bridgeTestSession
}

func (r *bridgeTestRunner) Running() []string { return []string{r.id} }
func (r *bridgeTestRunner) RunningAndIdle() ([]string, []string) {
	return []string{r.id}, []string{r.id}
}
func (r *bridgeTestRunner) Get(id string) agent.SessionHandle {
	if id == r.id {
		return r.session
	}
	return nil
}
func (*bridgeTestRunner) HasPendingRestart(string) bool { return false }

func bridgeAuthorityServer(t *testing.T) (*Server, *bridgeTestRunner, *model.Task) {
	t.Helper()
	s, d := testServer(t)
	testutil.NoError(t, d.SetBackend("bridge-test", config.Backend{Command: "claude"}))
	task := &model.Task{Name: "bridge", Backend: "bridge-test", SessionID: "session", Status: model.StatusInProgress}
	testutil.NoError(t, d.Add(task))
	r := &bridgeTestRunner{SessionRunner: agent.NewRunner(nil), id: task.ID, session: &bridgeTestSession{generation: "launch"}}
	s.runner = r
	return s, r, task
}

func TestBridgeAuthorityWatcherAndRestart(t *testing.T) {
	s, r, task := bridgeAuthorityServer(t)
	now := time.Now()
	s.bridgeHub.Ingest(task.ID, []bridge.Event{{Type: bridge.TypeTurnStart, Generation: "launch", SessionID: "session", Boot: "b", Seq: 1}}, now)
	state := newIdleWatcherState()
	s.idleWatcherTick(state)
	testutil.False(t, state.idleNow[task.ID]) // raw runner reports idle
	s.bridgeHub.Ingest(task.ID, []bridge.Event{{Type: bridge.TypeNeedsInput, Generation: "launch", SessionID: "session", Boot: "b", Seq: 2}}, now)
	s.idleWatcherTick(state)
	testutil.True(t, state.idleNow[task.ID])
	testutil.True(t, state.needsInputNow[task.ID])
	s.bridgeHub.Ingest(task.ID, []bridge.Event{{Type: bridge.TypeCleared, Generation: "launch", SessionID: "session", Boot: "b", Seq: 3}}, now)
	s.idleWatcherTick(state)
	testutil.False(t, state.needsInputNow[task.ID])
	testutil.False(t, state.idleNow[task.ID])
	s.bridgeHub.Ingest(task.ID, []bridge.Event{{Type: bridge.TypeNeedsInput, Kind: bridge.KindPermission, Generation: "launch", SessionID: "session", Boot: "b", Seq: 4}}, now)
	s.idleWatcherTick(state)
	testutil.True(t, s.bridgeLive(task.ID, time.Now()))
	testutil.False(t, state.idleNow[task.ID])
	testutil.False(t, state.needsInputNow[task.ID])
	r.session.generation = "restarted"
	testutil.False(t, s.bridgeLive(task.ID, time.Now()))
	s.idleWatcherTick(state)
	testutil.True(t, state.idleNow[task.ID])
}

func TestBridgePermissionKeepsSourceAndCorroboratesOnlyInput(t *testing.T) {
	s, r, task := bridgeAuthorityServer(t)
	path := filepath.Join(t.TempDir(), "ux.log")
	testutil.NoError(t, uxlog.Init(path))
	t.Cleanup(uxlog.Close)
	now := time.Now()
	seq := int64(0)
	post := func(kind, confidence string) {
		t.Helper()
		seq++
		s.bridgeHub.Ingest(task.ID, []bridge.Event{{Type: bridge.TypeNeedsInput, Kind: kind, Confidence: confidence, Generation: "launch", SessionID: "session", Boot: "b", Seq: seq}}, now)
	}
	s.bridgeHub.Ingest(task.ID, []bridge.Event{{Type: bridge.TypeTurnStart, Generation: "launch", SessionID: "session", Boot: "b", Seq: seq}}, now)
	state := newIdleWatcherState()
	s.idleWatcherTick(state)
	for _, tc := range []struct {
		name, kind, confidence string
		tail                   []byte
		want                   bool
	}{
		{"hook approved", bridge.KindPermission, bridge.ConfidencePendingOrRunning, nil, false},
		{"exact permission", bridge.KindPermission, bridge.ConfidenceExact, nil, true},
		{"prior exact cannot corroborate", bridge.KindPermission, bridge.ConfidencePendingOrRunning, nil, false},
		{"visible dialog", bridge.KindPermission, bridge.ConfidencePendingOrRunning, blockedTail, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r.session.tail = tc.tail
			post(tc.kind, tc.confidence)
			s.idleWatcherTick(state)
			testutil.True(t, state.bridgeSource[task.ID])
			testutil.Equal(t, state.needsInputNow[task.ID], tc.want)
			testutil.Equal(t, state.idleNow[task.ID], tc.want)
			st := s.BridgeStates()[task.ID]
			testutil.True(t, st.Busy)
			testutil.Equal(t, st.NeedsInput, tc.want)
			route := s.bridgeDeliveryRoute(task.ID)
			testutil.True(t, route.Live)
			testutil.False(t, route.Idle) // delivery requires turn idle, not quiescence
			testutil.Equal(t, route.NeedsInput, tc.want)
			_, idle, needs := s.sessionStateMaps()
			testutil.Equal(t, idle[task.ID], tc.want)
			testutil.Equal(t, needs[task.ID], tc.want)
			w := httptest.NewRecorder()
			s.routes().ServeHTTP(w, authedReq("GET", "/api/bridge/states", ""))
			var states map[string]bridge.State
			testutil.NoError(t, json.Unmarshal(w.Body.Bytes(), &states))
			testutil.Equal(t, states[task.ID].NeedsInput, tc.want)
			w = httptest.NewRecorder()
			s.routes().ServeHTTP(w, authedReq("GET", "/api/status", ""))
			var status statusResponse
			testutil.NoError(t, json.Unmarshal(w.Body.Bytes(), &status))
			wantIdle := 0
			if tc.want {
				wantIdle = 1
			}
			testutil.Equal(t, status.Sessions.Idle, wantIdle)
		})
	}
	data, err := os.ReadFile(path)
	testutil.NoError(t, err)
	testutil.Equal(t, strings.Count(string(data), "[bridge] source"), 1)
	r.session.generation = "restarted"
	s.idleWatcherTick(state)
	data, err = os.ReadFile(path)
	testutil.NoError(t, err)
	testutil.Equal(t, strings.Count(string(data), "[bridge] source"), 2)
}

func TestBridgeRejectsOldLaunch(t *testing.T) {
	s, _, task := bridgeAuthorityServer(t)
	w := httptest.NewRecorder()
	s.routes().ServeHTTP(w, authedReq("POST", "/api/tasks/"+task.ID+"/bridge/events", `{"events":[{"type":"turn.start","seq":1,"boot":"old","generation":"previous","session_id":"session"}]}`))
	testutil.Equal(t, w.Code, 200)
	_, ok := s.bridgeHub.Get(task.ID)
	testutil.False(t, ok)
}

func TestBridgeDisabledRestoresScraperForRESTAndWatcher(t *testing.T) {
	s, _, task := bridgeAuthorityServer(t)
	post := func(seq int) {
		t.Helper()
		body := `{"events":[{"type":"needs_input","kind":"question","generation":"launch","session_id":"session","boot":"b","seq":` + strconv.Itoa(seq) + `}]}`
		w := httptest.NewRecorder()
		s.routes().ServeHTTP(w, authedReq("POST", "/api/tasks/"+task.ID+"/bridge/events", body))
		testutil.Equal(t, w.Code, 200)
	}
	post(1)
	state := newIdleWatcherState()
	s.idleWatcherTick(state)
	testutil.True(t, state.needsInputNow[task.ID])
	testutil.NoError(t, s.db.SetConfigValue("bridge.enabled", "false"))
	post(2) // already-running mods continue to post while disabled
	s.idleWatcherTick(state)
	testutil.False(t, state.needsInputNow[task.ID])
	testutil.True(t, state.idleNow[task.ID])
	running, idle, needs := s.sessionStateMaps()
	testutil.True(t, running[task.ID])
	testutil.True(t, idle[task.ID])
	testutil.False(t, needs[task.ID])
	w := httptest.NewRecorder()
	s.routes().ServeHTTP(w, authedReq("GET", "/api/bridge/states", ""))
	var states map[string]bridge.State
	testutil.NoError(t, json.Unmarshal(w.Body.Bytes(), &states))
	testutil.Equal(t, len(states), 0)
}

func TestBridgeMeasurementIngestStampsContextAndCost(t *testing.T) {
	s, _, task := bridgeAuthorityServer(t)
	orch, err := s.db.CreateHeraOrchestrator("bridge-cost", "")
	testutil.NoError(t, err)
	_, binding, err := s.db.CreateHeraRoleWithBinding(db.CreateHeraRoleInput{OrchestratorID: orch.ID, Name: "worker", Kind: db.HeraKindWorker}, task.ID, "/tmp/bridge")
	testutil.NoError(t, err)
	body := `{"events":[{"type":"session.measure","generation":"launch","session_id":"session","boot":"b","seq":1,"context":{"tokens":10000,"percent":5,"window":200000},"cost_usd":1.5}]}`
	for i := 0; i < 2; i++ {
		w := httptest.NewRecorder()
		s.routes().ServeHTTP(w, authedReq("POST", "/api/tasks/"+task.ID+"/bridge/events", body))
		testutil.Equal(t, w.Code, 200)
	}
	got, err := s.db.GetHeraBindingCostTotals(binding.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, got.CostUSDAccrued, 1.5)
	entries, err := s.db.ListMeta(task.ID, db.HeraMetaNamespace)
	testutil.NoError(t, err)
	meta := map[string]string{}
	for _, entry := range entries {
		meta[entry.Key] = entry.Value
	}
	testutil.Equal(t, meta[db.HeraMetaKeyContextSize], "10000")
	testutil.Equal(t, meta["context_percent"], "5")
	testutil.NoError(t, s.db.SetConfigValue("bridge.enabled", "false"))
	w := httptest.NewRecorder()
	s.routes().ServeHTTP(w, authedReq("POST", "/api/tasks/"+task.ID+"/bridge/events", `{"events":[{"type":"session.measure","generation":"launch","session_id":"session","boot":"b","seq":2,"cost_usd":20}]}`))
	testutil.Equal(t, w.Code, 200)
	got, err = s.db.GetHeraBindingCostTotals(binding.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, got.CostUSDAccrued, 1.5)
}

func TestBridgeUsageAccountAndFreshness(t *testing.T) {
	s, _, task := bridgeAuthorityServer(t)
	now := time.Now()
	s.bridgeHub.Ingest(task.ID, []bridge.Event{{Type: bridge.TypeMeasure, Generation: "launch", SessionID: "session", Boot: "b", Seq: 1, RateLimits: []bridge.RateLimit{{Kind: "seven_day", PercentUsed: 42, ResetsAt: now.Add(time.Hour).Format(time.RFC3339)}}}}, now)
	reading, ok := s.BridgeUsage("default")
	testutil.True(t, ok)
	testutil.Equal(t, reading.Percentage, 42.0)
	_, ok = s.BridgeUsage("work")
	testutil.False(t, ok)
	s.bridgeHub.Ingest(task.ID, []bridge.Event{{Type: bridge.TypeMeasure, Generation: "launch", SessionID: "session", Boot: "b", Seq: 2, RateLimits: []bridge.RateLimit{{Kind: "seven_day", PercentUsed: 80, ResetsAt: now.Add(time.Hour).Format(time.RFC3339)}}}}, now.Add(-2*time.Hour))
	s.bridgeHub.Ingest(task.ID, []bridge.Event{{Type: bridge.TypeHeartbeat, Generation: "launch", SessionID: "session", Boot: "b", Seq: 3}}, now)
	testutil.True(t, s.bridgeLive(task.ID, now))
	_, ok = s.BridgeUsage("")
	testutil.False(t, ok)
}
