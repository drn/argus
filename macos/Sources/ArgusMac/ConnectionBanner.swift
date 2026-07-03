import SwiftUI

/// A non-blocking banner shown only while the connection is in an error state,
/// with a Retry button. Renders nothing when connected or connecting.
struct ConnectionBanner: View {
    @Environment(AppState.self) private var app

    var body: some View {
        if case .error(let message) = app.connection {
            HStack(spacing: 10) {
                Image(systemName: "exclamationmark.triangle.fill")
                    .foregroundStyle(.orange)
                Text(message)
                    .lineLimit(2)
                    .font(.callout)
                Spacer(minLength: 8)
                Button("Retry") { app.retry() }
                    .buttonStyle(.borderedProminent)
                    .controlSize(.small)
            }
            .padding(.horizontal, 12)
            .padding(.vertical, 8)
            .background(.regularMaterial, in: RoundedRectangle(cornerRadius: 8))
            .overlay(
                RoundedRectangle(cornerRadius: 8)
                    .strokeBorder(.orange.opacity(0.4))
            )
            .shadow(radius: 2, y: 1)
            .transition(.move(edge: .top).combined(with: .opacity))
        }
    }
}
