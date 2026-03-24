package tui2

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/sanitize"
	"github.com/drn/argus/internal/testutil"
)

func TestStripANSI(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"plain text", "hello world", "hello world"},
		{"bold", "\x1b[1mhello\x1b[0m", "hello"},
		{"color", "\x1b[31mred\x1b[0m text", "red text"},
		{"cursor", "\x1b[?25l\x1b[?25h", ""},
		{"osc title", "\x1b]0;title\x07rest", "rest"},
		{"mixed", "\x1b[1;32m=> \x1b[0mDone", "=> Done"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testutil.Equal(t, sanitize.StripANSI(tt.in), tt.want)
		})
	}
}

func TestWriteForkContextFiles(t *testing.T) {
	dir := t.TempDir()

	ctx := &forkContext{
		SourceName:   "original-task",
		SourcePrompt: "Fix the bug in main.go",
		SourceStatus: "in_progress",
		SourceBranch: "argus/original-task",
		RecentOutput: "Building... done.\nTests passed.",
		GitDiff:      "diff --git a/main.go b/main.go\n--- a/main.go\n+++ b/main.go\n",
	}

	err := writeForkContextFiles(dir, ctx)
	testutil.NoError(t, err)

	// Verify fork-source.md exists and contains source info.
	src, err := os.ReadFile(filepath.Join(dir, ".context", "fork-source.md"))
	testutil.NoError(t, err)
	testutil.Contains(t, string(src), "original-task")
	testutil.Contains(t, string(src), "Fix the bug in main.go")
	testutil.Contains(t, string(src), "argus/original-task")

	// Verify fork-output.md exists.
	out, err := os.ReadFile(filepath.Join(dir, ".context", "fork-output.md"))
	testutil.NoError(t, err)
	testutil.Contains(t, string(out), "Tests passed.")

	// Verify fork-diff.patch exists.
	diff, err := os.ReadFile(filepath.Join(dir, ".context", "fork-diff.patch"))
	testutil.NoError(t, err)
	testutil.Contains(t, string(diff), "diff --git")
}

func TestWriteForkContextFiles_EmptyOutput(t *testing.T) {
	dir := t.TempDir()

	ctx := &forkContext{
		SourceName:   "new-task",
		SourcePrompt: "Do something",
		SourceStatus: "pending",
	}

	err := writeForkContextFiles(dir, ctx)
	testutil.NoError(t, err)

	// fork-source.md should exist.
	_, err = os.Stat(filepath.Join(dir, ".context", "fork-source.md"))
	testutil.NoError(t, err)

	// fork-output.md and fork-diff.patch should NOT exist.
	_, err = os.Stat(filepath.Join(dir, ".context", "fork-output.md"))
	testutil.Equal(t, os.IsNotExist(err), true)

	_, err = os.Stat(filepath.Join(dir, ".context", "fork-diff.patch"))
	testutil.Equal(t, os.IsNotExist(err), true)
}

func TestBuildForkPrompt(t *testing.T) {
	source := &model.Task{
		Name:   "original",
		Prompt: "Fix the login bug",
	}

	t.Run("with all context", func(t *testing.T) {
		ctx := &forkContext{
			RecentOutput: "some output",
			GitDiff:      "some diff",
		}
		prompt := buildForkPrompt(source, ctx)
		testutil.Contains(t, prompt, ".context/")
		testutil.Contains(t, prompt, "Fix the login bug")
		testutil.Contains(t, prompt, "fork-source.md")
		testutil.Contains(t, prompt, "fork-output.md")
		testutil.Contains(t, prompt, "fork-diff.patch")
	})

	t.Run("with empty context", func(t *testing.T) {
		ctx := &forkContext{}
		prompt := buildForkPrompt(source, ctx)
		testutil.Contains(t, prompt, "fork-source.md")
		testutil.Contains(t, prompt, "Fix the login bug")
		// Should NOT reference files that weren't written.
		if strings.Contains(prompt, "fork-output.md") {
			t.Error("prompt references fork-output.md but no output was extracted")
		}
		if strings.Contains(prompt, "fork-diff.patch") {
			t.Error("prompt references fork-diff.patch but no diff was extracted")
		}
	})
}

