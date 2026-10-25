package api

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/drn/argus/internal/bridge"
	"github.com/drn/argus/internal/claudesession"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/notify"
	"github.com/drn/argus/internal/testutil"
)

func TestBridgeDeliveryEndpoints(t *testing.T) {
	s, _, task := bridgeAuthorityServer(t)
	s.SetNotifier(notify.New(notify.AdaptRunner(func(string) notify.SessionHandleIface { return nil }), notify.NewFocusTracker(nil)))
	s.bridgeHub.Ingest(task.ID, []bridge.Event{{Type: bridge.TypeTurnComplete, Generation: "launch", SessionID: "session", Boot: "b", Seq: 1}}, time.Now())
	s.notifier.ReliableNotify(task.ID, "notice", "hera:1", notify.NotifyOpts{})
	call := func(path, body string) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		s.routes().ServeHTTP(w, authedReq("POST", "/api/tasks/"+task.ID+"/bridge/"+path, body))
		return w
	}
	w := call("deliveries", `{"generation":"launch","boot":"b"}`)
	testutil.Equal(t, w.Code, 200)
	var response struct {
		Delivery *notify.BridgeClaim `json:"delivery"`
	}
	testutil.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
	testutil.NotNil(t, response.Delivery)
	body := `{"generation":"launch","boot":"b","token":"` + response.Delivery.Token + `"}`
	testutil.Equal(t, call("deliveries/authorize", body).Code, 200)
	testutil.Equal(t, call("deliveries/ack", body).Code, 200)
	testutil.Equal(t, s.notifier.DeliveryState(task.ID, "hera:1"), notify.StateSubmitted)
	for _, key := range []string{"bridge.delivery", "bridge.enabled"} {
		t.Run(key, func(t *testing.T) {
			testutil.NoError(t, s.db.SetConfigValue(key, "false"))
			testutil.False(t, s.bridgeDeliveryRoute(task.ID).Live)
			testutil.NoError(t, s.db.SetConfigValue(key, "true"))
		})
	}
}

func TestBridgeDeliveryBlocksRawUncertainPermission(t *testing.T) {
	for _, confidence := range []string{bridge.ConfidencePendingOrRunning, ""} {
		t.Run("confidence="+confidence, func(t *testing.T) {
			s, _, task := bridgeAuthorityServer(t)
			now := time.Now()
			busy, needs := false, true
			s.bridgeHub.Ingest(task.ID, []bridge.Event{{Type: bridge.TypeHeartbeat, Generation: "launch", SessionID: "session", Boot: "b", Seq: 1, Busy: &busy, NeedsInput: &needs, Kind: bridge.KindPermission, Confidence: confidence}}, now)
			// No raw scraper input: display clears input, but delivery must not.
			testutil.False(t, s.BridgeStates()[task.ID].NeedsInput)
			route := s.bridgeDeliveryRoute(task.ID)
			testutil.True(t, route.Live)
			testutil.True(t, route.Idle)
			testutil.True(t, route.NeedsInput)
			s.SetNotifier(notify.New(notify.AdaptRunner(func(string) notify.SessionHandleIface { return nil }), notify.NewFocusTracker(nil)))
			s.notifier.ReliableNotify(task.ID, "notice", "hera:raw-permission", notify.NotifyOpts{})
			testutil.Nil(t, s.notifier.ClaimBridge(task.ID, "launch", "b", now))
		})
	}
}

func TestScanBridgeEvidence(t *testing.T) {
	expires := time.Date(2026, 10, 24, 22, 0, 0, 0, time.UTC)
	c := notify.BridgeClaim{DeliveryID: "hera:1", Expires: expires}
	for _, tc := range []struct {
		name, body string
		want       notify.BridgeEvidence
	}{
		{"found", `{"type":"user","message":{"content":"notice [argus-delivery:hera:1]"}}` + "\n", notify.BridgeFound},
		{"assistant reflection", `{"type":"assistant","timestamp":"2026-10-24T22:01:00Z","message":{"content":"[argus-delivery:hera:1]"}}` + "\n", notify.BridgeAbsent},
		{"covered absent", `{"type":"assistant","timestamp":"2026-10-24T22:01:00Z"}` + "\n", notify.BridgeAbsent},
		{"unflushed", `{"type":"assistant","timestamp":"2026-10-24T21:00:00Z"}` + "\n", notify.BridgeUnknown},
		{"truncated", `{"type":"user"`, notify.BridgeUnknown},
		{"empty", "", notify.BridgeUnknown},
	} {
		t.Run(tc.name, func(t *testing.T) { testutil.Equal(t, scanBridgeEvidence(strings.NewReader(tc.body), c), tc.want) })
	}
}

