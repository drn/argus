package ui

import (
	"fmt"
	"strconv"
	"strings"
)

// DiffLineType categorizes a line in a unified diff.
type DiffLineType int

const (
	DiffContext DiffLineType = iota
	DiffAdded
	DiffRemoved
)

// DiffLine is a single line from a parsed unified diff.
type DiffLine struct {
	Type    DiffLineType
	Content string // line content without the +/- prefix
	OldNum  int    // line number in old file (0 if added)
	NewNum  int    // line number in new file (0 if removed)
}

// DiffHunk is a group of contiguous changes.
type DiffHunk struct {
	OldStart int
	OldCount int
	NewStart int
	NewCount int
	Header   string // the @@ line
	Lines    []DiffLine
}

// ParsedDiff is the result of parsing a unified diff.
type ParsedDiff struct {
	OldFile string
	NewFile string
	Hunks   []DiffHunk
}

// SideBySideLine represents one row in a side-by-side diff view.
type SideBySideLine struct {
	LeftNum  int    // 0 = blank/padding
	LeftText string // raw content (no ANSI yet)
	LeftType DiffLineType
	RightNum  int
	RightText string
	RightType DiffLineType
}

// ParseUnifiedDiff parses raw unified diff output into structured data.
func ParseUnifiedDiff(raw string) ParsedDiff {
	lines := strings.Split(raw, "\n")
	var pd ParsedDiff
	var currentHunk *DiffHunk
	oldNum, newNum := 0, 0

	for _, line := range lines {
		// File headers
		if strings.HasPrefix(line, "--- ") {
			pd.OldFile = strings.TrimPrefix(line, "--- ")
			pd.OldFile = strings.TrimPrefix(pd.OldFile, "a/")
			continue
		}
		if strings.HasPrefix(line, "+++ ") {
			pd.NewFile = strings.TrimPrefix(line, "+++ ")
			pd.NewFile = strings.TrimPrefix(pd.NewFile, "b/")
			continue
		}

		// Hunk header: @@ -old,count +new,count @@
		if strings.HasPrefix(line, "@@") {
			h := parseHunkHeader(line)
			pd.Hunks = append(pd.Hunks, h)
			currentHunk = &pd.Hunks[len(pd.Hunks)-1]
			oldNum = h.OldStart
			newNum = h.NewStart
			continue
		}

		// Skip diff metadata lines (diff --git, index, etc.)
		if currentHunk == nil {
			continue
		}

		// Diff content lines
		if strings.HasPrefix(line, "-") {
			currentHunk.Lines = append(currentHunk.Lines, DiffLine{
				Type:    DiffRemoved,
				Content: expandTabs(line[1:]),
				OldNum:  oldNum,
			})
			oldNum++
		} else if strings.HasPrefix(line, "+") {
			currentHunk.Lines = append(currentHunk.Lines, DiffLine{
				Type:    DiffAdded,
				Content: expandTabs(line[1:]),
				NewNum:  newNum,
			})
			newNum++
		} else if strings.HasPrefix(line, " ") {
			currentHunk.Lines = append(currentHunk.Lines, DiffLine{
				Type:    DiffContext,
				Content: expandTabs(line[1:]),
				OldNum:  oldNum,
				NewNum:  newNum,
			})
			oldNum++
			newNum++
		} else if line == `\ No newline at end of file` {
			// Skip this marker
			continue
		} else if line == "" {
			// Could be a context line that's just empty
			if currentHunk != nil && oldNum > 0 {
				currentHunk.Lines = append(currentHunk.Lines, DiffLine{
					Type:    DiffContext,
					Content: "",
					OldNum:  oldNum,
					NewNum:  newNum,
				})
				oldNum++
				newNum++
			}
		}
	}

	return pd
}

