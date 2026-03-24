// Package sanitize provides ANSI stripping and PTY noise filtering for
// rendering raw terminal output as clean text (web UI, fork context, etc.).
package sanitize

import (
	"regexp"
	"strings"
)

// ansiRe matches ANSI escape sequences (CSI, CSI with DEC private mode, OSC, charset).
var ansiRe = regexp.MustCompile(
	`\x1b\[\??[0-9;:]*[a-zA-Z]` + // CSI sequences (including DEC private ?-prefixed)
		`|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)` + // OSC sequences
		`|\x1b[()][A-Z0-9]` + // Charset designation
		`|\x1b[>=]` + // Keypad mode
		`|\x1b#[0-9]`, // DEC line attributes
)

// Patterns for noise filtering.
var (
	spinnerRe        = regexp.MustCompile(`^[✳✶✻✽✢·\s]+$`)
	thinkingRe       = regexp.MustCompile(`^[✳✶✻✽✢·\s]*(ping…)?\(thinking\)\s*$`)
	warpClaudRe      = regexp.MustCompile(`^[✳✶✻✽✢·\s]*(Warping|Clauding)….*$`)
	statusBarRe      = regexp.MustCompile(`^⏵`)
	separatorRe      = regexp.MustCompile(`^─+\s*$`)
	promptRe         = regexp.MustCompile(`^❯\s*$`)
	// partialRenderRe: frame-by-frame "Warping…"/"Clauding…" renders appear as 1-4 char lines.
	// Over-broad (matches "Go", "OK") but acceptable — real content is always longer.
	partialRenderRe = regexp.MustCompile(`^[✳✶✻✽✢·]?[A-Za-z…]{0,4}(\(thinking\))?\s*$`)
	timingRe         = regexp.MustCompile(`^[✳✶✻✽✢·]?…?\s*\(\d+s.*\)\s*$`)
	cwdResetRe       = regexp.MustCompile(`^⎿\s+Shell cwd was reset`)
	runningRe        = regexp.MustCompile(`^\s*⎿\s+Running…\s*$`)
	noOutputRe = regexp.MustCompile(`\(No output\)`) // intentionally unanchored — matches inline too
	bakedRe          = regexp.MustCompile(`Baked for \d+s`)
	expandHintRe     = regexp.MustCompile(`…\s*\+\d+ lines \(ctrl\+o to expand\)`)
	loneDigitRe      = regexp.MustCompile(`^\d\s*$`)
	emptyAssistantRe = regexp.MustCompile(`^⏺\s*$`)
	keybindHintRe    = regexp.MustCompile(`^\(ctrl\+[a-z].*\)\s*$`)

	// Inline noise patterns for long concatenated terminal lines.
	inlineRunningRe   = regexp.MustCompile(`⎿\s+Running…\s*`)
	inlineCwdResetRe  = regexp.MustCompile(`⎿\s+Shell cwd was reset[^⏺⎿]*`)
	inlineWarpClaudRe = regexp.MustCompile(`[✳✶✻✽✢·]\s*(Warping|Clauding)…[^⏺⎿]*`)
	inlineSeparatorRe = regexp.MustCompile(`─{5,}[^⏺⎿]*`)
	inlinePromptRe    = regexp.MustCompile(`❯[^⏺⎿]*`)
	inlineStatusBarRe = regexp.MustCompile(`⏵[^⏺⎿]*`)
	inlineNoOutputRe  = regexp.MustCompile(`⏺\(No output\)[^⏺⎿]*`)
	inlineBakedRe     = regexp.MustCompile(`[✳✶✻✽✢·]?Baked for \d+s[^⏺⎿]*`)
	inlineExpandRe    = regexp.MustCompile(`…\s*\+\d+ lines \(ctrl\+o to expand\)[^⏺⎿]*`)
)

// StripANSI removes ANSI escape sequences from text.
func StripANSI(s string) string {
	return ansiRe.ReplaceAllString(s, "")
}

// CleanPTYOutput strips ANSI sequences and removes terminal rendering noise,
// returning clean human-readable text. Suitable for web display, fork context, etc.
func CleanPTYOutput(s string) string {
	if s == "" {
		return ""
	}

	s = StripANSI(s)

	// Normalize line endings.
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	// Replace non-breaking spaces with regular spaces.
	s = strings.ReplaceAll(s, "\u00a0", " ")

	lines := strings.Split(s, "\n")
	var out []string
	prevBlank := false

	for _, line := range lines {
		if len(line) > 120 {
			line = cleanLongLine(line)
		}
		trimmed := strings.TrimRight(line, " \t")

		if isNoiseLine(trimmed) {
			continue
		}

		if trimmed == "" {
			if prevBlank {
				continue
			}
			prevBlank = true
			out = append(out, "")
			continue
		}

		prevBlank = false
		out = append(out, line)
	}

	// Trim trailing blank lines.
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}

	if len(out) == 0 {
		return ""
	}
	return strings.Join(out, "\n") + "\n"
}

// isNoiseLine returns true if the line is terminal rendering noise.
func isNoiseLine(line string) bool {
	if line == "" {
		return false
	}
	return spinnerRe.MatchString(line) ||
		thinkingRe.MatchString(line) ||
		warpClaudRe.MatchString(line) ||
		statusBarRe.MatchString(line) ||
		separatorRe.MatchString(line) ||
		promptRe.MatchString(line) ||
		partialRenderRe.MatchString(line) ||
		timingRe.MatchString(line) ||
		cwdResetRe.MatchString(line) ||
		runningRe.MatchString(line) ||
		noOutputRe.MatchString(line) ||
		bakedRe.MatchString(line) ||
		expandHintRe.MatchString(line) ||
		loneDigitRe.MatchString(line) ||
		emptyAssistantRe.MatchString(line) ||
		keybindHintRe.MatchString(line)
}

// cleanLongLine strips inline noise from long concatenated terminal lines.
func cleanLongLine(line string) string {
	s := line
	s = inlineRunningRe.ReplaceAllString(s, "")
	s = inlineCwdResetRe.ReplaceAllString(s, "")
	s = inlineWarpClaudRe.ReplaceAllString(s, "")
	s = inlineExpandRe.ReplaceAllString(s, "")
	s = inlineNoOutputRe.ReplaceAllString(s, "")
	s = inlineBakedRe.ReplaceAllString(s, "")
	s = inlineSeparatorRe.ReplaceAllString(s, "")
	s = inlinePromptRe.ReplaceAllString(s, "")
	s = inlineStatusBarRe.ReplaceAllString(s, "")
	return strings.TrimRight(s, " \t")
}
