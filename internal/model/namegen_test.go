package model

import (
	"strings"
	"testing"
)

func TestGenerateName(t *testing.T) {
	name := GenerateName()
	parts := strings.Split(name, "-")
	if len(parts) != 3 {
		t.Fatalf("expected 3 parts, got %d: %q", len(parts), name)
	}
	if parts[1] == parts[2] {
		t.Errorf("nouns should not repeat: %q", name)
	}
}

func TestGenerateNameFromPrompt(t *testing.T) {
	tests := []struct {
		prompt string
		want   string
	}{
		{"fix the authentication token refresh bug", "fix-authentication-token-refresh"},
		{"add retry logic to the API client", "add-retry-logic-api"},
		{"refactor database connection pooling", "refactor-database-connection-pooling"},
		{"update the nav styles for mobile", "update-nav-styles-mobile"},
	}
	for _, tt := range tests {
		got := GenerateNameFromPrompt(tt.prompt)
		if got != tt.want {
			t.Errorf("GenerateNameFromPrompt(%q) = %q, want %q", tt.prompt, got, tt.want)
		}
	}
}

func TestGenerateNameFromPromptFallback(t *testing.T) {
	// All stop words should fall back to random name
	name := GenerateNameFromPrompt("the and or but")
	parts := strings.Split(name, "-")
	if len(parts) != 3 {
		t.Fatalf("fallback should produce 3-part random name, got %q", name)
	}
}

func TestExtractKeywords(t *testing.T) {
	tests := []struct {
		prompt string
		max    int
		want   string
	}{
		{"Fix the broken login page", 4, "fix-broken-login-page"},
		{"UPPERCASE words SHOULD work", 3, "uppercase-words-work"},
		{"a the an or but", 4, ""},
		{"add --verbose flag to CLI", 4, "add-verbose-flag-cli"},
		{"x", 4, ""},
	}
	for _, tt := range tests {
		got := extractKeywords(tt.prompt, tt.max)
		if got != tt.want {
			t.Errorf("extractKeywords(%q, %d) = %q, want %q", tt.prompt, tt.max, got, tt.want)
		}
	}
}

func TestGenerateNameUniqueness(t *testing.T) {
	seen := make(map[string]bool)
	for range 100 {
		seen[GenerateName()] = true
	}
	if len(seen) < 90 {
		t.Errorf("expected at least 90 unique names in 100 draws, got %d", len(seen))
	}
}
