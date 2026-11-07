package agent

import (
	"strings"
	"testing"

	"github.com/drn/argus/internal/testutil"
)

func TestTailView_SharedRender(t *testing.T) {
	t.Parallel()
	v := NewTailView(&ScreenRenderer{}, []byte("Finished.\r\n"), 80, 24)
	v.DetectNeedsInputScreen()
	v.AwaitingInputFingerprint()
	v.ParkedSelectionSignal()
	v.ContentIdleFingerprint()
	testutil.Equal(t, v.renderCount, 1)
}

func TestTailView_RawHitAndDistinctWindows(t *testing.T) {
	t.Parallel()
	buf := []byte("unique prefix\n" + strings.Repeat(" ", needsInputTailWindow) + "\n❯ 1. Yes\n")
	v := NewTailView(&ScreenRenderer{}, buf, 80, 24)
	testutil.Equal(t, v.DetectNeedsInputScreen(), true)
	fp, ok := v.AwaitingInputFingerprint()
	testutil.Equal(t, ok, true)
	testutil.Equal(t, fp, ContentFingerprint(buf))
	testutil.Equal(t, v.ParkedSelectionSignal(), true)
	testutil.Equal(t, v.renderCount, 0)
	testutil.Equal(t, strings.Contains(v.windowText(), "unique prefix"), false)
	testutil.Contains(t, v.fullText(), "unique prefix")
	testutil.Equal(t, fp == fingerprintText(v.windowText()), false)
}

func TestTailView_RenderedTextSurvivesOtherSessions(t *testing.T) {
	t.Parallel()
	r := &ScreenRenderer{}
	// Cursor-addressed option forces the emulated path, not a raw hit.
	buf := []byte("\x1b[2J\x1b[1;3H1. Yes\x1b[H❯")
	v := NewTailView(r, buf, 80, 24)
	testutil.Equal(t, v.DetectNeedsInputScreen(), true)
	r.render([]byte("completely different session"), 80, 24)
	testutil.Equal(t, v.ParkedSelectionSignal(), true)
	_, working := v.ContentIdleFingerprint()
	testutil.Equal(t, working, false)
	testutil.Equal(t, v.renderCount, 1)
}

func TestTailView_NilAndEmpty(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		r    *ScreenRenderer
		buf  string
		want bool
	}{
		{"nil empty", nil, "", false},
		{"renderer empty", &ScreenRenderer{}, "", false},
		{"nil prompt", nil, "❯ 1. Yes\n", true},
		{"nil no prompt", nil, "Done.\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := NewTailView(tc.r, []byte(tc.buf), 80, 24)
			testutil.Equal(t, v.DetectNeedsInputScreen(), tc.want)
			_, ok := v.AwaitingInputFingerprint()
			testutil.Equal(t, ok, tc.want)
			testutil.Equal(t, v.ParkedSelectionSignal(), tc.want)
			_, working := v.ContentIdleFingerprint()
			testutil.Equal(t, working, false)
			testutil.Equal(t, v.renderCount, 0)
		})
	}
}
