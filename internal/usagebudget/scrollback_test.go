package usagebudget

import (
	"strings"
	"testing"
)

func TestRenderEmulator_ScrollbackBounded(t *testing.T) {
	chunk := []byte(strings.Repeat(strings.Repeat("x", 100)+"\r\n", 300))
	for i := 0; i < 20; i++ {
		resetEmulator()
		feedEmulator(chunk)
	}
	renderState.mu.Lock()
	defer renderState.mu.Unlock()
	if n := renderState.emu.ScrollbackLen(); n > 1 {
		t.Fatalf("scrollback = %d, want <= 1", n)
	}
}
