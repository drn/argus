package agent

import "github.com/drn/argus/internal/sanitize"

// TailView shares lazy detector inputs for one immutable tail and PTY size.
// It is scan-local and must not be reused after the tail or dimensions change.
// Render sharing is safe because ScreenRenderer resets with RIS on every call;
// InputDraft, which inspects emulator cells, uses its own renderer separately.
type TailView struct {
	renderer                            *ScreenRenderer
	buf                                 []byte
	cols, rows                          int
	window, full, screen                string
	windowReady, fullReady, screenReady bool
	renderCount                         int
}

func NewTailView(r *ScreenRenderer, buf []byte, cols, rows int) *TailView {
	return &TailView{renderer: r, buf: buf, cols: cols, rows: rows}
}

func (v *TailView) windowText() string {
	if !v.windowReady {
		tail := v.buf
		if len(tail) > needsInputTailWindow {
			tail = tail[len(tail)-needsInputTailWindow:]
		}
		v.window = sanitize.StripANSI(string(tail))
		v.windowReady = true
	}
	return v.window
}

func (v *TailView) fullText() string {
	if !v.fullReady {
		if len(v.buf) <= needsInputTailWindow {
			v.full = v.windowText()
		} else {
			v.full = sanitize.StripANSI(string(v.buf))
		}
		v.fullReady = true
	}
	return v.full
}

func (v *TailView) rendered() string {
	if !v.screenReady {
		v.screen = v.renderer.render(v.buf, v.cols, v.rows)
		v.screenReady = true
		v.renderCount++
	}
	return v.screen
}

func (v *TailView) DetectNeedsInputScreen() bool {
	if len(v.buf) == 0 {
		return false
	}
	if detectNeedsInputText(v.windowText()) {
		return true
	}
	return v.renderer != nil && detectNeedsInputText(v.rendered())
}

func (v *TailView) AwaitingInputFingerprint() (uint64, bool) {
	if len(v.buf) == 0 {
		return 0, false
	}
	if awaitingInputText(v.windowText()) {
		return fingerprintText(v.fullText()), true
	}
	if v.renderer == nil || !awaitingInputText(v.rendered()) {
		return 0, false
	}
	return fingerprintText(v.rendered()), true
}

func (v *TailView) ParkedSelectionSignal() bool {
	if len(v.buf) == 0 {
		return false
	}
	text := v.windowText()
	if detectSelectionPromptText(text) {
		return !needsInputWorkingRe.MatchString(text)
	}
	if v.renderer == nil {
		return false
	}
	text = v.rendered()
	return detectSelectionPromptText(text) && !needsInputWorkingRe.MatchString(text)
}

func (v *TailView) ContentIdleFingerprint() (uint64, bool) {
	if v.renderer == nil || len(v.buf) == 0 {
		return fingerprintText(v.fullText()), needsInputWorkingRe.MatchString(v.windowText())
	}
	text := v.rendered()
	return fingerprintText(text), needsInputWorkingRe.MatchString(text)
}
