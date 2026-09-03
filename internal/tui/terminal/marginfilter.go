package terminal

import (
	"strconv"
	"strings"
)

// maxScrollRegionCSIScan bounds how many bytes ClampScrollRegion buffers
// while looking for a CSI sequence's final byte, mirroring maxOSCDropBytes's
// runaway guard in oscfilter.go. Real DECSTBM/DECSLRM sequences are a
// handful of bytes; the cap only protects against a stream that never sends
// a byte in the CSI final-byte range, so scanning can never run unbounded.
const maxScrollRegionCSIScan = 64

// ClampScrollRegion rewrites DECSTBM (`CSI Pt;Pb r`, set top/bottom margins)
// and DECSLRM (`CSI Pl;Pr s`, set left/right margins) sequences whose
// second parameter exceeds the target emulator's dimensions, clamping it to
// rows (for 'r') or cols (for 's') before the sequence reaches x/vt.
//
// Replay and preview feed a byte tail captured from a session's on-disk PTY
// log into a FRESH (or resized) emulator sized for whatever is doing the
// replaying — the current pane geometry — which is not necessarily the size
// the PTY had when those bytes were originally written. PTY resizes are
// never encoded IN the byte stream (they arrive out-of-band via
// SIGWINCH/ioctl), so a DECSTBM/DECSLRM the agent emitted while the PTY was
// LARGER survives verbatim into replayed bytes and sets a scroll region
// that exceeds the replay emulator's actual buffer. Neither x/vt's
// setVerticalMargins/setHorizontalMargins (screen.go) nor ultraviolet's own
// Buffer.DeleteLineArea/InsertLineArea (buffer.go) clamp the margin/area
// against the buffer's current Width()/Height() — so the next CSI M (DL) or
// CSI L (IL) call indexes past the end of a Lines row/column and panics.
// Verified against the newest upstream commit available at fix time (both
// repos, 2026-09-02): not yet patched there. See gotchas/pty-terminal.md.
//
// Only the exact, unambiguous `CSI <digits>[;<digits>] r|s` shape is
// rewritten — no private markers, no intermediates, no sub-parameters (`:`).
// Any other form, and any sequence whose second parameter is already within
// bounds or omitted (omitted defaults to the CURRENT dimension inside x/vt
// itself, which is always safe), is passed through byte-for-byte untouched
// — matching FilterOSC's conservative stance of only acting where the
// sequence is unambiguous.
func ClampScrollRegion(in []byte, cols, rows int) []byte {
	if cols <= 0 || rows <= 0 || len(in) == 0 {
		return in
	}
	out := make([]byte, 0, len(in))
	i := 0
	for i < len(in) {
		b := in[i]
		if b != 0x1b || i+1 >= len(in) || in[i+1] != '[' {
			out = append(out, b)
			i++
			continue
		}

		// Scan a possible CSI sequence starting at i for its final byte.
		j := i + 2
		plain := true // params are digits/';' only — no intermediates/private markers/subparams
		var final byte
		for j < len(in) && j-i < maxScrollRegionCSIScan {
			c := in[j]
			switch {
			case c >= 0x40 && c <= 0x7e: // final byte
				final = c
			case c >= 0x30 && c <= 0x3f: // parameter byte
				if c != ';' && (c < '0' || c > '9') {
					plain = false
				}
				j++
				continue
			case c >= 0x20 && c <= 0x2f: // intermediate byte
				plain = false
				j++
				continue
			}
			// Either we just found the final byte, or c is not a valid CSI
			// byte at all (e.g. a stray control char) — either way, stop
			// scanning; final stays 0 in the latter case.
			break
		}

		if final == 0 {
			// No recognizable final byte within the scan window: emit just
			// the ESC and reprocess the rest byte-by-byte (matches oscFilter's
			// posture of never dropping unboundedly).
			out = append(out, b)
			i++
			continue
		}

		seq := in[i : j+1] // full sequence: ESC [ params final
		if plain && (final == 'r' || final == 's') {
			seq = clampMarginParams(seq, cols, rows, final)
		}
		out = append(out, seq...)
		i = j + 1
	}
	return out
}

// clampMarginParams parses the numeric parameters of a `CSI Pt[;Pb] r` or
// `CSI Pl[;Pr] s` sequence and, if the second parameter is present and
// exceeds max, rewrites it to max. Returns seq unchanged in every other case
// (missing second param, already within bounds, or unparseable — the last
// of which should be unreachable given ClampScrollRegion only calls this
// when `plain` restricted every parameter byte to digits and ';').
func clampMarginParams(seq []byte, cols, rows int, final byte) []byte {
	params := seq[2 : len(seq)-1] // strip "ESC [" prefix and final byte
	parts := strings.Split(string(params), ";")
	if len(parts) != 2 || parts[1] == "" {
		// Missing/omitted second param defaults to the emulator's OWN
		// current dimension inside x/vt — always safe, nothing to clamp.
		return seq
	}

	max := rows
	if final == 's' {
		max = cols
	}

	second, err := strconv.Atoi(parts[1])
	if err != nil || second <= max {
		return seq
	}

	var b strings.Builder
	b.WriteByte(0x1b)
	b.WriteByte('[')
	b.WriteString(parts[0])
	b.WriteByte(';')
	b.WriteString(strconv.Itoa(max))
	b.WriteByte(final)
	return []byte(b.String())
}
