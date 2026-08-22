import Testing
@testable import ArgusKit

/// Stage 5.4 (tasks.md): asserts the Terminal tab's fixed intercepted-chord
/// allowlist (``TerminalChords/intercepted``) never collides with a REAL
/// chrome-level `.keyboardShortcut` declared anywhere else in the app
/// (design.md D2's Risks mitigation — "a unit test asserts no chrome-level
/// `.keyboardShortcut` reuses one of these chords").
///
/// ArgusKit cannot introspect SwiftUI `.keyboardShortcut` declarations at
/// runtime (no XCUITest harness exists in this repo — see
/// `context/knowledge/gotchas/macos-app.md`), so `chromeChords` below
/// hardcodes the authoritative list as of this stage. It was produced by
/// running, from the repo root:
///
///     grep -rn '\.keyboardShortcut(' macos/Sources/Argus/*.swift
///
/// and keeping every entry EXCEPT `.cancelAction`/`.defaultAction` (Escape/
/// Enter on a sheet's Cancel/OK button — not a user-chosen chord). If a
/// future PR adds, removes, or reassigns a chrome-level shortcut without
/// updating `chromeChords` here, this test silently stops being
/// authoritative for the new shortcut — re-run the grep and update this
/// list in the SAME PR that changes chrome shortcuts.
@Suite("Terminal-chord vs. chrome-shortcut collision")
struct ChromeShortcutCollisionTests {
    /// The full set of real chrome-level chords as of Stage 5 (verified via
    /// the grep above, one entry per distinct chord — Cmd+N is declared
    /// twice, in `ContentView.swift` and `MenuBarContent.swift`, but both
    /// mean the same "New Task" action so it's one set member):
    ///  - Cmd+N — new task
    ///  - Cmd+Q — quit
    ///  - Cmd+R — rename
    ///  - Cmd+Shift+S — schedules window
    ///  - Cmd+Shift+/ — shortcuts help
    ///  - Cmd+Shift+J — jump to next needs-input
    ///  - Cmd+1 / Cmd+2 / Cmd+3 / Cmd+4 — detail tab select
    ///  - Cmd+Shift+B — fork
    ///  - Cmd+Shift+A — archive/unarchive
    ///  - Cmd+Shift+E — open repo
    ///  - Cmd+Shift+U — open PR (ALSO handled by the Terminal tab's separate
    ///    defensive fallback allowlist — see `TerminalTab.swift` — which is
    ///    why it's excluded from `TerminalChords.intercepted` itself and
    ///    must stay excluded: this test's whole point is that the two
    ///    allowlists never merge)
    ///  - Cmd+Shift+P — pin/unpin
    ///  - Cmd+Delete — delete (destroy)
    ///  - Cmd+F — focus the sidebar filter field
    static let chromeChords: Set<KeyChord> = [
        KeyChord(.character("n"), [.command]),
        KeyChord(.character("q"), [.command]),
        KeyChord(.character("r"), [.command]),
        KeyChord(.character("s"), [.command, .shift]),
        KeyChord(.character("/"), [.command, .shift]),
        KeyChord(.character("j"), [.command, .shift]),
        KeyChord(.character("1"), [.command]),
        KeyChord(.character("2"), [.command]),
        KeyChord(.character("3"), [.command]),
        KeyChord(.character("4"), [.command]),
        KeyChord(.character("b"), [.command, .shift]),
        KeyChord(.character("a"), [.command, .shift]),
        KeyChord(.character("e"), [.command, .shift]),
        KeyChord(.character("u"), [.command, .shift]),
        KeyChord(.character("p"), [.command, .shift]),
        KeyChord(.delete, [.command]),
        KeyChord(.character("f"), [.command]),
    ]

    @Test("chromeChords is exactly 17 distinct chords — a canary for the hardcoded list going stale")
    func chromeChordsCountCanary() {
        #expect(Self.chromeChords.count == 17)
    }

    @Test("no chrome-level chord collides with the terminal-safe allowlist")
    func noCollisionWithTerminalAllowlist() {
        let overlap = TerminalChords.intercepted.intersection(Self.chromeChords)
        #expect(overlap.isEmpty)
    }

    @Test("Cmd+Shift+U (open PR) is a chrome chord but is NOT in TerminalChords.intercepted")
    func openPRChordStaysOutOfTerminalAllowlist() {
        let openPR = KeyChord(.character("u"), [.command, .shift])
        #expect(Self.chromeChords.contains(openPR))
        #expect(!TerminalChords.isIntercepted(openPR))
    }
}
