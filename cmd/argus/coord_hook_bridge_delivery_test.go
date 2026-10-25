package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/drn/argus/internal/testutil"
)

func TestCoordHookBridgeDeliveryNudge(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy", true: "bridge"}[enabled], func(t *testing.T) {
			f := &fakeCoordHookEnv{getenv: map[string]string{"ARGUS_TASK_ID": "t"}, roleKind: "coordinator", contextSize: 110, budget: 100}
			env := f.env()
			env.BridgeDelivery = func(string, int, string) (bool, error) { return enabled, nil }
			var out, errOut bytes.Buffer
			runCoordHook(stopHookStdin("unused"), &out, &errOut, env)
			testutil.Equal(t, out.Len() == 0, enabled)
			testutil.True(t, f.stampCalled)
			testutil.True(t, f.stampLastNudgedCalled)
			f.contextSize = 150
			runCoordHook(stopHookStdin("unused"), &out, &errOut, env)
			testutil.True(t, f.forceRecycleCalled)
		})
	}
}

func TestCoordHookBridgeCadence(t *testing.T) {
	for _, tc := range []struct {
		name               string
		size, last         int
		had, pending, want bool
	}{
		{"first", 300, 0, false, false, true},
		{"below", 299, 0, false, false, false},
		{"within increment", 349, 300, true, false, false},
		{"full increment", 350, 300, true, false, true},
		{"fresh episode", 310, 400, true, false, true},
		{"pending", 350, 300, true, true, false},
		{"forced", 450, 300, true, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeCoordHookEnv{getenv: map[string]string{"ARGUS_TASK_ID": "t"}, roleKind: "coordinator", contextSize: tc.size, budget: 300, lastNudged: tc.last, hadLastNudged: tc.had, nudgeIncrement: 50, pendingRecycle: tc.pending}
			env := f.env()
			called := false
			env.BridgeDelivery = func(id string, size int, text string) (bool, error) {
				called = true
				testutil.Equal(t, id, "t")
				testutil.Equal(t, size, tc.size)
				testutil.Contains(t, text, "Reach a safe seam")
				return true, nil
			}
			var out, errOut bytes.Buffer
			runCoordHook(stopHookStdin("unused"), &out, &errOut, env)
			testutil.Equal(t, called, tc.want)
			testutil.Equal(t, f.stampLastNudgedCalled, tc.want)
			testutil.Equal(t, out.String(), "")
		})
	}
}

func TestCoordHookBudgetAmbiguityDoesNotBlock(t *testing.T) {
	f := &fakeCoordHookEnv{getenv: map[string]string{"ARGUS_TASK_ID": "t"}, roleKind: "coordinator", contextSize: 110, budget: 100}
	env := f.env()
	env.BridgeDelivery = func(string, int, string) (bool, error) { return false, errors.New("response lost") }
	var out, errOut bytes.Buffer
	runCoordHook(stopHookStdin("unused"), &out, &errOut, env)
	testutil.Equal(t, out.String(), "")
	testutil.True(t, f.stampLastNudgedCalled)
	testutil.Contains(t, errOut.String(), "ambiguous")
}

func TestPostBridgeBudget(t *testing.T) {
	for _, tc := range []struct {
		name, body        string
		status            int
		queued, ambiguous bool
	}{
		{"queued", `{"enabled":true}`, 200, true, false},
		{"disabled", `{"enabled":false}`, 200, false, false},
		{"old daemon", `{}`, 404, false, false},
		{"server error", `{}`, 500, false, true},
		{"bad response", `broken`, 200, false, true},
		{"missing result", `{}`, 200, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				testutil.Equal(t, r.Method, "POST")
				testutil.Equal(t, r.Header.Get("Authorization"), "Bearer fake")
				var body struct {
					Size int    `json:"size"`
					Text string `json:"text"`
				}
				testutil.NoError(t, json.NewDecoder(r.Body).Decode(&body))
				testutil.Equal(t, body.Size, 110)
				testutil.Equal(t, body.Text, "nudge")
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			queued, err := postBridgeBudget(srv.URL, "fake", 110, "nudge")
			testutil.Equal(t, queued, tc.queued)
			testutil.Equal(t, err != nil, tc.ambiguous)
		})
	}
	t.Run("lost response", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			conn, _, err := w.(http.Hijacker).Hijack()
			testutil.NoError(t, err)
			_ = conn.Close()
		}))
		defer srv.Close()
		queued, err := postBridgeBudget(srv.URL, "fake", 110, "nudge")
		testutil.False(t, queued)
		testutil.NotNil(t, err)
	})
}
