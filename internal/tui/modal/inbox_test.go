package modal

import (
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/drn/argus/internal/testutil"
)

func inboxKey(m *InboxModal, k tcell.Key, r rune) {
	m.InputHandler()(tcell.NewEventKey(k, r, tcell.ModNone), nil)
}

func drawInbox(t *testing.T, m *InboxModal, w, h int) string {
	t.Helper()
	sim := drawAt(t, w, h)
	m.SetRect(0, 0, w, h)
	m.Draw(sim)
	sim.Sync()
	return screenString(sim)
}

func TestInboxModal_InputHandler(t *testing.T) {
	for _, tc := range []struct {
		name       string
		key        tcell.Key
		r          rune
		wantClosed bool
		wantReload bool
	}{
		{"esc closes", tcell.KeyEscape, 0, true, false},
		{"ctrl+q closes", tcell.KeyCtrlQ, 0, true, false},
		{"q closes", tcell.KeyRune, 'q', true, false},
		{"i closes", tcell.KeyRune, 'i', true, false},
		{"r reloads", tcell.KeyRune, 'r', false, true},
		{"other rune no-op", tcell.KeyRune, 'x', false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := NewInboxModal("Inbox")
			inboxKey(m, tc.key, tc.r)
			testutil.Equal(t, m.Closed(), tc.wantClosed)
			testutil.Equal(t, m.TakeReload(), tc.wantReload)
			testutil.False(t, m.TakeReload())
		})
	}
}

func TestInboxModal_ReloadDisabledWhenUnavailable(t *testing.T) {
	m := NewInboxModal("Inbox")
	m.SetUnavailable("not here")
	inboxKey(m, tcell.KeyRune, 'r')
	testutil.False(t, m.TakeReload())
}

func TestInboxModal_DrawStates(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(m *InboxModal)
		want  string
	}{
		{"loading", func(m *InboxModal) {}, "Loading…"},
		{"empty", func(m *InboxModal) { m.SetEntries(nil) }, "No messages."},
		{"error", func(m *InboxModal) { m.SetError("boom") }, "Error: boom"},
		{"unavailable", func(m *InboxModal) { m.SetUnavailable("remote mode") }, "remote mode"},
		{"reloading", func(m *InboxModal) { m.SetEntries(nil); m.SetLoading() }, "Loading…"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := NewInboxModal("Inbox - demo")
			tc.setup(m)
			body := drawInbox(t, m, 100, 30)
			testutil.Contains(t, body, "Inbox - demo")
			testutil.Contains(t, body, tc.want)
		})
	}
}

func TestInboxModal_DrawEntries(t *testing.T) {
	at := time.Date(2026, 9, 24, 10, 0, 0, 0, time.Local)
	m := NewInboxModal("Inbox")
	m.SetEntries([]InboxEntry{
		{At: at, Source: "task", From: "sender-task", Summary: "note", Unread: true, Body: "first line\nsecond line"},
		{At: at.Add(time.Minute), Source: "hera", From: "coord", To: "w1", Summary: "status ping", ReadAt: at.Add(2 * time.Minute), Delivery: "idle_submit", Body: "hera body"},
	})
	body := drawInbox(t, m, 100, 40)
	testutil.Contains(t, body, "2026-09-24 10:00:00  [task]  sender-task")
	testutil.Contains(t, body, "unread")
	testutil.Contains(t, body, "first line")
	testutil.Contains(t, body, "second line")
	testutil.Contains(t, body, "[hera]  coord → w1")
	testutil.Contains(t, body, "read 09-24 10:02:00  ·  delivery: idle_submit")
	testutil.Contains(t, body, "status ping")
	testutil.Contains(t, body, "hera body")
	testutil.Contains(t, body, "2 message(s)")
}

func TestInboxModal_OpensAtBottomAndScrolls(t *testing.T) {
	var entries []InboxEntry
	for i := range 30 {
		entries = append(entries, InboxEntry{Source: "task", From: "s", Body: strings.Repeat("x", 5) + string(rune('a'+i%26))})
	}
	m := NewInboxModal("Inbox")
	m.SetEntries(entries)
	drawInbox(t, m, 80, 20)
	bottom := m.scroll
	if bottom == 0 {
		t.Fatal("expected modal to open scrolled to the newest message")
	}

	inboxKey(m, tcell.KeyRune, 'g')
	drawInbox(t, m, 80, 20)
	testutil.Equal(t, m.scroll, 0)

	inboxKey(m, tcell.KeyRune, 'j')
	inboxKey(m, tcell.KeyDown, 0)
	testutil.Equal(t, m.scroll, 2)
	inboxKey(m, tcell.KeyRune, 'k')
	inboxKey(m, tcell.KeyUp, 0)
	testutil.Equal(t, m.scroll, 0)

	inboxKey(m, tcell.KeyPgDn, 0)
	testutil.Equal(t, m.scroll, m.pageStep)
	inboxKey(m, tcell.KeyPgUp, 0)
	testutil.Equal(t, m.scroll, 0)

	inboxKey(m, tcell.KeyEnd, 0)
	drawInbox(t, m, 80, 20)
	testutil.Equal(t, m.scroll, bottom)
	inboxKey(m, tcell.KeyHome, 0)
	drawInbox(t, m, 80, 20)
	testutil.Equal(t, m.scroll, 0)
	inboxKey(m, tcell.KeyRune, 'G')
	drawInbox(t, m, 80, 20)
	testutil.Equal(t, m.scroll, bottom)
}

