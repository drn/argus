import SwiftUI

/// The root window: a sidebar of tasks + a detail pane, with a connection
/// indicator in the toolbar and a non-blocking error banner overlaid on top.
struct ContentView: View {
    @Environment(AppState.self) private var app

    var body: some View {
        NavigationSplitView {
            Sidebar()
                .navigationSplitViewColumnWidth(min: 240, ideal: 300, max: 420)
        } detail: {
            DetailView()
        }
        .toolbar {
            ToolbarItem(placement: .status) {
                ConnectionIndicator()
            }
        }
        .overlay(alignment: .top) {
            ConnectionBanner()
                .padding(.horizontal)
                .padding(.top, 8)
        }
    }
}
