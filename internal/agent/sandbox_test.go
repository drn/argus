package agent

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/model"
)

func TestGenerateSandboxConfig_BasicPaths(t *testing.T) {
	cfg := config.Config{}
	worktree := "/home/user/.argus/worktrees/myapp/fix-bug"

	path, cleanup, err := GenerateSandboxConfig(worktree, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	if path == "" {
		t.Fatal("expected non-empty path")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	var settings srtSettings
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatal(err)
	}

	// Check allowWrite contains worktree and /tmp
	aw := settings.Filesystem.AllowWrite
	if !containsString(aw, "//home/user/.argus/worktrees/myapp/fix-bug") {
		t.Errorf("allowWrite missing worktree path, got %v", aw)
	}
	if !containsString(aw, "//tmp") {
		t.Errorf("allowWrite missing /tmp, got %v", aw)
	}

	// Check denyRead contains credential dirs
	dr := settings.Filesystem.DenyRead
	for _, expected := range []string{"~/.ssh", "~/.gnupg", "~/.aws", "~/.kube"} {
		if !containsString(dr, expected) {
			t.Errorf("denyRead missing %q, got %v", expected, dr)
		}
	}

	// Check default allowed domains
	ad := settings.Network.AllowedDomains
	for _, expected := range []string{"api.anthropic.com", "statsig.anthropic.com", "sentry.io"} {
		if !containsString(ad, expected) {
			t.Errorf("allowedDomains missing %q, got %v", expected, ad)
		}
	}
}

func TestGenerateSandboxConfig_CustomConfig(t *testing.T) {
	cfg := config.Config{
		Sandbox: config.SandboxConfig{
			AllowedDomains: []string{"github.com", "npmjs.org"},
			DenyRead:       []string{"/secrets"},
			ExtraWrite:     []string{"~/.npm", "/var/cache"},
		},
	}
	worktree := "/tmp/wt"

	path, cleanup, err := GenerateSandboxConfig(worktree, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	var settings srtSettings
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatal(err)
	}

	// Custom domains should be merged with defaults
	ad := settings.Network.AllowedDomains
	if !containsString(ad, "github.com") {
		t.Errorf("allowedDomains missing github.com, got %v", ad)
	}
	if !containsString(ad, "api.anthropic.com") {
		t.Errorf("allowedDomains missing default api.anthropic.com, got %v", ad)
	}

	// Custom deny read paths
	dr := settings.Filesystem.DenyRead
	if !containsString(dr, "//secrets") {
		t.Errorf("denyRead missing /secrets, got %v", dr)
	}

	// Custom extra write paths
	aw := settings.Filesystem.AllowWrite
	if !containsString(aw, "~/.npm") {
		t.Errorf("allowWrite missing ~/.npm, got %v", aw)
	}
	if !containsString(aw, "//var/cache") {
		t.Errorf("allowWrite missing /var/cache, got %v", aw)
	}
}

func TestGenerateSandboxConfig_Cleanup(t *testing.T) {
	cfg := config.Config{}
	path, cleanup, err := GenerateSandboxConfig("/tmp/wt", cfg)
	if err != nil {
		t.Fatal(err)
	}

	// File should exist before cleanup
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("settings file should exist: %v", err)
	}

	cleanup()

	// File should be gone after cleanup
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("settings file should be removed after cleanup")
	}
}

func TestGenerateSandboxConfig_NoDuplicateDomains(t *testing.T) {
	cfg := config.Config{
		Sandbox: config.SandboxConfig{
			AllowedDomains: []string{"api.anthropic.com", "custom.io"},
		},
	}

	path, cleanup, err := GenerateSandboxConfig("/tmp/wt", cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	var settings srtSettings
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatal(err)
	}

	// Count occurrences of api.anthropic.com
	count := 0
	for _, d := range settings.Network.AllowedDomains {
		if d == "api.anthropic.com" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("api.anthropic.com should appear once, got %d times in %v",
			count, settings.Network.AllowedDomains)
	}
}

func TestWrapWithSandbox_NpxFallback(t *testing.T) {
	// When srtPath is "npx", should use npx invocation
	oldPath := srtPath
	srtPath = "npx"
	defer func() { srtPath = oldPath }()

	result := WrapWithSandbox("claude --dangerously-skip-permissions", "/tmp/sandbox.json")
	if !strings.HasPrefix(result, "npx @anthropic-ai/sandbox-runtime") {
		t.Errorf("expected npx prefix, got %q", result)
	}
	if !strings.Contains(result, "--settings '/tmp/sandbox.json'") {
		t.Errorf("expected settings path, got %q", result)
	}
	if !strings.Contains(result, "-- claude --dangerously-skip-permissions") {
		t.Errorf("expected original command after --, got %q", result)
	}
}

func TestWrapWithSandbox_DirectBinary(t *testing.T) {
	oldPath := srtPath
	srtPath = "/usr/local/bin/srt"
	defer func() { srtPath = oldPath }()

	result := WrapWithSandbox("claude", "/tmp/sandbox.json")
	if !strings.HasPrefix(result, "'/usr/local/bin/srt'") {
		t.Errorf("expected direct binary path, got %q", result)
	}
}

func TestNormalizeSrtPath(t *testing.T) {
	tests := []struct {
		input, expected string
	}{
		{"~/.ssh", "~/.ssh"},
		{"//tmp", "//tmp"},
		{"/usr/local", "//usr/local"},
		{"./relative", "./relative"},
		{"  /spaced  ", "//spaced"},
	}
	for _, tt := range tests {
		got := normalizeSrtPath(tt.input)
		if got != tt.expected {
			t.Errorf("normalizeSrtPath(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestBuildCmd_WithSandboxDisabled(t *testing.T) {
	cfg := testConfig()
	cfg.Sandbox.Enabled = false
	task := &model.Task{
		Name:     "test",
		Prompt:   "hello",
		Worktree: "/tmp/wt",
	}

	cmd, cleanup, err := BuildCmd(task, cfg, false)
	if err != nil {
		t.Fatal(err)
	}

	// No sandbox wrapping when disabled
	if strings.Contains(cmd.Args[2], "sandbox-runtime") {
		t.Errorf("sandbox should not be in command when disabled: %q", cmd.Args[2])
	}
	if cleanup != nil {
		t.Error("cleanup should be nil when sandbox disabled")
	}
}
