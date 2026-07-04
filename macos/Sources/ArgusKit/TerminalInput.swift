import Foundation

/// Sanitizes outbound terminal keyboard input before it is forwarded to the
/// argus daemon's `POST /input` endpoint.
///
/// The macOS app streams keystrokes from SwiftTerm straight to the agent PTY.
/// One control byte must never make that trip: **Ctrl+Z (`0x1A`, ASCII SUB)**.
/// Delivered to a foreground agent it raises `SIGTSTP` and suspends the
/// process — but the graver problem is Claude Code specific. The Claude Code
/// CLI runs its OWN background-session supervisor (separate from argus), and a
/// literal Ctrl+Z byte reaching it makes Claude Code reparent the session out
/// of argus's process tree *permanently and invisibly* (an "orphaned" session
/// argus's stop path can never signal again). A human attached to a worker's
/// terminal who presses Ctrl+Z out of ordinary shell muscle memory would
/// orphan the session with nothing to catch it.
///
/// The TUI already guards this footgun: it never forwards Ctrl+Z to the PTY
/// (it remaps the key to a pane-zoom / fullscreen toggle). The macOS SwiftUI
/// surface has no analogous zoom affordance, so it simply drops the byte — the
/// *intent* (Ctrl+Z never reaches the session) is mirrored, the mechanism is
/// not.
///
/// This lives in ArgusKit (pure Foundation) so the decision is unit-testable
/// without SwiftTerm/AppKit; `ArgusMac`'s terminal input delegate calls it at
/// the single outbound chokepoint (`TerminalCoordinator.send`).
public enum TerminalInput {

    /// Ctrl+Z — ASCII SUB. See the type doc for why it must never be forwarded.
    public static let suspendByte: UInt8 = 0x1A

    /// Returns `data` with every ``suspendByte`` (Ctrl+Z / `0x1A`) removed. All
    /// other bytes — including other control characters such as Ctrl+C (`0x03`),
    /// Ctrl+Y (`0x19`), and ESC (`0x1B`) — are preserved in their original
    /// order. The result may be empty (e.g. a lone Ctrl+Z keypress), in which
    /// case the caller forwards nothing.
    public static func sanitize(_ data: Data) -> Data {
        // Fast path: the overwhelmingly common keystroke carries no Ctrl+Z, so
        // return it untouched and avoid the copy/allocation entirely.
        guard data.contains(suspendByte) else { return data }
        return Data(data.filter { $0 != suspendByte })
    }
}
