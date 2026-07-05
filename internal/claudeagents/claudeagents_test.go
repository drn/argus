package claudeagents

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"testing"

	"github.com/drn/argus/internal/testutil"
)

func TestSession_Alive(t *testing.T) {
	testutil.Equal(t, Session{PID: 1234}.Alive(), true)
	testutil.Equal(t, Session{}.Alive(), false)
}

func TestSession_Backgrounded(t *testing.T) {
	testutil.Equal(t, Session{Kind: "background"}.Backgrounded(), true)
	testutil.Equal(t, Session{Kind: "interactive"}.Backgrounded(), false)
	testutil.Equal(t, Session{}.Backgrounded(), false)
}

func TestList_NoClaude(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	_, err := List(context.Background(), "/some/worktree")
	testutil.ErrorIs(t, err, ErrUnavailable)
}

func TestStop_NoClaude(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	err := Stop(context.Background(), "abc123")
	testutil.ErrorIs(t, err, ErrUnavailable)
}

func TestStop_EmptyID(t *testing.T) {
	err := Stop(context.Background(), "   ")
	if err == nil {
		t.Fatal("want error for empty id, got nil")
	}
}

// setupFakeClaude wires a fake `claude` binary onto PATH and swaps
// cmdFactory to run it, mirroring internal/llm's setupFakeClaude. The fake
// script echoes stdout, writes stderr, and exits with the given code.
// captureArgs, if non-nil, is populated with the args cmdFactory received.
func setupFakeClaude(t *testing.T, stdout, stderr string, exitCode int, captureArgs *[]string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell script not portable on Windows")
	}
	tmp := t.TempDir()
	fake := tmp + "/claude"
	script := "#!/bin/sh\n"
	if stdout != "" {
		script += "printf '%s' '" + stdout + "'\n"
	}
	if stderr != "" {
		script += "printf '%s' '" + stderr + "' >&2\n"
	}
	script += "exit " + itoa(exitCode) + "\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil { //nolint:gosec // test fixture, needs +x
		t.Fatalf("write fake claude: %v", err)
	}
	t.Setenv("PATH", tmp)

	prev := cmdFactory
	t.Cleanup(func() { cmdFactory = prev })
	cmdFactory = func(ctx context.Context, args ...string) *exec.Cmd {
		if captureArgs != nil {
			*captureArgs = args
		}
		return exec.CommandContext(ctx, fake)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}

func TestList_ParsesInteractiveOnly(t *testing.T) {
	setupFakeClaude(t, `[{"pid":6671,"cwd":"/x","kind":"interactive","startedAt":1,"sessionId":"abc","name":"n","status":"busy"}]`, "", 0, nil)

	sessions, err := List(context.Background(), "/x")
	testutil.NoError(t, err)
	testutil.Equal(t, len(sessions), 1)
	testutil.Equal(t, sessions[0].Kind, "interactive")
	testutil.Equal(t, sessions[0].Backgrounded(), false)
	testutil.Equal(t, sessions[0].Alive(), true)
}

func TestList_ParsesMixedInteractiveAndBackground(t *testing.T) {
	setupFakeClaude(t, `[
		{"pid":1,"cwd":"/x","kind":"interactive","startedAt":1,"sessionId":"a"},
		{"pid":2,"cwd":"/x","kind":"background","startedAt":2,"id":"short1","state":"working"},
		{"cwd":"/x","kind":"background","startedAt":3,"id":"short2","state":"done"}
	]`, "", 0, nil)

	sessions, err := List(context.Background(), "/x")
	testutil.NoError(t, err)
	testutil.Equal(t, len(sessions), 3)

	testutil.Equal(t, sessions[0].Backgrounded(), false)

	testutil.Equal(t, sessions[1].Backgrounded(), true)
	testutil.Equal(t, sessions[1].Alive(), true)
	testutil.Equal(t, sessions[1].ID, "short1")

	testutil.Equal(t, sessions[2].Backgrounded(), true)
	testutil.Equal(t, sessions[2].Alive(), false)
}

func TestList_MalformedJSON(t *testing.T) {
	setupFakeClaude(t, `not json`, "", 0, nil)

	_, err := List(context.Background(), "/x")
	if err == nil {
		t.Fatal("want parse error, got nil")
	}
}

func TestList_NonZeroExit(t *testing.T) {
	setupFakeClaude(t, "", "boom", 1, nil)

	_, err := List(context.Background(), "/x")
	if err == nil {
		t.Fatal("want error, got nil")
	}
}

func TestList_PassesCwdFlag(t *testing.T) {
	var args []string
	setupFakeClaude(t, `[]`, "", 0, &args)

	_, err := List(context.Background(), "/my/worktree")
	testutil.NoError(t, err)

	testutil.DeepEqual(t, args, []string{"agents", "--json", "--cwd", "/my/worktree"})
}

func TestList_OmitsCwdFlagWhenEmpty(t *testing.T) {
	var args []string
	setupFakeClaude(t, `[]`, "", 0, &args)

	_, err := List(context.Background(), "")
	testutil.NoError(t, err)

	testutil.DeepEqual(t, args, []string{"agents", "--json"})
}

func TestStop_Success(t *testing.T) {
	var args []string
	setupFakeClaude(t, "stopped", "", 0, &args)

	err := Stop(context.Background(), "short1")
	testutil.NoError(t, err)
	testutil.DeepEqual(t, args, []string{"stop", "short1"})
}

func TestStop_NoJobMatching(t *testing.T) {
	setupFakeClaude(t, "No job matching 'abc-uuid'", "", 1, nil)

	err := Stop(context.Background(), "abc-uuid")
	if err == nil {
		t.Fatal("want error, got nil")
	}
	testutil.Contains(t, err.Error(), "No job matching")
}
