package gitutil

import "unicode"

// DiffSpan marks a range of runes [Start, End) as changed.
type DiffSpan struct {
	Start int
	End   int
}

// WordDiff computes the changed character spans between two strings by
// tokenizing into words, running LCS on the tokens, and mapping non-matching
// tokens back to character positions. Returns spans for the old and new
// strings respectively.
func WordDiff(old, new string) (oldSpans, newSpans []DiffSpan) {
	oldTokens := tokenize(old)
	newTokens := tokenize(new)

	oldMatch, newMatch := lcsMatch(oldTokens, newTokens)

	oldSpans = mergeUnmatched(oldTokens, oldMatch)
	newSpans = mergeUnmatched(newTokens, newMatch)
	return
}

// token is a word or non-word chunk with its rune position in the original string.
type token struct {
	text  string
	start int // rune offset
	end   int // rune offset (exclusive)
}

// tokenize splits a string into alternating word and non-word tokens.
func tokenize(s string) []token {
	runes := []rune(s)
	var tokens []token
	i := 0
	for i < len(runes) {
		start := i
		if isWordChar(runes[i]) {
			for i < len(runes) && isWordChar(runes[i]) {
				i++
			}
		} else {
			for i < len(runes) && !isWordChar(runes[i]) {
				i++
			}
		}
		tokens = append(tokens, token{
			text:  string(runes[start:i]),
			start: start,
			end:   i,
		})
	}
	return tokens
}

func isWordChar(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_'
}

// lcsMatch returns boolean masks indicating which tokens in old and new
// are part of the longest common subsequence.
func lcsMatch(old, new []token) ([]bool, []bool) {
	m, n := len(old), len(new)
	if m == 0 || n == 0 {
		return make([]bool, m), make([]bool, n)
	}

	// DP table
	dp := make([][]int, m+1)
	for i := range dp {
		dp[i] = make([]int, n+1)
	}
	for i := 1; i <= m; i++ {
		for j := 1; j <= n; j++ {
			if old[i-1].text == new[j-1].text {
				dp[i][j] = dp[i-1][j-1] + 1
			} else {
				dp[i][j] = max(dp[i-1][j], dp[i][j-1])
			}
		}
	}

	// Backtrack
	oldMatch := make([]bool, m)
	newMatch := make([]bool, n)
	i, j := m, n
	for i > 0 && j > 0 {
		if old[i-1].text == new[j-1].text {
			oldMatch[i-1] = true
			newMatch[j-1] = true
			i--
			j--
		} else if dp[i-1][j] >= dp[i][j-1] {
			i--
		} else {
			j--
		}
	}

	return oldMatch, newMatch
}

// mergeUnmatched converts unmatched tokens into character spans, merging
// consecutive unmatched tokens into single spans.
func mergeUnmatched(tokens []token, matched []bool) []DiffSpan {
	var spans []DiffSpan
	i := 0
	for i < len(tokens) {
		if matched[i] {
			i++
			continue
		}
		start := tokens[i].start
		end := tokens[i].end
		i++
		for i < len(tokens) && !matched[i] {
			end = tokens[i].end
			i++
		}
		spans = append(spans, DiffSpan{Start: start, End: end})
	}
	return spans
}
