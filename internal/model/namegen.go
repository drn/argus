package model

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"regexp"
	"strings"
)

var (
	// Fallback word lists for random name generation.
	adjectives = []string{
		"ancient", "bright", "calm", "dark", "eager",
		"fading", "gentle", "hidden", "iron", "jade",
		"keen", "lone", "misty", "noble", "pale",
		"quick", "rare", "silent", "thin", "vast",
		"warm", "young", "bold", "crisp", "deep",
		"swift", "wild", "cold", "soft", "still",
	}

	nouns = []string{
		"autumn", "bloom", "cedar", "dawn", "ember",
		"flame", "grove", "heron", "iris", "jewel",
		"kite", "lake", "moon", "night", "ocean",
		"pine", "quail", "river", "stone", "thorn",
		"dusk", "vale", "willow", "cloud", "ridge",
		"frost", "crane", "spark", "drift", "storm",
		"dream", "light", "shade", "brook", "trail",
		"lark", "reef", "snow", "wind", "tide",
	}

	stopWords = map[string]bool{
		"a": true, "an": true, "the": true, "and": true, "or": true,
		"but": true, "in": true, "on": true, "at": true, "to": true,
		"for": true, "of": true, "with": true, "by": true, "from": true,
		"is": true, "are": true, "was": true, "were": true, "be": true,
		"been": true, "being": true, "have": true, "has": true, "had": true,
		"do": true, "does": true, "did": true, "will": true, "would": true,
		"could": true, "should": true, "may": true, "might": true, "must": true,
		"shall": true, "can": true, "need": true, "that": true, "which": true,
		"who": true, "whom": true, "this": true, "these": true, "those": true,
		"it": true, "its": true, "i": true, "we": true, "you": true,
		"they": true, "me": true, "him": true, "her": true, "us": true,
		"them": true, "my": true, "our": true, "your": true, "their": true,
		"not": true, "no": true, "so": true, "if": true, "then": true,
		"than": true, "when": true, "where": true, "how": true, "what": true,
		"all": true, "each": true, "every": true, "both": true, "few": true,
		"more": true, "most": true, "some": true, "any": true, "also": true,
		"just": true, "about": true, "into": true, "through": true, "during": true,
		"before": true, "after": true, "above": true, "below": true, "between": true,
		"same": true, "up": true, "out": true, "as": true, "very": true,
		"there": true, "here": true, "please": true, "make": true, "sure": true,
	}

	nonAlpha = regexp.MustCompile(`[^a-z0-9 ]+`)
)

// GenerateName creates a random fallback name like "silent-morning-frost".
func GenerateName() string {
	adj := pick(adjectives)
	n1 := pick(nouns)
	n2 := pick(nouns)
	for n2 == n1 {
		n2 = pick(nouns)
	}
	return fmt.Sprintf("%s-%s-%s", adj, n1, n2)
}

// GenerateNameFromPrompt extracts keywords from the prompt to build a
// kebab-case slug like "fix-auth-token-refresh". Falls back to a random
// name if the prompt yields no useful keywords.
func GenerateNameFromPrompt(prompt string) string {
	slug := extractKeywords(prompt, 4)
	if slug == "" {
		return GenerateName()
	}
	return slug
}

// extractKeywords pulls up to maxWords meaningful words from the prompt.
func extractKeywords(prompt string, maxWords int) string {
	// Normalize: lowercase, strip punctuation, collapse whitespace
	s := strings.ToLower(prompt)
	s = nonAlpha.ReplaceAllString(s, " ")
	words := strings.Fields(s)

	var keywords []string
	for _, w := range words {
		if stopWords[w] || len(w) < 2 {
			continue
		}
		keywords = append(keywords, w)
		if len(keywords) >= maxWords {
			break
		}
	}

	if len(keywords) == 0 {
		return ""
	}

	slug := strings.Join(keywords, "-")

	// Cap at 40 chars, break at hyphen boundary
	if len(slug) > 40 {
		slug = slug[:40]
		if i := strings.LastIndex(slug, "-"); i > 5 {
			slug = slug[:i]
		}
	}

	return slug
}

func pick(list []string) string {
	n, _ := rand.Int(rand.Reader, big.NewInt(int64(len(list))))
	return list[n.Int64()]
}
