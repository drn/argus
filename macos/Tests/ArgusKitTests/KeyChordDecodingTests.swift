import Testing
@testable import ArgusKit

@Suite("KeyChordDecoding")
struct KeyChordDecodingTests {

    // MARK: - Virtual-keycode-addressed keys (checked before any character)

    @Test("Up-arrow virtual keycode (0x7E) decodes to .up")
    func upArrow() {
        #expect(KeyChordDecoding.key(forKeyCode: 0x7E, charactersIgnoringModifiers: nil) == .up)
    }

    @Test("Down-arrow virtual keycode (0x7D) decodes to .down")
    func downArrow() {
        #expect(KeyChordDecoding.key(forKeyCode: 0x7D, charactersIgnoringModifiers: nil) == .down)
    }

    @Test("Left-arrow virtual keycode (0x7B) decodes to .left")
    func leftArrow() {
        #expect(KeyChordDecoding.key(forKeyCode: 0x7B, charactersIgnoringModifiers: nil) == .left)
    }

    @Test("Right-arrow virtual keycode (0x7C) decodes to .right")
    func rightArrow() {
        #expect(KeyChordDecoding.key(forKeyCode: 0x7C, charactersIgnoringModifiers: nil) == .right)
    }

    @Test("Page Up virtual keycode (0x74) decodes to .pageUp")
    func pageUp() {
        #expect(KeyChordDecoding.key(forKeyCode: 0x74, charactersIgnoringModifiers: nil) == .pageUp)
    }

    @Test("Page Down virtual keycode (0x79) decodes to .pageDown")
    func pageDown() {
        #expect(KeyChordDecoding.key(forKeyCode: 0x79, charactersIgnoringModifiers: nil) == .pageDown)
    }

    @Test("End virtual keycode (0x77) decodes to .end")
    func end() {
        #expect(KeyChordDecoding.key(forKeyCode: 0x77, charactersIgnoringModifiers: nil) == .end)
    }

    @Test("a keycode mapping takes priority over a non-empty character")
    func keycodeWinsOverCharacter() {
        // A real Up-arrow keyDown's charactersIgnoringModifiers is the
        // NSUpArrowFunctionKey private-use codepoint, never "u" — but even
        // a contrived mismatch must still resolve via the keycode table.
        #expect(KeyChordDecoding.key(forKeyCode: 0x7E, charactersIgnoringModifiers: "u") == .up)
    }

    // MARK: - Character fallback for unmapped keycodes

    @Test("an unmapped keycode falls back to the lowercased character")
    func unmappedFallsBackToLowercasedCharacter() {
        #expect(KeyChordDecoding.key(forKeyCode: 8, charactersIgnoringModifiers: "C") == .character("c"))
    }

    @Test("an unmapped keycode with an already-lowercase character round-trips")
    func unmappedLowercaseCharacter() {
        #expect(KeyChordDecoding.key(forKeyCode: 1, charactersIgnoringModifiers: "s") == .character("s"))
    }

    @Test("nil characters with an unmapped keycode yields nil")
    func nilCharactersYieldsNil() {
        #expect(KeyChordDecoding.key(forKeyCode: 63, charactersIgnoringModifiers: nil) == nil)
    }

    @Test("empty characters with an unmapped keycode yields nil")
    func emptyCharactersYieldsNil() {
        #expect(KeyChordDecoding.key(forKeyCode: 63, charactersIgnoringModifiers: "") == nil)
    }

    @Test("only the first character of a multi-character string is used")
    func multiCharacterUsesFirst() {
        #expect(KeyChordDecoding.key(forKeyCode: 99, charactersIgnoringModifiers: "ab") == .character("a"))
    }
}