// parseHunkHeader parses "@@ -old,count +new,count @@ optional context"
func parseHunkHeader(line string) DiffHunk {
	h := DiffHunk{Header: line, OldStart: 1, OldCount: 1, NewStart: 1, NewCount: 1}
	// Find the range spec between @@ markers
	if !strings.HasPrefix(line, "@@") {
		return h
	}
	end := strings.Index(line[2:], "@@")
	if end < 0 {
		return h
	}
	spec := strings.TrimSpace(line[2 : 2+end])
	parts := strings.Fields(spec)
	for _, p := range parts {
		if strings.HasPrefix(p, "-") {
			h.OldStart, h.OldCount = parseRange(p[1:])
		} else if strings.HasPrefix(p, "+") {
			h.NewStart, h.NewCount = parseRange(p[1:])
		}
	}
	return h
}

// parseRange parses "start,count" or just "start" (count defaults to 1).
func parseRange(s string) (int, int) {
	if idx := strings.Index(s, ","); idx >= 0 {
		start, _ := strconv.Atoi(s[:idx])
		count, _ := strconv.Atoi(s[idx+1:])
		if start == 0 {
			start = 1
		}
		return start, count
	}
	start, _ := strconv.Atoi(s)
	if start == 0 {
		start = 1
	}
	return start, 1
}

// BuildSideBySide converts parsed diff hunks into side-by-side line pairs.
// Consecutive removed+added blocks are paired together.
func BuildSideBySide(pd ParsedDiff) []SideBySideLine {
	var result []SideBySideLine

	for hi, hunk := range pd.Hunks {
		// Add a separator between hunks (except first)
		if hi > 0 {
			result = append(result, SideBySideLine{
				LeftText: "───", RightText: "───",
			})
		}
		// Add hunk header as a separator
		result = append(result, SideBySideLine{
			LeftText:  hunk.Header,
			RightText: hunk.Header,
			LeftType:  DiffContext,
			RightType: DiffContext,
		})

		lines := hunk.Lines
		i := 0
		for i < len(lines) {
			dl := lines[i]
			switch dl.Type {
			case DiffContext:
				result = append(result, SideBySideLine{
					LeftNum: dl.OldNum, LeftText: dl.Content, LeftType: DiffContext,
					RightNum: dl.NewNum, RightText: dl.Content, RightType: DiffContext,
				})
				i++
			case DiffRemoved:
				// Collect consecutive removed lines
				removed := collectRun(lines, i, DiffRemoved)
				j := i + len(removed)
				// Collect consecutive added lines that follow
				added := collectRun(lines, j, DiffAdded)

				// Pair them up
				maxLen := max(len(removed), len(added))
				for k := 0; k < maxLen; k++ {
					var row SideBySideLine
					if k < len(removed) {
						row.LeftNum = removed[k].OldNum
						row.LeftText = removed[k].Content
						row.LeftType = DiffRemoved
					}
					if k < len(added) {
						row.RightNum = added[k].NewNum
						row.RightText = added[k].Content
						row.RightType = DiffAdded
					}
					result = append(result, row)
				}
				i = j + len(added)
			case DiffAdded:
				// Added lines without preceding removed lines
				result = append(result, SideBySideLine{
					RightNum: dl.NewNum, RightText: dl.Content, RightType: DiffAdded,
				})
				i++
			}
		}
	}

	return result
}

// collectRun collects consecutive lines of the given type starting at index i.
func collectRun(lines []DiffLine, i int, t DiffLineType) []DiffLine {
	var run []DiffLine
	for i < len(lines) && lines[i].Type == t {
		run = append(run, lines[i])
		i++
	}
	return run
}

// expandTabs replaces tab characters with spaces. Uses 2-space tab width.
func expandTabs(s string) string {
	return strings.ReplaceAll(s, "\t", "  ")
}

// FormatLineNum formats a line number for display, or blank if 0.
func FormatLineNum(n, width int) string {
	if n == 0 {
		return strings.Repeat(" ", width)
	}
	s := strconv.Itoa(n)
	if len(s) > width {
		return s[len(s)-width:]
	}
	return fmt.Sprintf("%*d", width, n)
}
