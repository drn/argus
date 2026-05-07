// Package links extracts http/https URLs from raw terminal output.
//
// Used by the TUI's fuzzy link picker (ctrl+l in agent view) and by the
// web app's "Open link" overflow action. The extraction logic lives here
// so it can be shared without dragging in tview / tcell dependencies.
package links

import (
	"regexp"
	"strings"
)

// Link represents a URL extracted from content with an optional display label.
type Link struct {
	Label string `json:"label"` // markdown link text, or the URL itself for bare URLs
	URL   string `json:"url"`
}

// mdLinkRe matches markdown links: [text](url)
var mdLinkRe = regexp.MustCompile(`\[([^\]]+)\]\((https?://[^\s)]+)\)`)

// bareLinkRe matches bare URLs not already inside markdown link syntax.
// Excludes characters that are never valid in URLs per RFC 3986 (", `, {, }, <)
// and all ASCII control characters (\x00-\x1f, including \x1b ESC) to prevent
// matching through formatted/structured text containing URLs.
var bareLinkRe = regexp.MustCompile(`https?://[^\s)\]<>"\x60{}\x00-\x1f]+`)

// osc8Re matches OSC 8 hyperlink tags: \x1b]8;params;URL\x07 or \x1b]8;params;URL\x1b\\
// Captures the URL in group 1. Opening tags have a non-empty URL; closing tags are empty.
var osc8Re = regexp.MustCompile(`\x1b\]8;[^;]*;([^\x07\x1b]*)(?:\x07|\x1b\\)`)

// ansiRe matches ANSI escape sequences (CSI, OSC, simple escapes).
// Mirrors widget.AnsiRe so this package doesn't depend on the TUI layer.
var ansiRe = regexp.MustCompile(`\x1b(?:\[[\x20-\x3f]*[\x40-\x7e]|\][^\x07\x1b]*(?:\x07|\x1b\\)|[()][0-9A-B]|[78DEHM])`)

// stripANSI removes ANSI escape sequences from raw terminal output.
// OSC 8 hyperlink tags are replaced with their embedded URL (+ space separator)
// so the URL is preserved for extraction.
//
// SGR (style/color) sequences ending in 'm' are stripped to empty so that
// color codes mid-URL don't break the URL. All other ANSI sequences (cursor
// movement, erase, mode changes) are replaced with a space to prevent text
// from different screen positions from merging into false URLs.
func stripANSI(s string) string {
	// First pass: extract URLs from OSC 8 hyperlinks before general stripping.
	// Opening tags become "URL " (preserving the link target); closing tags
	// (empty URL) become just a space — harmless for subsequent URL matching.
	s = osc8Re.ReplaceAllString(s, "$1 ")
	// Second pass: conditionally replace ANSI sequences.
	return ansiRe.ReplaceAllStringFunc(s, func(seq string) string {
		// SGR sequences are CSI ending in 'm' — strip to preserve URL continuity.
		// seq[0] is always ESC (\x1b); seq[1]=='[' means CSI (vs ']' for OSC, etc.)
		if len(seq) >= 3 && seq[1] == '[' && seq[len(seq)-1] == 'm' {
			return ""
		}
		// Everything else (cursor movement, erase, etc.) → space.
		return " "
	})
}

// cleanURL strips trailing punctuation that is not part of the URL.
// Some chars (`, {, }) are also excluded by bareLinkRe but are kept here
// as a safety net for URLs extracted via mdLinkRe or osc8Re.
func cleanURL(u string) string {
	// Byte indexing is safe here — all stripped chars are single-byte ASCII.
	for len(u) > 0 {
		last := u[len(u)-1]
		switch last {
		case '.', ',', ';', ':', '\'', '"', '`', '{', '}', '*':
			u = u[:len(u)-1]
		default:
			return u
		}
	}
	return u
}

// isTruncatedURL returns true if the URL contains a truncation marker.
// Unicode ellipsis (…) can appear anywhere; ASCII "..." is only checked as a
// suffix to avoid false-positives on legitimate URLs (e.g. GitHub compare ranges).
func isTruncatedURL(raw string) bool {
	return strings.Contains(raw, "…") || strings.HasSuffix(raw, "...")
}

// Extract returns unique http/https URLs from content that may contain ANSI
// escape sequences (e.g. raw PTY session logs). Markdown-style links
// [text](url) are preferred; bare URLs not already captured by a markdown
// link are added with the URL as the label.
func Extract(content string) []Link {
	// Strip ANSI escape sequences so terminal formatting doesn't pollute URLs.
	content = stripANSI(content)

	seen := make(map[string]bool)
	var out []Link

	// First pass: markdown links
	for _, m := range mdLinkRe.FindAllStringSubmatch(content, -1) {
		raw := m[2]
		if isTruncatedURL(raw) {
			continue
		}
		url := cleanURL(raw)
		if url == "" || seen[url] {
			continue
		}
		seen[url] = true
		out = append(out, Link{Label: m[1], URL: url})
	}

	// Second pass: bare URLs not already captured
	for _, raw := range bareLinkRe.FindAllString(content, -1) {
		if isTruncatedURL(raw) {
			continue
		}
		url := cleanURL(raw)
		if url == "" || seen[url] {
			continue
		}
		seen[url] = true
		out = append(out, Link{Label: url, URL: url})
	}

	return out
}
