package agent

import (
	"io"
	"strings"
	"testing"

	xvt "github.com/charmbracelet/x/vt"

	"github.com/drn/argus/internal/oscfilter"
	"github.com/drn/argus/internal/testutil"
)

// TestScreenRenderer_ScrollbackStaysBounded proves the RIS-per-render reset
// does NOT clear x/vt's scrollback: every render of a tail taller than the
// screen pushes lines into the shared emulator's scrollback, which then grows
// monotonically toward x/vt's 10K-line default across ticks.
func TestScreenRenderer_ScrollbackStaysBounded(t *testing.T) {
	r := &ScreenRenderer{}
	var sb strings.Builder
	for i := 0; i < 200; i++ {
		sb.WriteString(strings.Repeat("x", 120))
		sb.WriteString("\r\n")
	}
	tail := []byte(sb.String())

	r.render(tail, 120, 24)
	first := r.emu.ScrollbackLen()
	for i := 0; i < 50; i++ {
		// Alternate sizes so the Resize branch is exercised too.
		if i%2 == 0 {
			r.render(tail, 120, 24)
		} else {
			r.render(tail, 90, 10)
		}
	}
	last := r.emu.ScrollbackLen()
	t.Logf("scrollback after 1 render=%d, after 51 renders=%d", first, last)
	if first > 1 || last > 1 {
		t.Fatalf("scrollback first=%d last=%d, want <= 1", first, last)
	}
}

// TestScreenRenderer_VisibleOutputUnchangedByScrollbackCap checks the
// visible screen matches an emulator that keeps x/vt's default scrollback.
func TestScreenRenderer_VisibleOutputUnchangedByScrollbackCap(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < 120; i++ {
		sb.WriteString("line " + strings.Repeat("y", i%40) + "\r\n")
	}
	tail := []byte(sb.String())

	ref := xvt.NewSafeEmulator(80, 24)
	go io.Copy(io.Discard, ref) //nolint:errcheck
	safeEmuWrite(ref, oscfilter.Strip(tail))

	r := &ScreenRenderer{}
	r.render([]byte("warm up\r\n"), 80, 24)
	testutil.Equal(t, r.render(tail, 80, 24), ref.String())
}
