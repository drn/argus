import Foundation
import Testing
@testable import ArgusKit

/// Stage 1 (TDD red phase) of `add-mac-keybinding-parity`: pins the fixed
/// intercepted-chord allowlist design.md D2 requires — the "single Swift
/// constant" its Risks section calls for, so the Terminal tab's local
/// `NSEvent` monitor (wired in a later stage) and a later chrome-shortcut
/// collision test (tasks.md 5.4, also a later stage) can never drift apart.
/// None of `KeyChord`, `TerminalChords.intercepted`, or
/// `TerminalChords.isIntercepted(_:)` exist yet; this file is expected to
/// fail to compile until a later stage adds
/// `macos/Sources/ArgusKit/TerminalChords.swift`.
///
/// This is deliberately the ArgusKit-side half only: it proves *which
/// chords the allowlist claims*, not that the live NSEvent monitor
/// correctly intercepts them ahead of SwiftTerm, or that unclaimed
/// keystrokes still reach `POST /input` — both require a real
/// SwiftUI/AppKit surface neither ArgusKit nor `ArgusKitTests` can host
/// (see `context/knowledge/gotchas/macos-app.md`), and are Stage 5's smoke
/// test (tasks.md 5.5), not this stage's job.
@Suite("TerminalChords intercepted allowlist")
struct TerminalChordsTests {

    // MARK: - The fixed allowlist size (design.md D2's exact 10 chords)

    @Test("the allowlist is exactly the 10 chords design.md D2 lists — no more, no fewer")
    func allowlistHasExactlyTenChords() {
        #expect(TerminalChords.intercepted.count == 10)
    }

    // MARK: - Every one of the 10 listed chords IS intercepted

    @Test("Cmd+Up is intercepted (previous task)")
    func cmdUpIntercepted() {
        #expect(TerminalChords.isIntercepted(KeyChord(.up, [.command])))
    }

    @Test("Cmd+Down is intercepted (next task)")
    func cmdDownIntercepted() {
        #expect(TerminalChords.isIntercepted(KeyChord(.down, [.command])))
    }

    @Test("Cmd+Left is intercepted (pane focus)")
    func cmdLeftIntercepted() {
        #expect(TerminalChords.isIntercepted(KeyChord(.left, [.command])))
    }

    @Test("Cmd+Right is intercepted (pane focus)")
    func cmdRightIntercepted() {
        #expect(TerminalChords.isIntercepted(KeyChord(.right, [.command])))
    }

    @Test("Shift+Up is intercepted (scrollback)")
    func shiftUpIntercepted() {
        #expect(TerminalChords.isIntercepted(KeyChord(.up, [.shift])))
    }

    @Test("Shift+Down is intercepted (scrollback)")
    func shiftDownIntercepted() {
        #expect(TerminalChords.isIntercepted(KeyChord(.down, [.shift])))
    }

    @Test("Shift+PageUp is intercepted (scrollback)")
    func shiftPageUpIntercepted() {
        #expect(TerminalChords.isIntercepted(KeyChord(.pageUp, [.shift])))
    }

    @Test("Shift+PageDown is intercepted (scrollback)")
    func shiftPageDownIntercepted() {
        #expect(TerminalChords.isIntercepted(KeyChord(.pageDown, [.shift])))
    }

    @Test("Shift+End is intercepted (scrollback)")
    func shiftEndIntercepted() {
        #expect(TerminalChords.isIntercepted(KeyChord(.end, [.shift])))
    }

    @Test("Cmd+Shift+C is intercepted (copy visible output)")
    func cmdShiftCIntercepted() {
        #expect(TerminalChords.isIntercepted(KeyChord(.character("c"), [.command, .shift])))
    }

    // MARK: - Non-regression: everything NOT on the allowlist falls through

    @Test("a plain (unmodified) Up arrow is not intercepted")
    func plainUpNotIntercepted() {
        #expect(!TerminalChords.isIntercepted(KeyChord(.up, [])))
    }

    @Test("a plain (unmodified) Down arrow is not intercepted")
    func plainDownNotIntercepted() {
        #expect(!TerminalChords.isIntercepted(KeyChord(.down, [])))
    }

    @Test("a plain (unmodified) Left arrow is not intercepted")
    func plainLeftNotIntercepted() {
        #expect(!TerminalChords.isIntercepted(KeyChord(.left, [])))
    }

    @Test("a plain (unmodified) Right arrow is not intercepted")
    func plainRightNotIntercepted() {
        #expect(!TerminalChords.isIntercepted(KeyChord(.right, [])))
    }

    @Test("Ctrl+Up is not intercepted — only the listed Cmd/Shift chords are")
    func ctrlUpNotIntercepted() {
        #expect(!TerminalChords.isIntercepted(KeyChord(.up, [.control])))
    }

    @Test("Ctrl+C is not intercepted — Ctrl chords are never claimed by this allowlist")
    func ctrlCNotIntercepted() {
        #expect(!TerminalChords.isIntercepted(KeyChord(.character("c"), [.control])))
    }

    @Test("Cmd+A is not intercepted — arbitrary letter chords are not swept in")
    func cmdANotIntercepted() {
        #expect(!TerminalChords.isIntercepted(KeyChord(.character("a"), [.command])))
    }

    @Test("Cmd+N is not intercepted — arbitrary letter chords are not swept in")
    func cmdNNotIntercepted() {
        #expect(!TerminalChords.isIntercepted(KeyChord(.character("n"), [.command])))
    }

    @Test("plain 'c' with no modifiers is not intercepted — only Cmd+Shift+C is")
    func plainCNotIntercepted() {
        #expect(!TerminalChords.isIntercepted(KeyChord(.character("c"), [])))
    }

    @Test("Cmd+C alone (no Shift) is not intercepted — the copy chord requires both modifiers")
    func cmdCWithoutShiftNotIntercepted() {
        #expect(!TerminalChords.isIntercepted(KeyChord(.character("c"), [.command])))
    }
}
