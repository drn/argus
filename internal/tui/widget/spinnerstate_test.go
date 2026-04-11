package widget

import "testing"

func TestSpinnerFrame(t *testing.T) {
	// Default (progress): 6 frames cycling through ee06–ee0b.
	SetActiveSpinner("progress")
	defer SetActiveSpinner("progress")

	expected := []rune{'\uEE06', '\uEE07', '\uEE08', '\uEE09', '\uEE0A', '\uEE0B'}
	for i, want := range expected {
		if got := SpinnerFrame(i); got != want {
			t.Errorf("SpinnerFrame(%d) = %U, want %U", i, got, want)
		}
	}
	// Wraps around.
	if got := SpinnerFrame(6); got != '\uEE06' {
		t.Errorf("SpinnerFrame(6) = %U, want %U (wrap)", got, '\uEE06')
	}
}

func TestSetActiveSpinner(t *testing.T) {
	defer SetActiveSpinner("progress")

	SetActiveSpinner("classic")
	if got := SpinnerFrameCount(); got != 4 {
		t.Errorf("classic FrameCount = %d, want 4", got)
	}
	if got := SpinnerFrame(0); got != '|' {
		t.Errorf("classic Frame(0) = %c, want |", got)
	}

	// Unknown style falls back to progress.
	SetActiveSpinner("nonexistent")
	if got := SpinnerFrameCount(); got != 6 {
		t.Errorf("fallback FrameCount = %d, want 6", got)
	}
}
