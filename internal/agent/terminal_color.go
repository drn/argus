package agent

import "bytes"

// Terminal color queries can arrive before Argus has attached any renderer to
// the PTY. The session answers them itself so clients that inspect the color
// scheme during startup (notably Codex) can style their first frame.
var (
	backgroundQueryST  = []byte("\x1b]11;?\x1b\\")
	backgroundQueryBEL = []byte("\x1b]11;?\a")
	foregroundQueryST  = []byte("\x1b]10;?\x1b\\")
	foregroundQueryBEL = []byte("\x1b]10;?\a")
	backgroundReply    = []byte("\x1b]11;rgb:0000/0000/0000\x1b\\")
	foregroundReply    = []byte("\x1b]10;rgb:e5e5/e5e5/e5e5\x1b\\")
)

type terminalColorQueries struct{ tail []byte }

func (q *terminalColorQueries) scan(data []byte, reply func([]byte)) {
	for _, b := range data {
		q.tail = append(q.tail, b)
		if len(q.tail) > len(backgroundQueryST) {
			q.tail = q.tail[1:]
		}
		switch {
		case bytes.HasSuffix(q.tail, backgroundQueryST), bytes.HasSuffix(q.tail, backgroundQueryBEL):
			reply(backgroundReply)
			q.tail = q.tail[:0]
		case bytes.HasSuffix(q.tail, foregroundQueryST), bytes.HasSuffix(q.tail, foregroundQueryBEL):
			reply(foregroundReply)
			q.tail = q.tail[:0]
		}
	}
}

func isColorQueryReply(p []byte) bool {
	return colorReplyIndex(p) >= 0
}

// 0 is OSC 10 (foreground), 1 is OSC 11 (background).
func colorReplyIndex(p []byte) int {
	if !bytes.HasSuffix(p, []byte("\x1b\\")) && !bytes.HasSuffix(p, []byte("\a")) {
		return -1
	}
	if bytes.HasPrefix(p, []byte("\x1b]10;rgb:")) {
		return 0
	}
	if bytes.HasPrefix(p, []byte("\x1b]11;rgb:")) {
		return 1
	}
	return -1
}

func (s *Session) forwardColorReplies() {
	for reply := range s.colorReplies {
		// A PTY write can block if the child stops reading. Keep it off the
		// sole output reader so later PTY output can still drain.
		_, _ = s.ptmx.Write(reply)
	}
}
