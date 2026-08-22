import SwiftUI

/// A sheet listing the app's keyboard shortcuts, opened via Shift+Cmd+/
/// (mirrors the standard macOS "Show Help" idiom) or the toolbar's "?"
/// button. Mirrors the existing sheet-presentation pattern (``RenameSheet``
/// + its own `.sheet(item:)` mount point) — see ``ContentView``'s
/// `.sheet(isPresented: $app.isPresentingShortcutsHelp)`.
///
/// The list below is a hardcoded static array rather than an introspection
/// of a centralized dispatch table, per design.md D1 (this app has no such
/// table — every shortcut is attached directly to its own button/menu
/// item). Appending a row for a later stage's shortcut is a one-line edit
/// to ``ShortcutsHelpSheet/sections``.
struct ShortcutsHelpSheet: View {
    @Environment(\.dismiss) private var dismiss

    struct Shortcut: Identifiable {
        let id = UUID()
        let action: String
        let chord: String
    }

    struct Section: Identifiable {
        let id = UUID()
        let title: String
        let shortcuts: [Shortcut]
    }

    /// Every shortcut in the app, grouped by area. Includes shortcuts owned
    /// by other stages of `add-mac-keybinding-parity` (archive/pin, filter
    /// focus, terminal chords) so this sheet is a single reference the
    /// moment all stages land, not just this one.
    static let sections: [Section] = [
        Section(title: "Global", shortcuts: [
            Shortcut(action: "New Task", chord: "\u{2318}N"),
            Shortcut(action: "Rename Task", chord: "\u{2318}R"),
            Shortcut(action: "Schedules", chord: "\u{21E7}\u{2318}S"),
            Shortcut(action: "Show Shortcuts", chord: "\u{21E7}\u{2318}/"),
            Shortcut(action: "Jump to Next Needs-Input Task", chord: "\u{21E7}\u{2318}J"),
            Shortcut(action: "Focus Filter", chord: "\u{2318}F"),
            Shortcut(action: "Quit", chord: "\u{2318}Q"),
        ]),
        Section(title: "Detail Tabs", shortcuts: [
            Shortcut(action: "Switch to Terminal", chord: "\u{2318}1"),
            Shortcut(action: "Switch to Diff", chord: "\u{2318}2"),
            Shortcut(action: "Switch to Files", chord: "\u{2318}3"),
            Shortcut(action: "Switch to Info", chord: "\u{2318}4"),
        ]),
        Section(title: "Task Actions", shortcuts: [
            Shortcut(action: "Fork Task", chord: "\u{21E7}\u{2318}B"),
            Shortcut(action: "Open Repo in Finder", chord: "\u{21E7}\u{2318}E"),
            Shortcut(action: "Open PR", chord: "\u{21E7}\u{2318}U"),
            Shortcut(action: "Delete Task", chord: "\u{2318}\u{232B}"),
            Shortcut(action: "Archive Task", chord: "\u{21E7}\u{2318}A"),
            Shortcut(action: "Pin Task", chord: "\u{21E7}\u{2318}P"),
        ]),
        Section(title: "Terminal", shortcuts: [
            Shortcut(action: "Previous / Next Task", chord: "\u{2318}\u{2191} / \u{2318}\u{2193}"),
            Shortcut(action: "Move Focus Between Panes", chord: "\u{2318}\u{2190} / \u{2318}\u{2192}"),
            Shortcut(action: "Scroll Terminal", chord: "\u{21E7}\u{2191}/\u{2193}/PgUp/PgDn/End"),
            Shortcut(action: "Copy Visible Output", chord: "\u{21E7}\u{2318}C"),
        ]),
    ]

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            HStack {
                Text("Keyboard Shortcuts")
                    .font(.title3.bold())
                Spacer()
                Button("Done") { dismiss() }
                    .keyboardShortcut(.defaultAction)
            }
            .padding()

            Divider()

            ScrollView {
                VStack(alignment: .leading, spacing: 20) {
                    ForEach(Self.sections) { section in
                        VStack(alignment: .leading, spacing: 6) {
                            Text(section.title)
                                .font(.headline)
                                .foregroundStyle(.secondary)
                            ForEach(section.shortcuts) { shortcut in
                                HStack {
                                    Text(shortcut.action)
                                    Spacer(minLength: 24)
                                    Text(shortcut.chord)
                                        .font(.system(.body, design: .monospaced))
                                        .foregroundStyle(.secondary)
                                }
                            }
                        }
                    }
                }
                .padding()
            }
        }
        .frame(width: 420, height: 480)
    }
}
