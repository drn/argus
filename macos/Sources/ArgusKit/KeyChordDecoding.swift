import Foundation

/// Pure decode of a physical key press into a ``KeyChord/Key`` — everything
/// this needs (a virtual keycode + the ignoring-modifiers character) is a
/// primitive, so this stays plain Foundation and is testable from
/// `ArgusKitTests` without linking AppKit. The App target's own
/// `KeyChord.init?(_:NSEvent)` (`TerminalTab.swift`) is the small,
/// untestable-but-trivial glue that extracts those primitives from a real
/// `NSEvent` and layers on the modifier-flag mapping (which DOES need
/// `NSEvent.ModifierFlags` and so can't live here) — this file carries the
/// more error-prone half, the keycode table, where a mistake would be a
/// silent "this chord just never fires" bug rather than a compile error.
public enum KeyChordDecoding {
    /// macOS virtual keycodes (`Carbon.HIToolbox`'s `kVK_*` constants,
    /// hardcoded as plain `UInt16`s here so this file never has to
    /// `import Carbon`/`AppKit`) for the non-character keys
    /// ``KeyChord/Key`` models.
    private static let virtualKeyCodes: [UInt16: KeyChord.Key] = [
        0x7E: .up,
        0x7D: .down,
        0x7B: .left,
        0x7C: .right,
        0x74: .pageUp,
        0x79: .pageDown,
        0x77: .end,
    ]

    /// Resolves a key press's ``KeyChord/Key``. `keyCode` is checked first
    /// (arrows/page-up/page-down/end are virtual-keycode-addressed on
    /// macOS, not character-addressed); anything else falls back to the
    /// first lowercased character `charactersIgnoringModifiers` produced
    /// (`nil` or empty yields `nil` — e.g. a bare modifier-flags-changed
    /// event, or a dead key with no committed character).
    public static func key(forKeyCode keyCode: UInt16, charactersIgnoringModifiers: String?) -> KeyChord.Key? {
        if let mapped = virtualKeyCodes[keyCode] { return mapped }
        guard let ch = charactersIgnoringModifiers?.lowercased().first else { return nil }
        return .character(ch)
    }
}
