package terminal

import "github.com/drn/argus/internal/oscfilter"

// FilterOSC strips OSC sequences from a complete buffer in a single pass; see
// package oscfilter for why this is necessary (x/ansi treats a 0x9C byte inside
// a UTF-8 OSC title as a C1 String Terminator). Use for one-shot whole-buffer
// feeds (replay/rebuild/preview); incremental live feeds hold a persistent
// oscfilter.Filter so split sequences survive across chunks.
func FilterOSC(in []byte) []byte { return oscfilter.Strip(in) }
