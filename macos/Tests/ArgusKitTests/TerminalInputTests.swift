import Foundation
import Testing
@testable import ArgusKit

/// ``TerminalInput/sanitize(_:)`` strips Ctrl+Z (`0x1A`, ASCII SUB) from
/// outbound keyboard input. A literal Ctrl+Z reaching the agent PTY suspends
/// the process and, on Claude Code, trips the CLI's own background-session
/// supervisor, which reparents the session out of argus's process tree
/// permanently (orphaning it). The macOS terminal input delegate calls this at
/// the single outbound chokepoint; these tests pin the byte-level contract:
/// only Ctrl+Z is removed, everything else survives in order.
@Suite("Terminal input sanitizer")
struct TerminalInputTests {

    private func sanitize(_ bytes: [UInt8]) -> [UInt8] {
        [UInt8](TerminalInput.sanitize(Data(bytes)))
    }

    @Test("a lone Ctrl+Z keypress is dropped to nothing")
    func loneCtrlZDropped() {
        #expect(sanitize([0x1A]) == [])
    }

    @Test("Ctrl+Z embedded in a payload is removed, surrounding bytes preserved in order")
    func embeddedCtrlZRemoved() {
        #expect(sanitize([0x68, 0x1A, 0x69]) == [0x68, 0x69]) // h <Ctrl+Z> i
    }

    @Test("every Ctrl+Z is removed when repeated")
    func repeatedCtrlZRemoved() {
        #expect(sanitize([0x1A, 0x1A, 0x41, 0x1A]) == [0x41]) // only "A" survives
    }

    @Test("clean input passes through byte-for-byte (identity)")
    func cleanInputUnchanged() {
        let clean: [UInt8] = [0x6C, 0x73, 0x0D] // "ls\r"
        #expect(sanitize(clean) == clean)
    }

    @Test("empty input yields empty output")
    func emptyInput() {
        #expect(sanitize([]) == [])
    }

    @Test("other control bytes are NOT stripped — only Ctrl+Z")
    func onlyCtrlZIsStripped() {
        // Ctrl+C (0x03), Ctrl+Y (0x19), ESC (0x1B), FS (0x1C) must reach the
        // PTY untouched — the guard is surgical, not a blanket control-byte
        // filter.
        let others: [UInt8] = [0x03, 0x19, 0x1B, 0x1C]
        #expect(sanitize(others) == others)
    }

    @Test("the sanitized suspend byte is Ctrl+Z (0x1A)")
    func suspendByteValue() {
        #expect(TerminalInput.suspendByte == 0x1A)
    }
}
