package terminal

// maxOSCDropBytes bounds how many bytes a single OSC sequence may consume before
// the filter gives up and resumes passing bytes through. Real OSC sequences
// (window titles, hyperlinks) are short; the cap only guards against a
// pathological stream whose OSC never sends a terminator we recognize, so the
// filter can never drop unboundedly.
const maxOSCDropBytes = 64 * 1024

type oscFilterState uint8

const (
	oscNormal      oscFilterState = iota // outside any sequence
	oscEscPending                        // saw ESC at top level, awaiting next byte
	oscInString                          // inside an OSC payload, dropping bytes
	oscInStringEsc                       // inside an OSC payload, saw ESC (maybe 7-bit ST)
)

// oscFilter is a streaming filter that removes OSC sequences (`ESC ] … ST`) from
// a PTY byte stream before it reaches the x/vt emulator.
//
// It works around an upstream parser bug in charmbracelet/x/ansi
// (parser_decode.go, StringState): a 0x9C byte inside an OSC string is treated
// as a C1 String Terminator even when that byte is a UTF-8 continuation byte.
// Many glyphs encode a 0x9C — e.g. Claude's spinner title "✳ …" (✳ = E2 9C B3).
// The parser truncates the OSC at the 0x9C and renders the rest of the title as
// printable ground text. A subsequent in-place menu repaint overwrites most of
// the leaked text, but Claude's option rows position their label with `ESC[6G`
// (cursor to column 6), skipping column 5 — so one leaked character survives in
// that gap. That is the stray character users see between "1." and the option
// label in AskUserQuestion menus. tmux/xterm don't reproduce it: in UTF-8 mode
// they never treat a 0x9C continuation byte as ST.
//
// Dropping OSC entirely is safe for the embedded agent pane: window titles are
// never displayed, and OSC-8 hyperlink *text* is ordinary ground text that
// survives (only the invisible URL association is dropped). The filter is
// stateful so OSC sequences split across incremental feeds are handled.
//
// Crucially, 0x9C is deliberately NOT treated as a terminator here — that is the
// exact upstream behavior we are working around. OSC strings are terminated only
// by BEL or 7-bit ST (`ESC \`) and cancelled by CAN/SUB or a fresh ESC sequence,
// matching how a UTF-8-mode terminal behaves.
type oscFilter struct {
	state   oscFilterState
	dropped int
	buf     []byte
}

// reset returns the filter to its initial state. Call whenever the downstream
// emulator is recreated and re-fed from a clean escape boundary.
func (f *oscFilter) reset() {
	f.state = oscNormal
	f.dropped = 0
}

// filter returns `in` with OSC sequences removed, carrying parser state across
// calls so a sequence split between feeds is still stripped. The returned slice
// is owned by the filter and is overwritten by the next call; callers must hand
// it to the emulator before calling filter again.
func (f *oscFilter) filter(in []byte) []byte {
	if cap(f.buf) < len(in) {
		f.buf = make([]byte, 0, len(in))
	} else {
		f.buf = f.buf[:0]
	}
	for i := 0; i < len(in); i++ {
		b := in[i]
		switch f.state {
		case oscNormal:
			if b == 0x1b {
				f.state = oscEscPending
			} else {
				f.buf = append(f.buf, b)
			}
		case oscEscPending:
			if b == ']' {
				f.state = oscInString
				f.dropped = 0
			} else {
				// Not an OSC introducer: emit the deferred ESC and reprocess
				// this byte from oscNormal (it may begin another sequence,
				// e.g. CSI `ESC [`).
				f.buf = append(f.buf, 0x1b)
				f.state = oscNormal
				i--
			}
		case oscInString:
			f.dropped++
			switch {
			case b == 0x07: // BEL terminates the OSC.
				f.state = oscNormal
			case b == 0x1b: // possible 7-bit ST (`ESC \`) or a fresh sequence.
				f.state = oscInStringEsc
			case b == 0x18 || b == 0x1a: // CAN/SUB cancel the OSC.
				f.state = oscNormal
			case f.dropped > maxOSCDropBytes:
				// Runaway guard: an OSC with no terminator we recognize. Stop
				// dropping and re-emit this byte so the filter can never hang.
				f.state = oscNormal
				f.buf = append(f.buf, b)
			default:
				// Drop the payload byte. 0x9C is intentionally NOT treated as a
				// terminator — that is the upstream bug we are working around.
			}
		case oscInStringEsc:
			if b == '\\' {
				// 7-bit ST: end of OSC; drop the trailing backslash too.
				f.state = oscNormal
			} else {
				// The ESC begins a new sequence, cancelling the OSC. Defer the
				// ESC and reprocess this byte (handles back-to-back `ESC ]`).
				f.state = oscEscPending
				i--
			}
		}
	}
	return f.buf
}

// flush emits any byte the filter is holding pending more input (a lone
// top-level ESC). Call after the final chunk of a one-shot feed; do NOT call
// between chunks of a streaming feed, or a deferred ESC would be emitted before
// the byte that decides whether it opens an OSC.
func (f *oscFilter) flush() []byte {
	if f.state == oscEscPending {
		f.state = oscNormal
		return []byte{0x1b}
	}
	return nil
}

// FilterOSC strips OSC sequences from a complete buffer in a single pass. Use
// for one-shot whole-buffer feeds (replay/rebuild/preview); for incremental
// live feeds use a persistent oscFilter so split sequences survive across
// chunks. See the oscFilter doc for why this is necessary.
func FilterOSC(in []byte) []byte {
	var f oscFilter
	out := f.filter(in)
	out = append(out, f.flush()...)
	return out
}