func TestSanitizeForkOutput(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			"spinner lines removed",
			"✳\n✶\n✻\n✽\n✢\n·\n",
			"",
		},
		{
			"thinking lines removed",
			"(thinking)\n✶ping…(thinking)\n✳(thinking)\n",
			"",
		},
		{
			"warping and clauding lines removed",
			"✻Warping…\nWarping…\nClauding…\n✶Clauding…\n✢ Warping… (thinking)\n✽ Warping… (thinking)\n",
			"",
		},
		{
			"status bar chrome removed",
			"⏵⏵bypasspermissionson (shift+tabtocycle)·esctointerrupt1MCPserverfailed ·/mcp\n⏵⏵ bypass permissions on (shift+tab to cycle) · esc to interrupt\n",
			"",
		},
		{
			"separator lines removed",
			"──────────────────────────────────────────────────────────────────────\n",
			"",
		},
		{
			"prompt lines removed",
			"❯  \n❯\n",
			"",
		},
		{
			"partial warping chars removed",
			"W\n\na\n\n✢r\n\nWp(thinking)\n\n✳ai\n\nrpng\n\n✶i…\n\nn(thinking)\n\ng\n\n✻…\n",
			"",
		},
		{
			"partial clauding chars removed",
			"Cl\n\na\n\nCu\n\n✻ld\n\nai\n\n✶un\n\ndg\n\n✳i…\n\nn\n\ng\n\n✢…(30s · ↑342 tokens)\n",
			"",
		},
		{
			"timing and token hints removed",
			" (3s)(ctrl+b ctrl+b (twice) to run in background)\n(30s · ↑342 tokens)\n",
			"",
		},
		{
			"consecutive blank lines collapsed",
			"hello\n\n\n\n\nworld\n",
			"hello\n\nworld\n",
		},
		{
			"preserves assistant messages",
			"⏺Scan pipeline is running\n",
			"⏺Scan pipeline is running\n",
		},
		{
			"preserves tool calls",
			"Bash(cd /tmp && ls)\n⏺Bash(echo hello)\n",
			"Bash(cd /tmp && ls)\n⏺Bash(echo hello)\n",
		},
		{
			"preserves tool results",
			"⎿  file1.txt\n   file2.txt\n",
			"⎿  file1.txt\n   file2.txt\n",
		},
		{
			"shell cwd reset removed",
			"⎿  Shell cwd was reset to /Users/foo/bar\n",
			"",
		},
		{
			"running marker removed",
			"⎿  Running…\n",
			"",
		},
		{
			"no output marker removed",
			"⏺(No output)\n",
			"",
		},
		{
			"baked for line removed",
			"✻Baked for 31s                ❯                    \n",
			"",
		},
		{
			"expand hint removed",
			"… +11 lines (ctrl+o to expand)\n",
			"",
		},
		{
			"empty input",
			"",
			"",
		},
		{
			"real mixed content",
			"✳ Warping… (thinking)\n⏺All queued up. The pipeline is running:\n\n1. Scan — picks up new files\n2. Auto-tag — matches performer\n✶\n✻\nClauding…\n",
			"⏺All queued up. The pipeline is running:\n\n1. Scan — picks up new files\n2. Auto-tag — matches performer\n",
		},
		{
			"lone digits removed",
			"4\n\n5\n\n1\n",
			"",
		},
		{
			"carriage returns normalized",
			"⏺Bash(echo hello)\r  ⎿  Running…\r✳ Warping…\n",
			"⏺Bash(echo hello)\n",
		},
		{
			"nbsp replaced",
			"⎿ \u00a0Running…\n",
			"",
		},
		{
			"empty assistant markers removed",
			"⏺\n⏺  \n",
			"",
		},
		{
			"keybind hints removed",
			"(ctrl+b ctrl+b (twice) to run in background)\n",
			"",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitize.CleanPTYOutput(tt.in)
			testutil.Equal(t, got, tt.want)
		})
	}
}

func TestSanitizeForkOutput_RealWorld(t *testing.T) {
	// Simulate the kind of noisy output seen in real fork captures.
	noisy := strings.Join([]string{
		"m⏺Bash(cd /tmp&&ls 2>&1)  ⎿  Running…                              ✳ Warping… (thinking)                              ──────────────────────────────────────────────────",
		"❯  ",
		"──────────────────────────────────────────────────────────────────────",
		"⏵⏵bypasspermissionson (shift+tabtocycle)·esctointerrupt1MCPserverfailed ·/mcp",
		"✶ping…(thinking)",
		"",
		"",
		"",
		"✻Warping…",
		"",
		"",
		"✽",
		"",
		"⏺The scan completed successfully.",
		"",
		"✳",
		"✶",
		"Clauding…",
		" Bash(tts -s1.1 \"done\")  ⎿  Running…                              ✻ Clauding…                              ──────────────────────────────────────────────────",
		"❯  ",
		"⏵⏵ bypass permissions on (shift+tab to cycle) · esc to interrupt",
		"✻Baked for 31s                ❯                    ",
		"",
	}, "\n")

	got := sanitize.CleanPTYOutput(noisy)

	// Should preserve the meaningful content.
	testutil.Contains(t, got, "⏺Bash(cd /tmp&&ls 2>&1)")
	testutil.Contains(t, got, "⏺The scan completed successfully.")
	testutil.Contains(t, got, "Bash(tts -s1.1 \"done\")")

	// Should NOT contain noise.
	if strings.Contains(got, "Warping") {
		t.Errorf("output still contains Warping noise:\n%s", got)
	}
	if strings.Contains(got, "Clauding") {
		t.Errorf("output still contains Clauding noise:\n%s", got)
	}
	if strings.Contains(got, "bypass permissions") {
		t.Errorf("output still contains status bar noise:\n%s", got)
	}
	if strings.Contains(got, "────") {
		t.Errorf("output still contains separator noise:\n%s", got)
	}

	// Count lines — should be dramatically reduced.
	lines := strings.Split(strings.TrimRight(got, "\n"), "\n")
	if len(lines) > 10 {
		t.Errorf("expected ≤10 lines after sanitization, got %d:\n%s", len(lines), got)
	}
}

func TestReadSessionLogTail(t *testing.T) {
	// Reading a non-existent log should return empty.
	result := readSessionLogTail("nonexistent-task-id-12345")
	testutil.Equal(t, result, "")
}
