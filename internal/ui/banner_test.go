package ui

import (
	"strings"
	"testing"
)

func TestRenderBanner_WideWidth(t *testing.T) {
	result := renderBanner(100)
	// Wide banner uses block art characters (e.g. "███████") not the word "ARGUS"
	if !strings.Contains(result, "██") {
		t.Error("wide banner should contain block art characters")
	}
	if !strings.Contains(result, "O R C H E S T R A T O R") {
		t.Error("wide banner should contain spaced subtitle")
	}
}

func TestRenderBanner_NarrowWidth(t *testing.T) {
	result := renderBanner(30)
	if !strings.Contains(result, "ARGUS") {
		t.Error("narrow banner should contain compact 'ARGUS'")
	}
	if !strings.Contains(result, "CODE ORCHESTRATOR") {
		t.Error("narrow banner should contain compact subtitle")
	}
}

func TestRenderBanner_ExactBoundary(t *testing.T) {
	// bannerWidth+4 = 45, so 44 should be narrow and 45 should be wide
	narrow := renderBanner(bannerWidth + 3)
	if !strings.Contains(narrow, "CODE ORCHESTRATOR") {
		t.Error("width bannerWidth+3 should use compact format")
	}

	wide := renderBanner(bannerWidth + 4)
	if !strings.Contains(wide, "O R C H E S T R A T O R") {
		t.Error("width bannerWidth+4 should use full banner")
	}
}

func TestRenderBanner_ZeroWidth(t *testing.T) {
	// Should not panic
	result := renderBanner(0)
	if result == "" {
		t.Error("banner with zero width should still produce output")
	}
}
