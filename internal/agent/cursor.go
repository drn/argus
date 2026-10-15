package agent

import (
	"crypto/md5" //nolint:gosec // Cursor keys chat dirs by md5(cwd); not a security use
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

var cursorChatIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

// cursorChatsDir returns ~/.cursor/chats, where cursor-agent keeps one
// <md5(cwd)>/<chat-uuid>/store.db per conversation.
func cursorChatsDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cursor chats dir: home dir: %w", err)
	}
	return filepath.Join(home, ".cursor", "chats"), nil
}

func cursorWorkspaceHash(path string) string {
	sum := md5.Sum([]byte(path)) //nolint:gosec
	return hex.EncodeToString(sum[:])
}

// CaptureCursorSessionID returns the newest chat ID under the worktree's chat
// directory. The hash input is cursor's resolved cwd, so both the canonical and
// the as-given worktree paths are tried.
func CaptureCursorSessionID(worktreePath string) (string, error) {
	if worktreePath == "" {
		return "", fmt.Errorf("CaptureCursorSessionID: worktree path is empty")
	}
	chats, err := cursorChatsDir()
	if err != nil {
		return "", err
	}
	var bestID string
	var bestMod int64
	for _, p := range []string{canonPath(worktreePath), filepath.Clean(worktreePath)} {
		entries, err := os.ReadDir(filepath.Join(chats, cursorWorkspaceHash(p)))
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() || !cursorChatIDPattern.MatchString(e.Name()) {
				continue
			}
			info, err := e.Info()
			if err != nil {
				continue
			}
			// A dir's mtime only moves on entry create/remove, so also consult store.db.
			mod := info.ModTime().UnixNano()
			if db, err := os.Stat(filepath.Join(chats, cursorWorkspaceHash(p), e.Name(), "store.db")); err == nil {
				mod = max(mod, db.ModTime().UnixNano())
			}
			if m := mod; bestID == "" || m > bestMod {
				bestID, bestMod = e.Name(), m
			}
		}
	}
	if bestID == "" {
		return "", fmt.Errorf("CaptureCursorSessionID: no chat for worktree %s", worktreePath)
	}
	return bestID, nil
}
