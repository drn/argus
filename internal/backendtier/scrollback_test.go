package backendtier

import (
	"strings"
	"testing"
)

func TestRenderCodexPTY_ScrollbackBounded(t *testing.T) {
	raw := []byte(strings.Repeat(strings.Repeat("x", 100)+"\r\n", 300))
	for i := 0; i < 20; i++ {
		renderCodexPTY(raw)
	}
	codexRenderState.mu.Lock()
	defer codexRenderState.mu.Unlock()
	if n := codexRenderState.emu.ScrollbackLen(); n > 1 {
		t.Fatalf("scrollback = %d, want <= 1", n)
	}
}
