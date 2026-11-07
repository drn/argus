package agent

import (
	"io"
	"runtime"
	"strings"
	"testing"

	xvt "github.com/charmbracelet/x/vt"

	"github.com/drn/argus/internal/oscfilter"
	"github.com/drn/argus/internal/testutil"
)

// legacyRender reproduces ScreenRenderer.render BEFORE the scrollback cap:
// one long-lived emulator with x/vt's default scrollback, RIS-reset per call.
type legacyRender struct{ emu *xvt.SafeEmulator }

func (l *legacyRender) render(tail []byte, cols, rows int) string {
	if l.emu == nil {
		l.emu = xvt.NewSafeEmulator(cols, rows)
		go io.Copy(io.Discard, l.emu) //nolint:errcheck
	} else {
		safeEmuWrite(l.emu, risReset)
		l.emu.Resize(cols, rows)
	}
	safeEmuWrite(l.emu, oscfilter.Strip(tail))
	return l.emu.String()
}

func equivTails() map[string][]byte {
	var tall strings.Builder
	for i := 0; i < 300; i++ {
		tall.WriteString(strings.Repeat("z", i%90) + "\r\n")
	}
	return map[string][]byte{
		"plain tall":        []byte(tall.String()),
		"alt-screen prompt": []byte(altScreenPromptFrame("3s", "✻")),
		"alt-screen quest":  []byte(altScreenQuestionFrame("5s", "✶", true)),
		"cursor-addressed":  []byte("\x1b[2J\x1b[5;10Hhello\x1b[8;1Hworld\x1b[1;1Hhead\x1b[24;80Htail"),
		"scroll region":     []byte("\x1b[3;10r" + tall.String() + "\x1b[r\x1b[H"),
		"osc title":         []byte("\x1b]0;✳ title\x07body line\r\n"),
		"sgr + wide":        []byte("\x1b[1;31m世界\x1b[0m plain\r\n" + tall.String()),
	}
}

// TestScreenRenderer_EquivalentToUncappedEmulator proves the visible screen is
// byte-identical to the pre-fix behaviour (default scrollback) across tail
// shapes, with renders interleaved and sizes varied on one shared renderer.
func TestScreenRenderer_EquivalentToUncappedEmulator(t *testing.T) {
	tails := equivTails()
	sizes := [][2]int{{80, 24}, {120, 40}, {80, 24}}
	got, want := &ScreenRenderer{}, &legacyRender{}
	for round := 0; round < 3; round++ {
		for name, tail := range tails {
			sz := sizes[round%len(sizes)]
			t.Run(name, func(t *testing.T) {
				testutil.Equal(t, got.render(tail, sz[0], sz[1]), want.render(tail, sz[0], sz[1]))
			})
		}
	}
}

func benchTail() []byte {
	var sb strings.Builder
	for i := 0; i < 400; i++ {
		sb.WriteString(strings.Repeat("x", 200) + "\r\n")
	}
	return []byte(sb.String())
}

func BenchmarkScreenRender_Capped(b *testing.B) {
	tail, r := benchTail(), &ScreenRenderer{}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		r.render(tail, 200, 50)
	}
}

func BenchmarkScreenRender_LegacyUncapped(b *testing.B) {
	tail, r := benchTail(), &legacyRender{}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		r.render(tail, 200, 50)
	}
}

// TestScreenRender_RetainedHeapStaysBounded asserts the capped renderer's
// retained heap stays small after many tall renders. The legacy uncapped
// emulator retained ~260MB after 100 renders (see PR numbers); only the capped
// side is asserted here so the test stays fast under -race. Heap deltas are
// signed: a GC between the two readings can leave the heap SMALLER than the
// baseline, which an unsigned subtraction turns into a huge bogus number.
func TestScreenRender_RetainedHeapStaysBounded(t *testing.T) {
	if testing.Short() {
		t.Skip("memory measurement")
	}
	tail := benchTail()
	r := &ScreenRenderer{}
	var a, b runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&a)
	for i := 0; i < 30; i++ {
		r.render(tail, 200, 50)
	}
	runtime.GC()
	runtime.ReadMemStats(&b)
	deltaMB := (float64(b.HeapAlloc) - float64(a.HeapAlloc)) / 1e6
	t.Logf("retained heap after 30 renders: %.1fMB", deltaMB)
	if deltaMB > 20 {
		t.Fatalf("capped renderer retains %.1fMB, want <= 20MB", deltaMB)
	}
	runtime.KeepAlive(r)
}
