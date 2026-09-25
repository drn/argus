package tui

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/tui/theme"
	"github.com/drn/argus/internal/tui/widget"
)

const artifactPreviewLimit = 256 * 1024

// ArtifactBrowser shows one task's registered manifest and a bounded text preview.
// All state mutation occurs on the tview goroutine.
type ArtifactBrowser struct {
	*tview.Box
	title       string
	entries     []*model.Artifact
	cursor      int
	scroll      int
	preview     []string
	previewName string
	loading     bool
	errMsg      string
	info        string
	OnRefresh   func()
	OnPreview   func(*model.Artifact)
	OnOpen      func(*model.Artifact)
	OnClose     func()
}

func NewArtifactBrowser(title string) *ArtifactBrowser {
	return &ArtifactBrowser{Box: tview.NewBox(), title: safeArtifactText(title), loading: true}
}

func safeArtifactText(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return '�'
		}
		return r
	}, s)
}

func (b *ArtifactBrowser) SetLoading() { b.loading = true; b.errMsg = "" }
func (b *ArtifactBrowser) SetEntries(entries []*model.Artifact) {
	b.entries = entries
	b.loading = false
	b.errMsg = ""
	if b.cursor >= len(entries) {
		b.cursor = max(len(entries)-1, 0)
	}
}
func (b *ArtifactBrowser) SetError(err error) {
	b.loading = false
	b.errMsg = safeArtifactText(err.Error())
}
func (b *ArtifactBrowser) SetInfo(s string) { b.info = safeArtifactText(s) }
func (b *ArtifactBrowser) SetPreview(name string, data []byte, truncated bool) {
	var safe strings.Builder
	for _, r := range string(data) {
		switch {
		case r == '\n':
			safe.WriteRune('\n')
		case r == '\t':
			safe.WriteString("    ")
		case unicode.IsControl(r):
			safe.WriteRune('�')
		default:
			safe.WriteRune(r)
		}
	}
	b.preview = strings.Split(safe.String(), "\n")
	if truncated {
		b.preview = append(b.preview, "[Preview truncated at 256 KiB]")
	}
	b.previewName = safeArtifactText(name)
	b.scroll = 0
	b.loading = false
	b.errMsg = ""
}

func (b *ArtifactBrowser) selected() *model.Artifact {
	if b.cursor < 0 || b.cursor >= len(b.entries) {
		return nil
	}
	return b.entries[b.cursor]
}

func (b *ArtifactBrowser) PasteHandler() func(string, func(tview.Primitive)) {
	return b.WrapPasteHandler(func(string, func(tview.Primitive)) {})
}

func (b *ArtifactBrowser) InputHandler() func(*tcell.EventKey, func(tview.Primitive)) {
	return b.WrapInputHandler(func(ev *tcell.EventKey, _ func(tview.Primitive)) {
		switch ev.Key() {
		case tcell.KeyEscape, tcell.KeyCtrlQ:
			if b.preview != nil {
				b.preview = nil
				b.previewName = ""
				b.scroll = 0
			} else if b.OnClose != nil {
				b.OnClose()
			}
		case tcell.KeyUp:
			if b.preview != nil {
				b.scroll = max(0, b.scroll-1)
			} else {
				b.cursor = max(0, b.cursor-1)
			}
		case tcell.KeyDown:
			if b.preview != nil {
				b.scroll++
			} else {
				b.cursor = min(max(len(b.entries)-1, 0), b.cursor+1)
			}
		case tcell.KeyPgUp:
			b.scroll = max(0, b.scroll-10)
		case tcell.KeyPgDn:
			b.scroll += 10
		case tcell.KeyEnter:
			if b.preview == nil {
				b.openSelected(false)
			}
		case tcell.KeyRune:
			switch ev.Rune() {
			case 'j':
				if b.preview != nil {
					b.scroll++
				} else {
					b.cursor = min(max(len(b.entries)-1, 0), b.cursor+1)
				}
			case 'k':
				if b.preview != nil {
					b.scroll = max(0, b.scroll-1)
				} else {
					b.cursor = max(0, b.cursor-1)
				}
			case 'r':
				b.preview = nil
				b.previewName = ""
				b.scroll = 0
				if b.OnRefresh != nil {
					b.OnRefresh()
				}
			case 'o':
				if b.preview == nil {
					b.openSelected(true)
				}
			}
		}
	})
}

