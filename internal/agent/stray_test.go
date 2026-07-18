package agent

import (
	"testing"

	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

func TestStrayJobIDsForSession_MatchesAndFiltersBySessionID(t *testing.T) {
	data := []byte(`[
		{"id": "job-1", "session_id": "sess-a"},
		{"id": "job-2", "session_id": "sess-b"},
		{"id": "job-3", "session_id": "sess-a"}
	]`)

	ids, err := strayJobIDsForSession(data, "sess-a")
	testutil.NoError(t, err)
	testutil.DeepEqual(t, ids, []string{"job-1", "job-3"})
}

func TestStrayJobIDsForSession_NoMatches(t *testing.T) {
	data := []byte(`[{"id": "job-1", "session_id": "sess-b"}]`)

	ids, err := strayJobIDsForSession(data, "sess-a")
	testutil.NoError(t, err)
	testutil.Equal(t, len(ids), 0)
}

func TestStrayJobIDsForSession_ToleratesKeyVariants(t *testing.T) {
	data := []byte(`[
		{"id": "job-1", "sessionId": "sess-a"},
		{"job_id": "job-2", "session": "sess-a"}
	]`)

	ids, err := strayJobIDsForSession(data, "sess-a")
	testutil.NoError(t, err)
	testutil.DeepEqual(t, ids, []string{"job-1", "job-2"})
}

func TestStrayJobIDsForSession_MalformedJSONErrors(t *testing.T) {
	_, err := strayJobIDsForSession([]byte("not json"), "sess-a")
	testutil.Contains(t, err.Error(), "strayJobIDsForSession")
}

func TestClaudeBinaryFromCommand(t *testing.T) {
	testutil.Equal(t, claudeBinaryFromCommand("claude --model sonnet"), "claude")
	testutil.Equal(t, claudeBinaryFromCommand("/usr/local/bin/claude --resume x"), "/usr/local/bin/claude")
	testutil.Equal(t, claudeBinaryFromCommand(""), "claude")
}

func TestStopStrayJobs_NoopWhenSessionIDEmpty(t *testing.T) {
	task := &model.Task{ID: "t1", Backend: "claude"}
	testutil.NoError(t, StopStrayJobs(task, config.DefaultConfig(), ""))
}

func TestStopStrayJobs_NoopForNonClaudeBackend(t *testing.T) {
	task := &model.Task{ID: "t1", Backend: "codex"}
	testutil.NoError(t, StopStrayJobs(task, config.DefaultConfig(), "sess-a"))
}
