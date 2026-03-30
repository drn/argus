package spinner

import (
	"testing"

	"github.com/drn/argus/internal/testutil"
)

func TestGet_Known(t *testing.T) {
	for _, s := range All {
		t.Run(string(s.Style), func(t *testing.T) {
			got := Get(s.Style)
			testutil.Equal(t, got.Style, s.Style)
		})
	}
}

func TestGet_UnknownFallsBack(t *testing.T) {
	got := Get("nonexistent")
	testutil.Equal(t, got.Style, StyleProgress)
}

func TestFrame(t *testing.T) {
	s := Get(StyleClassic)
	testutil.Equal(t, s.Frame(0), '|')
	testutil.Equal(t, s.Frame(1), '/')
	testutil.Equal(t, s.Frame(3), '\\')
	// Wraps around.
	testutil.Equal(t, s.Frame(4), '|')
}

func TestFrameCount(t *testing.T) {
	tests := []struct {
		style Style
		want  int
	}{
		{StyleProgress, 6},
		{StyleDots, 10},
		{StyleBraille, 8},
		{StyleClassic, 4},
	}
	for _, tt := range tests {
		t.Run(string(tt.style), func(t *testing.T) {
			testutil.Equal(t, Get(tt.style).FrameCount(), tt.want)
		})
	}
}

func TestNext(t *testing.T) {
	testutil.Equal(t, Next(StyleProgress), StyleDots)
	testutil.Equal(t, Next(StyleClassic), StyleProgress) // wraps
	testutil.Equal(t, Next("bogus"), StyleProgress)      // fallback
}

func TestPrev(t *testing.T) {
	testutil.Equal(t, Prev(StyleProgress), StyleClassic) // wraps
	testutil.Equal(t, Prev(StyleDots), StyleProgress)
	testutil.Equal(t, Prev("bogus"), StyleProgress) // fallback
}

func TestStyles(t *testing.T) {
	styles := Styles()
	testutil.Equal(t, len(styles), len(All))
	testutil.Equal(t, styles[0], StyleProgress)
}
