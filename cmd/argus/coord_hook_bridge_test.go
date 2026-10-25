package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/drn/argus/internal/testutil"
)

func TestCoordHook_BridgeMeasurementSkipsTranscript(t *testing.T) {
	f := &fakeCoordHookEnv{getenv: map[string]string{"ARGUS_TASK_ID": "task"}, roleKind: "worker"}
	env := f.env()
	env.BridgeMeasurement = func(string) (int, bool, bool) { return 12000, true, true }
	var out, errs bytes.Buffer
	runCoordHook(strings.NewReader(`{"transcript_path":"missing"}`), &out, &errs, env)
	testutil.False(t, f.readCalled)
	testutil.False(t, f.stampCalled)
	testutil.False(t, f.readTokenTotalsCalled)
	testutil.False(t, f.stampTokenAccrualCalled)
	testutil.Equal(t, errs.String(), "")
}