func TestBridgeSettledClaim(t *testing.T) {
	c := notify.BridgeClaim{Session: "s", Generation: "g", Expires: time.Now()}
	base := bridge.State{SessionID: "s", Generation: "g", LastHeartbeatAt: c.Expires.Add(time.Second)}
	testutil.True(t, bridgeSettledAfterClaim(base, true, c))
	for _, name := range []string{"busy", "dialog", "old-launch", "old-session", "too-early", "dead"} {
		t.Run(name, func(t *testing.T) {
			st := base
			switch name {
			case "busy":
				st.Busy = true
			case "dialog":
				st.NeedsInput = true
			case "old-launch":
				st.Generation = "old"
			case "old-session":
				st.SessionID = "old"
			case "too-early":
				st.LastHeartbeatAt = c.Expires
			}
			testutil.False(t, bridgeSettledAfterClaim(st, name != "dead", c))
		})
	}
}

func TestBridgeBudgetEndpoint(t *testing.T) {
	s, _, task := bridgeAuthorityServer(t)
	s.SetNotifier(notify.New(notify.AdaptRunner(func(string) notify.SessionHandleIface { return nil }), notify.NewFocusTracker(nil)))
	s.bridgeHub.Ingest(task.ID, []bridge.Event{{Type: bridge.TypeTurnComplete, Generation: "launch", SessionID: "session", Boot: "b", Seq: 1}}, time.Now())
	orch, err := s.db.CreateHeraOrchestrator("budget", "")
	testutil.NoError(t, err)
	_, _, err = s.db.CreateHeraRoleWithBinding(db.CreateHeraRoleInput{OrchestratorID: orch.ID, Name: "coord", Kind: db.HeraKindCoordinator}, task.ID, "/tmp/budget")
	testutil.NoError(t, err)
	testutil.NoError(t, s.db.SetMetaBatch(task.ID, db.HeraMetaNamespace, map[string]string{db.HeraMetaKeyContextSize: "310000"}))
	for _, pending := range []bool{false, true} {
		t.Run(map[bool]string{false: "nudge", true: "pending"}[pending], func(t *testing.T) {
			testutil.NoError(t, s.db.SetMetaBatch(task.ID, db.HeraMetaNamespace, map[string]string{db.HeraMetaKeyPendingRecycle: map[bool]string{false: "false", true: "true"}[pending]}))
			w := httptest.NewRecorder()
			s.routes().ServeHTTP(w, authedReq("POST", "/api/tasks/"+task.ID+"/bridge/budget", `{"size":310000,"text":"budget nudge"}`))
			var body struct {
				Enabled bool `json:"enabled"`
			}
			testutil.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
			testutil.Equal(t, body.Enabled, !pending)
			if !pending {
				claim := s.notifier.ClaimBridge(task.ID, "launch", "b", time.Now())
				testutil.NotNil(t, claim)
				testutil.Contains(t, claim.Text, "budget nudge")
				testutil.Contains(t, claim.DeliveryID, "budget:launch:session:310000")
			}
		})
	}
}

