package project

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetectLanguage(t *testing.T) {
	tests := []struct {
		file string
		want string
	}{
		{"go.mod", "go"},
		{"Cargo.toml", "rust"},
		{"package.json", "node"},
		{"tsconfig.json", "typescript"},
		{"Gemfile", "ruby"},
		{"requirements.txt", "python"},
		{"setup.py", "python"},
		{"pyproject.toml", "python"},
		{"pom.xml", "java"},
		{"build.gradle", "java"},
		{"mix.exs", "elixir"},
	}

	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			dir := t.TempDir()
			os.WriteFile(filepath.Join(dir, tt.file), []byte{}, 0o644)

			if got := DetectLanguage(dir); got != tt.want {
				t.Errorf("DetectLanguage() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDetectLanguage_Empty(t *testing.T) {
	dir := t.TempDir()
	if got := DetectLanguage(dir); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestDetectLanguage_Priority(t *testing.T) {
	// go.mod should win over package.json
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte{}, 0o644)
	os.WriteFile(filepath.Join(dir, "package.json"), []byte{}, 0o644)

	if got := DetectLanguage(dir); got != "go" {
		t.Errorf("expected go (higher priority), got %q", got)
	}
}

func TestDetectIcon(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte{}, 0o644)

	icon := DetectIcon(dir)
	if icon == "" || icon == "\uf115" {
		t.Error("expected Go icon, got fallback")
	}
}

func TestDetectIcon_Fallback(t *testing.T) {
	dir := t.TempDir()
	if got := DetectIcon(dir); got != "\uf115" {
		t.Errorf("expected folder fallback icon, got %q", got)
	}
}

func TestDetectIcon_GitFallback(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, ".git"), 0o755)

	icon := DetectIcon(dir)
	if icon == "\uf115" {
		t.Error("expected git icon, not folder fallback")
	}
}