func TestWrapPreservingLines(t *testing.T) {
	for _, tc := range []struct {
		name  string
		in    string
		width int
		want  []string
	}{
		{"zero width", "abc", 0, nil},
		{"keeps newlines and blanks", "a\n\nb", 10, []string{"a", "", "b"}},
		{"word wraps", "one two three", 7, []string{"one two", "three"}},
		{"hard breaks long token", "abcdefghij", 4, []string{"abcd", "efgh", "ij"}},
		{"hard break flushes current line", "hi abcdefgh", 4, []string{"hi", "abcd", "efgh"}},
		{"strips control chars and tabs", "a\x1b[31mb\tc", 20, []string{"a[31mb c"}},
		{"rune counted", "ééé ééé", 3, []string{"ééé", "ééé"}},
		{"wide runes count two columns", "漢字漢字", 4, []string{"漢字", "漢字"}},
		{"wide word wraps by width", "ab 漢字", 4, []string{"ab", "漢字"}},
		{"rune wider than line emitted alone", "漢", 1, []string{"漢"}},
		{"zero-width only word skipped", "a \u200b b", 10, []string{"a b"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			testutil.DeepEqual(t, wrapPreservingLines(tc.in, tc.width), tc.want)
		})
	}
}

func TestInboxModal_MouseScroll(t *testing.T) {
	m := NewInboxModal("Inbox")
	m.SetRect(0, 0, 80, 20)
	h := m.MouseHandler()
	ev := tcell.NewEventMouse(1, 1, tcell.WheelDown, tcell.ModNone)
	m.scroll = 0
	consumed, _ := h(tview.MouseScrollDown, ev, func(p tview.Primitive) {})
	testutil.True(t, consumed)
	testutil.Equal(t, m.scroll, 3)
	consumed, _ = h(tview.MouseScrollUp, ev, func(p tview.Primitive) {})
	testutil.True(t, consumed)
	testutil.Equal(t, m.scroll, 0)
	consumed, _ = h(tview.MouseLeftDown, ev, func(p tview.Primitive) {})
	testutil.True(t, consumed)
	consumed, _ = h(tview.MouseMove, ev, func(p tview.Primitive) {})
	testutil.False(t, consumed)
	_ = consumed
}

func TestInboxModal_Accessors(t *testing.T) {
	m := NewInboxModal("Inbox")
	_, loaded := m.Entries()
	testutil.False(t, loaded)

	m.SetEntries([]InboxEntry{{Body: "x"}})
	got, loaded := m.Entries()
	testutil.True(t, loaded)
	testutil.Equal(t, len(got), 1)

	m.SetError("boom")
	_, loaded = m.Entries()
	testutil.False(t, loaded)

	testutil.Equal(t, m.Unavailable(), "")
	m.SetUnavailable("remote")
	testutil.Equal(t, m.Unavailable(), "remote")
	_, loaded = m.Entries()
	testutil.False(t, loaded)
}

func TestInboxModal_StripsControlsFromHeaderAndTitle(t *testing.T) {
	m := NewInboxModal("Inbox - evil\x1b[2Jname")
	testutil.Equal(t, m.title, "Inbox - evil[2Jname")
	m.SetEntries([]InboxEntry{{Source: "task", From: "bad\x1b]0;pwn\x07", To: "r\nole", Delivery: "idle\x1b", Body: "ok"}})
	lines := m.lines(80)
	for _, l := range lines {
		for _, r := range l.text {
			if r < 0x20 {
				t.Fatalf("control rune %q leaked into line %q", r, l.text)
			}
		}
	}
	testutil.Contains(t, inboxHeader(m.entries[0]), "bad]0;pwn → r ole")

	m.SetError("line1\nline2\x1b")
	testutil.Equal(t, m.lines(80)[0].text, "Error: line1 line2")
	m.SetUnavailable("no\x1b")
	testutil.Equal(t, m.lines(80)[0].text, "no")
}

func TestDrawCells_WideRunesAndClipping(t *testing.T) {
	sim := drawAt(t, 10, 2)
	drawCells(sim, 0, 0, 5, "a漢b字c", tcell.StyleDefault)
	sim.Sync()
	cell := func(x, y int) string {
		str, _, _ := sim.Get(x, y)
		return str
	}
	testutil.Equal(t, cell(0, 0), "a")
	testutil.Equal(t, cell(1, 0), "漢")
	testutil.Equal(t, cell(3, 0), "b")
	// 字 would need columns 4-5 but only 5 columns are allowed: clipped.
	testutil.Equal(t, cell(4, 0), " ")

	drawCells(sim, 0, 1, 5, "x\u200by", tcell.StyleDefault)
	sim.Sync()
	testutil.Equal(t, cell(1, 1), "y")
}
