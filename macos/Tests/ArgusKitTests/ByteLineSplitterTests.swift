import Foundation
import Testing
@testable import ArgusKit

/// ``ByteLineSplitter`` exists because Swift's `AsyncLineSequence`
/// (`bytes.lines`) silently swallows empty lines — and SSE dispatches events on
/// blank lines, so parsing an event stream through it never dispatches anything.
/// These tests pin the property that broke: empty lines are PRESERVED.
@Suite("Byte line splitter")
struct ByteLineSplitterTests {

    private func split(_ s: String) -> [String] {
        var splitter = ByteLineSplitter()
        var out: [String] = []
        for byte in Array(s.utf8) {
            if let line = splitter.feed(byte) { out.append(line) }
        }
        if let line = splitter.flush() { out.append(line) }
        return out
    }

    @Test("empty lines are preserved (the AsyncLineSequence failure mode)")
    func emptyLinesPreserved() {
        #expect(split("a\n\nb\n\n\nc\n") == ["a", "", "b", "", "", "c"])
    }

    @Test("CRLF framing strips the CR, including on empty lines")
    func crlfStripped() {
        #expect(split("data: x\r\n\r\n") == ["data: x", ""])
    }

    @Test("a trailing unterminated line is flushed at end of stream")
    func trailingLineFlushed() {
        #expect(split("data: partial") == ["data: partial"])
        #expect(split("a\nb") == ["a", "b"])
    }

    @Test("flush after a terminated stream yields nothing")
    func flushAfterCleanEndIsNil() {
        var splitter = ByteLineSplitter()
        for byte in Array("done\n".utf8) { _ = splitter.feed(byte) }
        #expect(splitter.flush() == nil)
    }

    @Test("UTF-8 multibyte content survives byte-at-a-time feeding")
    func multibyteSurvives() {
        #expect(split("✻ Ruminating…\n\n") == ["✻ Ruminating…", ""])
    }

    @Test("a full SSE terminal exchange dispatches every frame at its blank line")
    func sseEndToEnd() {
        let b64a = Data("frame-one".utf8).base64EncodedString()
        let b64b = Data("frame-two".utf8).base64EncodedString()
        let raw = "data: \(b64a)\n\n: ping\n\nevent: exit\ndata: {\"rerendering\":false}\n\ndata: \(b64b)\n\n"

        var splitter = ByteLineSplitter()
        var parser = SSEParser()
        var events: [SSEvent] = []
        for byte in Array(raw.utf8) {
            if let line = splitter.feed(byte), let ev = parser.feed(line) {
                events.append(ev)
            }
        }
        #expect(events == [
            SSEvent(name: nil, data: b64a),
            SSEvent(name: "exit", data: "{\"rerendering\":false}"),
            SSEvent(name: nil, data: b64b),
        ])
    }
}
