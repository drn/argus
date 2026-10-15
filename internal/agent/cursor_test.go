package agent

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/drn/argus/internal/testutil"
)

func TestCaptureCursorSessionID(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	wt := t.TempDir()
	dir := filepath.Join(home, ".cursor", "chats", cursorWorkspaceHash(canonPath(wt)))

	_, err := CaptureCursorSessionID(wt)
	testutil.Equal(t, err != nil, true)
	_, err = CaptureCursorSessionID("")
	testutil.Equal(t, err != nil, true)

	mk := func(name string, age time.Duration) {
		p := filepath.Join(dir, name)
		testutil.NoError(t, os.MkdirAll(p, 0o755))
		ts := time.Now().Add(-age)
		testutil.NoError(t, os.Chtimes(p, ts, ts))
	}
	mk("old-chat", time.Hour)
	mk("new-chat", time.Minute)
	mk("bad id!", 0)
	testutil.NoError(t, os.WriteFile(filepath.Join(dir, "stray-file"), nil, 0o644))

	id, err := CaptureCursorSessionID(wt)
	testutil.NoError(t, err)
	testutil.Equal(t, id, "new-chat")
}

func TestCursorWorkspaceHash(t *testing.T) {
	testutil.Equal(t, cursorWorkspaceHash("/a"), "0639767f3e9eaad729b54037a7e2abf5")
}
