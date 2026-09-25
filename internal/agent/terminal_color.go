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

type terminalColorQueries struct {
	tail [9]byte // longest supported query (OSC 10/11 with ST terminator)
	n    int
}

func (q *terminalColorQueries) scan(data []byte, reply func([]byte)) {
	for _, b := range data {
		if q.n == len(q.tail) {
			copy(q.tail[:], q.tail[1:])
			q.n--
		}
		q.tail[q.n] = b
		q.n++
		tail := q.tail[:q.n]
		switch {
		case bytes.HasSuffix(tail, backgroundQueryST), bytes.HasSuffix(tail, backgroundQueryBEL):
			reply(backgroundReply)
			q.n = 0
		case bytes.HasSuffix(tail, foregroundQueryST), bytes.HasSuffix(tail, foregroundQueryBEL):
			reply(foregroundReply)
			q.n = 0
		}
	}
}

func isColorQueryReply(p []byte) bool {
	kind, end := colorReplyAt(p, 0)
	return kind >= 0 && end == len(p)
}

// 0 is OSC 10 (foreground), 1 is OSC 11 (background).
func colorReplyIndex(p []byte) int {
	kind, end := colorReplyAt(p, 0)
	if end != len(p) {
		return -1
	}
	return kind
}

// colorReplyAt recognizes one complete OSC color response at offset. The
// validation matters: remote clients batch writes, and a color response may
// be adjacent to keystrokes or another terminal response in the same RPC.
func colorReplyAt(p []byte, offset int) (kind, end int) {
	kind = -1
	if offset < 0 || offset >= len(p) {
		return
	}
	var prefix []byte
	switch {
	case bytes.HasPrefix(p[offset:], []byte("\x1b]10;rgb:")):
		kind, prefix = 0, []byte("\x1b]10;rgb:")
	case bytes.HasPrefix(p[offset:], []byte("\x1b]11;rgb:")):
		kind, prefix = 1, []byte("\x1b]11;rgb:")
	default:
		return -1, 0
	}
	i := offset + len(prefix)
	for component := 0; component < 3; component++ {
		start := i
		for i < len(p) && i-start < 4 && isHexDigit(p[i]) {
			i++
		}
		if i == start {
			return -1, 0
		}
		if component < 2 {
			if i >= len(p) || p[i] != '/' {
				return -1, 0
			}
			i++
		}
	}
	if i < len(p) && p[i] == '\a' {
		return kind, i + 1
	}
	if i+1 < len(p) && p[i] == '\x1b' && p[i+1] == '\\' {
		return kind, i + 2
	}
	return -1, 0
}

func isHexDigit(b byte) bool {
	return b >= '0' && b <= '9' || b >= 'a' && b <= 'f' || b >= 'A' && b <= 'F'
}

// filterAnsweredColorReplies removes only the OSC replies already supplied
// by this session, preserving every other byte in a coalesced remote write.
func filterAnsweredColorReplies(p []byte, answered [2]bool) []byte {
	var out []byte
	segment := 0
	for i := 0; i < len(p); {
		kind, end := colorReplyAt(p, i)
		if kind >= 0 {
			if answered[kind] {
				if out == nil {
					out = make([]byte, 0, len(p))
				}
				out = append(out, p[segment:i]...)
				segment = end
			}
			i = end
		} else {
			i++
		}
	}
	if out == nil {
		return p
	}
	return append(out, p[segment:]...)
}

func (s *Session) forwardColorReplies() {
	for reply := range s.colorReplies {
		// A PTY write can block if the child stops reading. Keep it off the
		// sole output reader so later PTY output can still drain.
		_, _ = s.ptmx.Write(reply)
	}
}
