package model

import (
	"testing"

	"github.com/drn/argus/internal/testutil"
)

func TestBuildToDoPrompt(t *testing.T) {
	t.Run("no user prompt uses note directly", func(t *testing.T) {
		got := BuildToDoPrompt("", "note content")
		testutil.Equal(t, got, "note content")
	})

	t.Run("no note content uses user prompt directly", func(t *testing.T) {
		got := BuildToDoPrompt("fix the bug", "")
		testutil.Equal(t, got, "fix the bug")
	})

	t.Run("both combines with context tags", func(t *testing.T) {
		got := BuildToDoPrompt("fix the bug", "details here")
		want := "fix the bug\n\n<context>\ndetails here\n</context>"
		testutil.Equal(t, got, want)
	})

	t.Run("both empty returns empty", func(t *testing.T) {
		got := BuildToDoPrompt("", "")
		testutil.Equal(t, got, "")
	})
}