func (b *ArtifactBrowser) openSelected(external bool) {
	a := b.selected()
	if a == nil {
		return
	}
	if !external && (a.Type == model.ArtifactText || a.Type == model.ArtifactMarkdown) {
		if b.OnPreview != nil {
			b.OnPreview(a)
		}
		return
	}
	if b.OnOpen != nil {
		b.OnOpen(a)
	}
}

func (b *ArtifactBrowser) Draw(screen tcell.Screen) {
	b.DrawForSubclass(screen, b)
	x, y, w, h := b.GetInnerRect()
	if w <= 0 || h <= 0 {
		return
	}
	inner := widget.DrawBorderedPanel(screen, x, y, w, h, " Artifacts - "+b.title+" ", theme.StyleBorder)
	if inner.W <= 0 || inner.H <= 0 {
		return
	}
	footer := "j/k move  Enter preview/open  o open externally  r refresh  Esc back"
	widget.DrawText(screen, inner.X, inner.Y+inner.H-1, inner.W, footer, theme.StyleDimmed)
	rows := inner.H - 2
	if rows <= 0 {
		return
	}
	if b.loading {
		widget.DrawText(screen, inner.X, inner.Y, inner.W, "Loading artifacts…", theme.StyleDimmed)
		return
	}
	if b.errMsg != "" {
		widget.DrawText(screen, inner.X, inner.Y, inner.W, "Error: "+b.errMsg, theme.StyleDimmed)
	}
	startY := inner.Y
	if b.errMsg != "" {
		startY++
		rows--
	}
	if b.info != "" {
		widget.DrawText(screen, inner.X, inner.Y+inner.H-2, inner.W, b.info, theme.StyleDimmed)
	}
	if b.preview != nil {
		widget.DrawText(screen, inner.X, startY, inner.W, "Preview: "+b.previewName, theme.StyleTitle)
		maxScroll := max(len(b.preview)-(rows-1), 0)
		b.scroll = min(b.scroll, maxScroll)
		for i := 0; i < rows-1 && i+b.scroll < len(b.preview); i++ {
			widget.DrawText(screen, inner.X, startY+i+1, inner.W, b.preview[i+b.scroll], theme.StyleNormal)
		}
		return
	}
	if len(b.entries) == 0 {
		widget.DrawText(screen, inner.X, startY, inner.W, "No artifacts registered for this task. Press r to refresh.", theme.StyleDimmed)
		return
	}
	if b.cursor < b.scroll {
		b.scroll = b.cursor
	}
	if b.cursor >= b.scroll+rows {
		b.scroll = b.cursor - rows + 1
	}
	for i := 0; i < rows && b.scroll+i < len(b.entries); i++ {
		a := b.entries[b.scroll+i]
		line := fmt.Sprintf("%s  [%s]  %s  %s", safeArtifactText(a.Name), a.Type, formatArtifactSize(a.Size), a.CreatedAt.Local().Format("2006-01-02 15:04"))
		style := theme.StyleNormal
		if b.scroll+i == b.cursor {
			style = theme.StyleTitle
		}
		widget.DrawText(screen, inner.X, startY+i, inner.W, line, style)
	}
}

func formatArtifactSize(n int64) string {
	if n >= 1024*1024 {
		return fmt.Sprintf("%.1f MiB", float64(n)/(1024*1024))
	}
	if n >= 1024 {
		return fmt.Sprintf("%.1f KiB", float64(n)/1024)
	}
	return fmt.Sprintf("%d B", n)
}
