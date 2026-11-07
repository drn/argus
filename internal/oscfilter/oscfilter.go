package oscfilter

import (
	"bytes"
	"strings"
)

// maxOSCDropBytes bounds how many bytes a single OSC sequence may consume before
// the filter gives up and resumes passing bytes through. Real OSC sequences
// (window titles, hyperlinks) are short; the cap only guards against a
// pathological stream whose OSC never sends a terminator we recognize, so the
// filter can never drop unboundedly.
const maxOSCDropBytes = 64 * 1024

// maxHyperlinkBytes bounds the OSC-8 payload (`params;URI`) the filter will
// buffer and pass through. A longer hyperlink is stripped like any other OSC;
// its visible link text is unaffected.
const maxHyperlinkBytes = 4096

type filterState uint8

const (
	oscNormal      filterState = iota // outside any sequence
	oscEscPending                     // saw ESC at top level, awaiting next byte
	oscInString                       // inside an OSC payload
	oscInStringEsc                    // inside an OSC payload, saw ESC (maybe 7-bit ST)
)

// Filter is a streaming filter that removes OSC sequences (`ESC ] … ST`) from
// a PTY byte stream before it reaches the x/vt emulator — except OSC-8
// hyperlinks, which it re-emits in a sanitized form.
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
// Dropping most OSC is safe for the embedded agent pane: window titles are
// never displayed. OSC-8 hyperlinks are the exception: dropping them strips the
// URL from every link the agent prints, so the host terminal can never open it.
// A hyperlink (`ESC ] 8 ; params ; URI`) is buffered and re-emitted with every
// byte outside printable ASCII percent-encoded and a 7-bit ST terminator, so no
// 0x9C can reach the parser inside it. The filter is stateful so OSC sequences
// split across incremental feeds are handled.
//
// Crucially, 0x9C is deliberately NOT treated as a terminator here — that is the
// exact upstream behavior we are working around. OSC strings are terminated only
// by BEL or 7-bit ST (`ESC \`) and cancelled by CAN/SUB or a fresh ESC sequence,
// matching how a UTF-8-mode terminal behaves.
type Filter struct {
	state   filterState
	dropped int
	buf     []byte

	// Hyperlink tracking for the OSC currently being read. maybeLink is true
	// while the payload so far is a prefix of "8;"; linking is true once the
	// payload began with exactly "8;", and link holds the bytes after it.
	maybeLink bool
	linking   bool
	link      []byte
}

// Reset returns the filter to its initial state. Call whenever the downstream
// emulator is recreated and re-fed from a clean escape boundary.
func (f *Filter) Reset() {
	f.state = oscNormal
	f.dropped = 0
	f.endOSC()
}

// endOSC clears the per-OSC hyperlink state.
func (f *Filter) endOSC() {
	f.maybeLink = false
	f.linking = false
	f.link = f.link[:0]
}

// terminateOSC handles a real OSC terminator (BEL or 7-bit ST): a buffered
// hyperlink is emitted, anything else stays dropped.
func (f *Filter) terminateOSC() {
	// A well-formed hyperlink payload is `params;URI`; without the separator
	// it is malformed and stripped.
	if f.linking && bytes.IndexByte(f.link, ';') >= 0 {
		f.emitHyperlink()
	}
	f.endOSC()
	f.state = oscNormal
}

const hexDigits = "0123456789ABCDEF"

// emitHyperlink appends `ESC ] 8 ; <payload> ESC \` to the output, with every
// payload byte outside printable ASCII percent-encoded.
func (f *Filter) emitHyperlink() {
	f.buf = append(f.buf, 0x1b, ']', '8', ';')
	for _, b := range f.link {
		if b < 0x20 || b > 0x7e {
			f.buf = append(f.buf, '%', hexDigits[b>>4], hexDigits[b&0x0f])
		} else {
			f.buf = append(f.buf, b)
		}
	}
	f.buf = append(f.buf, 0x1b, '\\')
}

// Filter returns `in` with OSC sequences removed (OSC-8 hyperlinks re-emitted
// sanitized), carrying parser state across calls so a sequence split between
// feeds is still handled. The returned slice is owned by the filter and is
// overwritten by the next call; callers must hand it to the emulator before
// calling filter again.
func (f *Filter) Filter(in []byte) []byte {
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
				f.endOSC()
				f.maybeLink = true
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
				f.terminateOSC()
			case b == 0x1b: // possible 7-bit ST (`ESC \`) or a fresh sequence.
				f.state = oscInStringEsc
			case b == 0x18 || b == 0x1a: // CAN/SUB cancel the OSC.
				f.endOSC()
				f.state = oscNormal
			case f.linking:
				if len(f.link) >= maxHyperlinkBytes {
					// Too long to pass through: strip it like any other OSC.
					f.endOSC()
				} else {
					f.link = append(f.link, b)
				}
			case f.maybeLink && f.dropped == 1:
				f.maybeLink = b == '8'
			case f.maybeLink && f.dropped == 2:
				f.maybeLink = false
				f.linking = b == ';'
			case f.dropped > maxOSCDropBytes:
				// Runaway guard: an OSC with no terminator we recognize. Stop
				// dropping and re-emit this byte so the filter can never hang.
				f.endOSC()
				f.state = oscNormal
				f.buf = append(f.buf, b)
			default:
				// Drop the payload byte. 0x9C is intentionally NOT treated as a
				// terminator — that is the upstream bug we are working around.
				f.maybeLink = false
			}
		case oscInStringEsc:
			if b == '\\' {
				// 7-bit ST: end of OSC; drop the trailing backslash too.
				f.terminateOSC()
			} else {
				// The ESC begins a new sequence, cancelling the OSC. Defer the
				// ESC and reprocess this byte (handles back-to-back `ESC ]`).
				f.endOSC()
				f.state = oscEscPending
				i--
			}
		}
	}
	return f.buf
}

// Flush emits any byte the filter is holding pending more input (a lone
// top-level ESC). Call after the final chunk of a one-shot feed; do NOT call
// between chunks of a streaming feed, or a deferred ESC would be emitted before
// the byte that decides whether it opens an OSC.
func (f *Filter) Flush() []byte {
	if f.state == oscEscPending {
		f.state = oscNormal
		return []byte{0x1b}
	}
	return nil
}

// Strip removes OSC sequences from a complete buffer in a single pass (OSC-8
// hyperlinks are kept, sanitized). Use for one-shot whole-buffer feeds
// (replay/rebuild/preview); for incremental live feeds use a persistent Filter
// so split sequences survive across chunks. See the Filter doc for why this is
// necessary.
func Strip(in []byte) []byte {
	var f Filter
	out := f.Filter(in)
	out = append(out, f.Flush()...)
	return out
}

// HyperlinkID returns the value of the `id` key in an OSC-8 params string
// (colon-separated `key=value` pairs, e.g. "id=ub9bei"), or "" when absent.
// Host terminals use the id to treat every cell of one link — including a
// link wrapped across rows — as a single link.
func HyperlinkID(params string) string {
	for _, kv := range strings.Split(params, ":") {
		if v, ok := strings.CutPrefix(kv, "id="); ok {
			return v
		}
	}
	return ""
}
