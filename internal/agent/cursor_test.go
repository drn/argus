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

func TestCaptureCursorSessionID_AsGivenPathAndStoreDBMtime(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	testutil.NoError(t, os.Symlink(real, link))
	chats := filepath.Join(home, ".cursor", "chats")

	// Chats seeded only under the as-given (symlink) path hash are found.
	asGiven := filepath.Join(chats, cursorWorkspaceHash(filepath.Clean(link)))
	testutil.NoError(t, os.MkdirAll(filepath.Join(asGiven, "only-chat"), 0o755))
	id, err := CaptureCursorSessionID(link)
	testutil.NoError(t, err)
	testutil.Equal(t, id, "only-chat")

	// Across both hashes, and via store.db mtime, the most recently active chat wins.
	canon := filepath.Join(chats, cursorWorkspaceHash(canonPath(link)))
	testutil.NoError(t, os.MkdirAll(filepath.Join(canon, "canon-chat"), 0o755))
	old := time.Now().Add(-time.Hour)
	testutil.NoError(t, os.Chtimes(filepath.Join(canon, "canon-chat"), old, old))
	testutil.NoError(t, os.Chtimes(filepath.Join(asGiven, "only-chat"), old.Add(time.Minute), old.Add(time.Minute)))
	db := filepath.Join(canon, "canon-chat", "store.db")
	testutil.NoError(t, os.WriteFile(db, nil, 0o644))
	id, err = CaptureCursorSessionID(link)
	testutil.NoError(t, err)
	testutil.Equal(t, id, "canon-chat")
}

func TestCaptureCursorSessionID_OnlyInvalidIDs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	wt := t.TempDir()
	testutil.NoError(t, os.MkdirAll(filepath.Join(home, ".cursor", "chats", cursorWorkspaceHash(canonPath(wt)), "-bad id"), 0o755))
	_, err := CaptureCursorSessionID(wt)
	testutil.Equal(t, err != nil, true)
}
