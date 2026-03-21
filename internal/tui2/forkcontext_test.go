package tui2

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/drn/argus/internal/model"
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
			testutil.Equal(t, stripANSI(tt.in), tt.want)
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

func TestReadSessionLogTail(t *testing.T) {
	// Reading a non-existent log should return empty.
	result := readSessionLogTail("nonexistent-task-id-12345")
	testutil.Equal(t, result, "")
}
