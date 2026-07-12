import AppKit
import ArgusKit
import SwiftUI

/// Dark-mode-aware colours for the git surfaces. Built from dynamic `NSColor`
/// providers (no asset catalog) so every swatch resolves correctly in both
/// appearances, and blended toward the system background so line fills stay
/// legible against `textBackgroundColor`.
enum GitColors {
    /// Background fill for an added / removed / context diff line.
    static func lineBackground(_ kind: DiffLineKind) -> Color {
        switch kind {
        case .added: return added
        case .removed: return removed
        case .context: return .clear
        }
    }

    /// Foreground tint for the gutter marker / line-number column.
    static func marker(_ kind: DiffLineKind) -> Color {
        switch kind {
        case .added: return addedStrong
        case .removed: return removedStrong
        case .context: return .secondary
        }
    }

    /// Status-letter colour for the file list / panel headers (M/A/D/U).
    static func status(_ code: String) -> Color {
        switch code.first {
        case "A": return addedStrong
        case "D": return removedStrong
        case "U": return .orange
        default: return .accentColor // M and everything else
        }
    }

    // MARK: - Swatches

    private static let added = Color(nsColor: dynamic(
        light: NSColor(calibratedRed: 0.82, green: 0.94, blue: 0.82, alpha: 1),
        dark: NSColor(calibratedRed: 0.12, green: 0.26, blue: 0.15, alpha: 1)))

    private static let removed = Color(nsColor: dynamic(
        light: NSColor(calibratedRed: 0.98, green: 0.84, blue: 0.84, alpha: 1),
        dark: NSColor(calibratedRed: 0.32, green: 0.14, blue: 0.15, alpha: 1)))

    private static let addedStrong = Color(nsColor: dynamic(
        light: NSColor(calibratedRed: 0.13, green: 0.55, blue: 0.20, alpha: 1),
        dark: NSColor(calibratedRed: 0.45, green: 0.82, blue: 0.50, alpha: 1)))

    private static let removedStrong = Color(nsColor: dynamic(
        light: NSColor(calibratedRed: 0.72, green: 0.16, blue: 0.16, alpha: 1),
        dark: NSColor(calibratedRed: 0.94, green: 0.50, blue: 0.50, alpha: 1)))

    /// A dynamic colour that picks `light` or `dark` from the drawing
    /// appearance. `NSColor(name:dynamicProvider:)` re-evaluates on appearance
    /// change, so SwiftUI redraws correctly when the system theme flips.
    private static func dynamic(light: NSColor, dark: NSColor) -> NSColor {
        NSColor(name: nil) { appearance in
            let isDark = appearance.bestMatch(from: [.aqua, .darkAqua]) == .darkAqua
            return isDark ? dark : light
        }
    }
}
