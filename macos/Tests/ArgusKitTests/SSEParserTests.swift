import Foundation
import Testing
@testable import ArgusKit

@Suite("SSE parser")
struct SSEParserTests {

    /// Feeds every line and returns all dispatched events (plus a final flush).
    private func run(_ lines: [String]) -> [SSEvent] {
        var parser = SSEParser()
        var out: [SSEvent] = []
        for line in lines {
            if let ev = parser.feed(line) { out.append(ev) }
        }
        if let ev = parser.finish() { out.append(ev) }
        return out
    }

    @Test("single unnamed data event dispatches on blank line")
    func singleData() {
        #expect(run(["data: aGVsbG8=", ""]) == [SSEvent(name: nil, data: "aGVsbG8=")])
    }

    @Test("named event carries its event name")
    func namedEvent() {
        #expect(run(["event: exit", "data: {\"rerendering\":false}", ""])
                == [SSEvent(name: "exit", data: "{\"rerendering\":false}")])
    }

    @Test("multi-line data joins with newline")
    func multiLineData() {
        #expect(run(["data: line one", "data: line two", "data: line three", ""])
                == [SSEvent(name: nil, data: "line one\nline two\nline three")])
    }

    @Test("comment lines (keepalive pings) dispatch nothing")
    func commentIgnored() {
        #expect(run([": ping", ""]).isEmpty)
    }

    @Test("comment interleaved with data does not corrupt the payload")
    func commentInterleaved() {
        #expect(run([": keepalive", "data: payload", ""])
                == [SSEvent(name: nil, data: "payload")])
    }

    @Test("blank line dispatches; multiple events parse in sequence")
    func multipleEvents() {
        let events = run([
            "data: first", "",
            "event: exit", "data: {}", "",
            "data: third", "",
        ])
        #expect(events == [
            SSEvent(name: nil, data: "first"),
            SSEvent(name: "exit", data: "{}"),
            SSEvent(name: nil, data: "third"),
        ])
    }

    @Test("value with no leading space after colon is preserved")
    func noLeadingSpace() {
        #expect(run(["data:noSpace", ""]) == [SSEvent(name: nil, data: "noSpace")])
    }

    @Test("only a single leading space is stripped")
    func singleSpaceStripped() {
        #expect(run(["data:  twoSpaces", ""]) == [SSEvent(name: nil, data: " twoSpaces")])
    }

    @Test("CRLF framing is tolerated")
    func crlfTolerated() {
        #expect(run(["data: value\r", "\r"]) == [SSEvent(name: nil, data: "value")])
    }

    @Test("event name without data dispatches nothing (spec)")
    func nameWithoutData() {
        #expect(run(["event: exit", ""]).isEmpty)
    }

    // MARK: - Terminal frame decoding

    @Test("unnamed frame base64-decodes to PTY bytes")
    func terminalOutputDecode() {
        let payload = "hello world".data(using: .utf8)!
        let te = ArgusClient.mapTerminalEvent(SSEvent(name: nil, data: payload.base64EncodedString()))
        #expect(te == .output(payload))
    }

    @Test("exit frame parses rerendering flag")
    func terminalExitDecode() {
        #expect(ArgusClient.mapTerminalEvent(SSEvent(name: "exit", data: "{\"rerendering\":true}"))
                == .exit(rerendering: true))
        #expect(ArgusClient.mapTerminalEvent(SSEvent(name: "exit", data: "{}"))
                == .exit(rerendering: false))
    }

    @Test("clipboard frame parses text and cleared payloads")
    func terminalClipboardDecode() {
        #expect(ArgusClient.mapTerminalEvent(SSEvent(name: "clipboard", data: "{\"text\":\"hi\"}"))
                == .clipboard(text: "hi", cleared: false))
        #expect(ArgusClient.mapTerminalEvent(SSEvent(name: "clipboard", data: "{\"cleared\":true}"))
                == .clipboard(text: nil, cleared: true))
    }

    @Test("invalid base64 output frame is skipped")
    func terminalBadBase64() {
        #expect(ArgusClient.mapTerminalEvent(SSEvent(name: nil, data: "!!!not base64!!!")) == nil)
    }
}
