// Package claudesession discovers and parses Claude Code conversation
// sessions stored on disk so Argus can present them for resume/switching.
//
// Claude Code writes one JSONL file per session under
// ~/.claude/projects/<encoded-cwd>/<session-uuid>.jsonl, where the directory
// name is the absolute worktree path with every non-alphanumeric character
// replaced by '-'. There is no sidecar index; metadata (human title, branch,
// linked PR, timestamps) is embedded as entries inside the JSONL file.
//
// This package is pure: it has no UI or DB dependencies and never mutates
// anything on disk. It is the single source of truth for "what Claude
// sessions exist for this worktree" — both the session switcher and (future)
// the /clear session-recapture fix should read through List.
package claudesession

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// sessionIDRe validates that a JSONL filename stem looks like a UUID — guards
// against picking up stray files Claude may drop in the project directory.
var sessionIDRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// nonAlnum matches any character Claude Code rewrites to '-' when encoding a
// working-directory path into a ~/.claude/projects/ folder name.
var nonAlnum = regexp.MustCompile(`[^a-zA-Z0-9]`)

// Session is the metadata Argus needs to display and resume one Claude
// conversation. It mirrors the columns shown by `claude --resume`.
type Session struct {
	ID        string    // session UUID (the JSONL filename stem)
	Title     string    // human-readable title (ai-title entry), or a fallback
	Branch    string    // git branch recorded on the conversation entries
	PRRef     string    // linked PR as "owner/repo#number", empty if none
	ModTime   time.Time // most recent activity timestamp
	SizeBytes int64     // JSONL file size on disk
}

// EncodeProjectDir converts an absolute worktree path into the folder name
// Claude Code uses under ~/.claude/projects/. Claude replaces every
// non-alphanumeric character (including '/', '.', and '-') with '-', so the
// transform is lossy and one-directional — never try to decode it back.
func EncodeProjectDir(worktreePath string) string {
	return nonAlnum.ReplaceAllString(worktreePath, "-")
}

// ProjectDir returns the absolute ~/.claude/projects/<encoded> directory for a
// worktree path. It returns an error only if the user's home dir can't be
// resolved.
func ProjectDir(worktreePath string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("claudesession: home dir: %w", err)
	}
	return filepath.Join(home, ".claude", "projects", EncodeProjectDir(worktreePath)), nil
}

// List returns every Claude session for the given worktree, newest activity
// first. A missing project directory is not an error — it returns an empty
// slice (a brand-new task simply has no sessions yet). Malformed JSONL lines
// and unreadable files are skipped rather than aborting the whole listing.
func List(worktreePath string) ([]Session, error) {
	if worktreePath == "" {
		return nil, fmt.Errorf("claudesession: worktree path is empty")
	}
	dir, err := ProjectDir(worktreePath)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("claudesession: read dir %s: %w", dir, err)
	}

	var sessions []Session
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".jsonl")
		if !sessionIDRe.MatchString(id) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		s := parseSession(filepath.Join(dir, e.Name()))
		s.ID = id
		s.SizeBytes = info.Size()
		if s.ModTime.IsZero() {
			s.ModTime = info.ModTime()
		}
		if s.Title == "" {
			s.Title = "(untitled session)"
		}
		sessions = append(sessions, s)
	}

	sort.SliceStable(sessions, func(i, j int) bool {
		return sessions[i].ModTime.After(sessions[j].ModTime)
	})
	return sessions, nil
}

// rawEntry is the minimal projection of a JSONL line we care about. Unknown
// entry types and fields decode to zero values and are ignored.
type rawEntry struct {
	Type         string `json:"type"`
	AITitle      string `json:"aiTitle"`
	GitBranch    string `json:"gitBranch"`
	Slug         string `json:"slug"`
	Timestamp    string `json:"timestamp"`
	PRNumber     int    `json:"prNumber"`
	PRRepository string `json:"prRepository"`
}

// parseSession scans one JSONL file and extracts display metadata. It reads
// line-by-line with an unbounded reader so multi-megabyte tool-result lines
// don't blow a fixed scanner buffer, and tolerates malformed lines.
func parseSession(path string) Session {
	var s Session
	var firstSlug string

	f, err := os.Open(path)
	if err != nil {
		return s
	}
	defer f.Close()

	r := bufio.NewReader(f)
	for {
		line, err := r.ReadBytes('\n')
		if len(line) > 0 {
			var e rawEntry
			if json.Unmarshal(line, &e) == nil {
				applyEntry(&s, &e, &firstSlug)
			}
		}
		if err != nil {
			break // io.EOF or read error: stop with whatever we gathered
		}
	}

	// Fall back to the first user slug when Claude never wrote an ai-title
	// (short or aborted conversations).
	if s.Title == "" && firstSlug != "" {
		s.Title = humanizeSlug(firstSlug)
	}
	return s
}

// applyEntry folds one decoded JSONL entry into the accumulating Session.
// Later entries win for title/branch/PR so the freshest values survive.
func applyEntry(s *Session, e *rawEntry, firstSlug *string) {
	if e.AITitle != "" {
		s.Title = e.AITitle
	}
	if e.GitBranch != "" {
		s.Branch = e.GitBranch
	}
	if e.Type == "pr-link" && e.PRRepository != "" && e.PRNumber > 0 {
		s.PRRef = fmt.Sprintf("%s#%d", e.PRRepository, e.PRNumber)
	}
	if *firstSlug == "" && e.Slug != "" {
		*firstSlug = e.Slug
	}
	if e.Timestamp != "" {
		if t, err := time.Parse(time.RFC3339Nano, e.Timestamp); err == nil && t.After(s.ModTime) {
			s.ModTime = t
		}
	}
}

// humanizeSlug turns a kebab-case slug into a readable title.
func humanizeSlug(slug string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(slug, "-", " ")), " ")
}

// RelativeTime renders a coarse "3 minutes ago" string relative to now,
// matching the granularity of the `claude --resume` listing.
func RelativeTime(t time.Time) string {
	return relativeTimeSince(time.Now(), t)
}

// relativeTimeSince is the testable core of RelativeTime (now is injected).
func relativeTimeSince(now, t time.Time) string {
	if t.IsZero() {
		return "unknown"
	}
	d := now.Sub(t)
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return plural(int(d.Minutes()), "minute")
	case d < 24*time.Hour:
		return plural(int(d.Hours()), "hour")
	default:
		return plural(int(d.Hours()/24), "day")
	}
}

func plural(n int, unit string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s ago", unit)
	}
	return fmt.Sprintf("%d %ss ago", n, unit)
}

// HumanSize renders a byte count the way `claude --resume` does (e.g.
// "296.1KB", "11.6MB"). Bytes under 1KB are shown as "<n>B".
func HumanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%dB", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%cB", float64(n)/float64(div), "KMGT"[exp])
}
