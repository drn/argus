import Foundation

/// A single keyboard chord: a base key plus the set of modifiers held while
/// it was pressed. Used by ``TerminalChords`` (the Terminal tab's fixed
/// intercepted-chord allowlist, design.md D2) and by
/// `ChromeShortcutCollisionTests`'s hardcoded chrome-level chord list
/// (tasks.md 5.4). The App target's `NSEvent`-to-`KeyChord` conversion
/// (`TerminalTab.swift`, which can't live here since it touches `NSEvent`)
/// constructs these from a live key press.
public struct KeyChord: Hashable, Sendable {
    /// The non-modifier key. Only the cases this app's shortcut surface
    /// actually needs are modeled — this is not a general keycode enum.
    /// `.delete` exists solely so `ChromeShortcutCollisionTests` can
    /// represent the real "Destroy" chord (`Cmd+Delete`) abstractly; it is
    /// never produced by the live decode path (that chord is not part of
    /// the Terminal tab's allowlist).
    public enum Key: Hashable, Sendable {
        case up, down, left, right, pageUp, pageDown, end, delete
        case character(Character)
    }

    /// A held modifier key. Deliberately excludes Caps Lock/Function/etc. —
    /// nothing in this app's shortcut surface keys off them.
    public enum Modifier: Hashable, Sendable {
        case command, shift, option, control
    }

    public let key: Key
    public let modifiers: Set<Modifier>

    public init(_ key: Key, _ modifiers: Set<Modifier> = []) {
        self.key = key
        self.modifiers = modifiers
    }
}

/// The Terminal tab's fixed, exhaustive intercepted-chord allowlist
/// (design.md D2 / D2's Risks mitigation: "define the allowlist as a
/// single Swift constant"). `TerminalTab.swift`'s local `NSEvent.keyDown`
/// monitor swallows exactly these 10 chords before SwiftTerm's own key
/// handling ever runs; every other keystroke — including every
/// chrome-level `.keyboardShortcut` declared elsewhere in the app — must
/// fall through untouched. `ChromeShortcutCollisionTests` (tasks.md 5.4)
/// pins the disjointness half of that guarantee; this file is the
/// allowlist itself, pinned by `TerminalChordsTests` (Stage 1).
///
/// Deliberately NOT included here: the Cmd+Shift+U "Open PR" defensive
/// fallback the Terminal tab's key monitor also swallows (per an earlier
/// stage's review finding that SwiftTerm might swallow it before a SwiftUI
/// Command sees it). That is a chrome-level shortcut with its own
/// `.keyboardShortcut` declaration (`TaskActions.swift`) needing a
/// terminal-focused fallback path, not a new terminal-native chord — see
/// `TerminalTab.swift`'s second, separate allowlist for it.
public enum TerminalChords {
    public static let intercepted: Set<KeyChord> = [
        KeyChord(.up, [.command]),                      // previous task
        KeyChord(.down, [.command]),                    // next task
        KeyChord(.left, [.command]),                    // previous detail tab
        KeyChord(.right, [.command]),                   // next detail tab
        KeyChord(.up, [.shift]),                         // scroll up one line
        KeyChord(.down, [.shift]),                       // scroll down one line
        KeyChord(.pageUp, [.shift]),                     // scroll up one page
        KeyChord(.pageDown, [.shift]),                   // scroll down one page
        KeyChord(.end, [.shift]),                        // scroll to bottom (live output)
        KeyChord(.character("c"), [.command, .shift]),   // copy visible output
    ]

    public static func isIntercepted(_ chord: KeyChord) -> Bool {
        intercepted.contains(chord)
    }
}
