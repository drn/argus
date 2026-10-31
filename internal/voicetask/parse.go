// Package voicetask extracts a project name and task prompt from a dictated transcript.
package voicetask

import (
	"regexp"
	"strings"
	"unicode"
)

const (
	sourceSpoken  = "spoken"
	sourceDefault = "default"
	maxCandidates = 3
)

var triggerRe = regexp.MustCompile(`(?i)in\s+(?:the\s+)?project`)

type Result struct {
	Project       string
	Prompt        string
	Source        string
	SpokenProject string
}

type word struct {
	text       string
	start, end int
}

func Parse(transcript string, projects []string, defaultProject string) Result {
	trimmed := strings.TrimSpace(transcript)
	res := Result{Prompt: trimmed}

	for _, loc := range triggerRe.FindAllStringIndex(transcript, -1) {
		if !boundaryBefore(transcript, loc[0]) || !boundaryAfter(transcript, loc[1]) {
			continue
		}
		words := candidateWords(transcript, loc[1])
		if len(words) == 0 {
			continue
		}
		if name, used := resolve(words, projects); used > 0 {
			end := words[used-1].end
			res.Project = name
			res.Source = sourceSpoken
			res.Prompt = removeSpan(transcript, loc[0], end)
			return res
		}
		if res.SpokenProject == "" {
			res.SpokenProject = words[0].text
		}
	}

	if name, ok := lookup(defaultProject, projects); ok {
		res.Project = name
		res.Source = sourceDefault
	}
	return res
}

func isWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r)
}

func boundaryBefore(s string, i int) bool {
	if i == 0 {
		return true
	}
	r := []rune(s[:i])
	return !isWordRune(r[len(r)-1])
}

func boundaryAfter(s string, i int) bool {
	for _, r := range s[i:] {
		return !isWordRune(r)
	}
	return true
}

func isInnerRune(r rune) bool {
	return isWordRune(r) || r == '-' || r == '_' || r == '.'
}

func candidateWords(s string, from int) []word {
	i := from
	if rest := s[i:]; strings.HasPrefix(strings.TrimLeft(rest, " \t"), ":") {
		i += len(rest) - len(strings.TrimLeft(rest, " \t")) + 1
	}
	var words []word
	for len(words) < maxCandidates {
		for i < len(s) && (s[i] == ' ' || s[i] == '\t' || s[i] == '\n') {
			i++
		}
		start := i
		for i < len(s) {
			r := []rune(s[i:])[0]
			if !isInnerRune(r) {
				break
			}
			i += len(string(r))
		}
		raw := s[start:i]
		text := strings.TrimRight(raw, ".")
		if text == "" || !isWordRune([]rune(text)[0]) && text[0] != '-' && text[0] != '_' {
			break
		}
		words = append(words, word{text: text, start: start, end: start + len(text)})
		if len(text) != len(raw) {
			break
		}
	}
	return words
}

func normalize(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if isWordRune(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func lookup(name string, projects []string) (string, bool) {
	n := normalize(name)
	if n == "" {
		return "", false
	}
	for _, p := range projects {
		if normalize(p) == n {
			return p, true
		}
	}
	return "", false
}

func resolve(words []word, projects []string) (string, int) {
	joined := func(k int) string {
		parts := make([]string, k)
		for i := 0; i < k; i++ {
			parts[i] = words[i].text
		}
		return normalize(strings.Join(parts, ""))
	}
	for k := len(words); k >= 1; k-- {
		cand := joined(k)
		for _, p := range projects {
			if n := normalize(p); n != "" && n == cand {
				return p, k
			}
		}
	}
	for k := len(words); k >= 1; k-- {
		cand := joined(k)
		var match string
		count := 0
		for _, p := range projects {
			n := normalize(p)
			maxDist := fuzzyBudget(len([]rune(n)))
			if maxDist == 0 {
				continue
			}
			if levenshtein(cand, n) <= maxDist {
				match = p
				count++
			}
		}
		if count == 1 {
			return match, k
		}
	}
	return "", 0
}

func fuzzyBudget(n int) int {
	switch {
	case n < 4:
		return 0
	case n < 8:
		return 1
	default:
		return 2
	}
}

func levenshtein(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	cur := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(rb)]
}

func removeSpan(s string, start, end int) string {
	before := strings.TrimRight(s[:start], " \t\n")
	after := strings.TrimLeft(s[end:], " \t\n")
	if strings.HasPrefix(after, ",") && (before == "" || strings.HasSuffix(before, ",")) {
		after = strings.TrimLeft(after[1:], " \t\n")
	}
	sep := " "
	if before == "" || after == "" || strings.ContainsRune(",.;:!?", rune(after[0])) {
		sep = ""
	}
	out := strings.Join(strings.Fields(before+sep+after), " ")
	out = strings.TrimLeft(out, ",;: ")
	return strings.TrimRight(out, ",;: ")
}