func TestBridgeDeliveryEvidenceFromTranscript(t *testing.T) {
	for _, name := range []string{"submitted", "settled absent", "restarted absent", "live queued", "uncertain permission", "unreadable"} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			s, runner, task := bridgeAuthorityServer(t)
			task.Worktree = t.TempDir()
			testutil.NoError(t, s.db.Update(task))
			now := time.Now()
			c := notify.BridgeClaim{TaskID: task.ID, DeliveryID: "hera:1", Session: "session", Generation: "launch", Expires: now.Add(-time.Second)}
			busy := name == "live queued"
			generation := "launch"
			if name == "restarted absent" {
				generation = "new"
				runner.session.generation = generation
				busy = true
			}
			needs := name == "uncertain permission"
			s.bridgeHub.Ingest(task.ID, []bridge.Event{{Type: bridge.TypeHeartbeat, Generation: generation, SessionID: "session", Boot: "b", Seq: 1, Busy: &busy, NeedsInput: &needs, Kind: bridge.KindPermission, Confidence: bridge.ConfidencePendingOrRunning}}, now)
			if needs {
				testutil.False(t, s.BridgeStates()[task.ID].NeedsInput)
				st, live := s.bridgeState(task.ID, now)
				testutil.False(t, bridgeSettledAfterClaim(st, live, c))
			}
			project, err := claudesession.ProjectDir(task.Worktree)
			testutil.NoError(t, err)
			testutil.NoError(t, os.MkdirAll(project, 0700))
			if name != "unreadable" {
				content := "ordinary message"
				if name == "submitted" {
					content = "notice " + notify.BridgeMarker(c.DeliveryID)
				}
				row, err := json.Marshal(map[string]any{"type": "user", "timestamp": now, "message": map[string]string{"content": content}})
				testutil.NoError(t, err)
				testutil.NoError(t, os.WriteFile(filepath.Join(project, "session.jsonl"), append(row, '\n'), 0600))
			}
			want := notify.BridgeUnknown
			if name == "submitted" {
				want = notify.BridgeFound
			}
			if name == "settled absent" || name == "restarted absent" {
				want = notify.BridgeAbsent
			}
			testutil.Equal(t, s.bridgeDeliveryEvidence(c), want)
		})
	}
}

func TestBridgeDeliveryRequestErrors(t *testing.T) {
	s, _, task := bridgeAuthorityServer(t)
	for _, tc := range []struct {
		name, body string
		notifier   bool
		want       int
	}{
		{"unavailable", `{}`, false, 503},
		{"malformed", `broken`, true, 400},
		{"missing identity", `{}`, true, 400},
		{"no route", `{"generation":"g","boot":"b"}`, true, 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.notifier {
				s.SetNotifier(notify.New(notify.AdaptRunner(func(string) notify.SessionHandleIface { return nil }), notify.NewFocusTracker(nil)))
			}
			w := httptest.NewRecorder()
			s.routes().ServeHTTP(w, authedReq("POST", "/api/tasks/"+task.ID+"/bridge/deliveries", tc.body))
			testutil.Equal(t, w.Code, tc.want)
		})
	}
}

func TestBridgeSettingsOffUsePTY(t *testing.T) {
	for _, key := range []string{"bridge.enabled", "bridge.delivery"} {
		t.Run(key, func(t *testing.T) {
			s, _, task := bridgeAuthorityServer(t)
			s.bridgeHub.Ingest(task.ID, []bridge.Event{{Type: bridge.TypeTurnComplete, Generation: "launch", SessionID: "session", Boot: "b", Seq: 1}}, time.Now())
			sess := &notifyIdleSession{}
			s.SetNotifier(notify.New(notify.AdaptRunner(func(string) notify.SessionHandleIface { return sess }), notifyNoFocus{}))
			testutil.NoError(t, s.db.SetConfigValue(key, "false"))
			w := httptest.NewRecorder()
			s.routes().ServeHTTP(w, authedReq("POST", "/api/tasks/"+task.ID+"/notify", `{"text":"notice","submit":true,"delivery_id":"disabled"}`))
			testutil.Equal(t, w.Code, 202)
			testutil.Contains(t, w.Body.String(), `"submitted"`)
			testutil.Equal(t, len(sess.writes), 3)
			testutil.Equal(t, string(sess.writes[0]), "\x15")
			testutil.Equal(t, string(sess.writes[1]), "notice")
			testutil.Equal(t, string(sess.writes[2]), "\r")
		})
	}
}

func TestBridgeSettingsREST(t *testing.T) {
	s, _, _ := bridgeAuthorityServer(t)
	for _, key := range []string{"enabled", "delivery"} {
		t.Run(key, func(t *testing.T) {
			before := s.db.Config().Bridge
			w := httptest.NewRecorder()
			s.routes().ServeHTTP(w, authedReq("PUT", "/api/settings", `{"bridge":{"`+key+`":false}}`))
			testutil.Equal(t, w.Code, 200)
			var body map[string]map[string]any
			testutil.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
			testutil.Equal(t, body["bridge"][key], any(false))
			cfg := s.db.Config()
			if key == "enabled" {
				testutil.False(t, cfg.Bridge.Enabled)
				testutil.Equal(t, cfg.Bridge.Delivery, before.Delivery)
			} else {
				testutil.False(t, cfg.Bridge.Delivery)
				testutil.Equal(t, cfg.Bridge.Enabled, before.Enabled)
			}
		})
	}
}
