import SwiftUI

/// Toolbar connection status: green dot (connected), spinner (connecting), or
/// red dot with the error as a tooltip.
struct ConnectionIndicator: View {
    @Environment(AppState.self) private var app

    var body: some View {
        switch app.connection {
        case .connecting:
            ProgressView()
                .controlSize(.small)
                .help("Connecting…")
        case .connected:
            Circle()
                .fill(.green)
                .frame(width: 10, height: 10)
                .help("Connected")
        case .error(let message):
            Circle()
                .fill(.red)
                .frame(width: 10, height: 10)
                .help(message)
        }
    }
}
