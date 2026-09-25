package agent

import (
	"bytes"
	"os/exec"
	"testing"
	"time"

	"github.com/drn/argus/internal/app/agentview"
)

func TestTerminalColorQueriesAcrossReadChunks(t *testing.T) {
	var scanner terminalColorQueries
	var replies [][]byte
	collect := func(p []byte) { replies = append(replies, bytes.Clone(p)) }
	scanner.scan([]byte("ordinary output\x1b]11;?\x1b"), collect)
	scanner.scan([]byte("\\more output\x1b]10;?\a"), collect)
	if len(replies) != 2 || !bytes.Equal(replies[0], backgroundReply) || !bytes.Equal(replies[1], foregroundReply) {
		t.Fatalf("color query replies = %q", replies)
	}
}

func TestIsColorQueryReply(t *testing.T) {
	for _, reply := range [][]byte{backgroundReply, foregroundReply} {
		if !isColorQueryReply(reply) {
			t.Fatalf("did not recognize %q", reply)
		}
	}
	for _, input := range [][]byte{nil, []byte("hello"), []byte("\x1b]11;?\x1b\\"), []byte("\x1b]11;rgb:bad"), append(bytes.Clone(backgroundReply), 'x')} {
		if isColorQueryReply(input) {
			t.Fatalf("misidentified ordinary input %q", input)
		}
	}
}

func TestFilterAnsweredColorRepliesInRemoteBatch(t *testing.T) {
	da := []byte("\x1b[?1;2c")
	batch := append(bytes.Clone(foregroundReply), backgroundReply...)
	batch = append(batch, da...)
	batch = append(batch, 'x')

	if got := filterAnsweredColorReplies(batch, [2]bool{true, false}); !bytes.Equal(got, append(append(bytes.Clone(backgroundReply), da...), 'x')) {
		t.Fatalf("one answered color: got %q", got)
	}
	if got := filterAnsweredColorReplies(batch, [2]bool{true, true}); !bytes.Equal(got, append(bytes.Clone(da), 'x')) {
		t.Fatalf("both answered colors: got %q", got)
	}
	if got := filterAnsweredColorReplies(append(bytes.Clone(da), backgroundReply...), [2]bool{false, true}); !bytes.Equal(got, da) {
		t.Fatalf("device attributes before color: got %q", got)
	}
	if got := filterAnsweredColorReplies(batch, [2]bool{}); !bytes.Equal(got, batch) {
		t.Fatalf("unanswered colors changed: got %q", got)
	}
}

func TestSessionAnswersColorQueryBeforeRendererAttaches(t *testing.T) {
	sess, err := StartSession("color-query-before-attach", exec.Command("sh", "-c", "stty raw -echo; printf ready; cat"), 24, 80)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sess.Stop() }()
	waitForOutput := func(want []byte) {
		t.Helper()
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			if bytes.Contains(sess.RecentOutput(), want) {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("session output did not contain %q", want)
	}
	waitForOutput([]byte("ready"))
	if _, err := sess.WriteInput(backgroundQueryST, agentview.OriginUser); err != nil {
		t.Fatal(err)
	}
	waitForOutput(backgroundReply)

	// A renderer that replays the original query must not send a second
	// color reply into the now-interactive child process.
	before := sess.LastInput()
	if _, err := sess.WriteInput(backgroundReply, agentview.OriginSystem); err != nil {
		t.Fatal(err)
	}
	if got := sess.LastInput(); got != before {
		t.Fatal("duplicate renderer reply reached the child process")
	}
	if _, err := sess.WriteInput(backgroundReply, agentview.OriginUser); err != nil {
		t.Fatal(err)
	}
	if got := sess.LastInput(); !got.After(before) {
		t.Fatal("literal user input was suppressed as a color reply")
	}
}
