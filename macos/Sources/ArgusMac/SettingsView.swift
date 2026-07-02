import SwiftUI

/// The Settings scene (Cmd+,): server URL + token overrides.
///
/// The URL persists to UserDefaults; the token persists to the Keychain. Leaving
/// a field empty clears that override — the URL falls back to the default local
/// port and the token falls back to `~/.argus/api-token`. Saving reconnects.
struct SettingsView: View {
    @Environment(AppState.self) private var app

    @State private var serverURL = ""
    @State private var token = ""
    @State private var didLoad = false

    var body: some View {
        Form {
            Section {
                TextField(
                    "Server URL",
                    text: $serverURL,
                    prompt: Text(defaultURLPlaceholder)
                )
                .textContentType(.URL)
                SecureField(
                    "API Token",
                    text: $token,
                    prompt: Text("Read from ~/.argus/api-token")
                )
            } header: {
                Text("Connection")
            } footer: {
                Text("Leave a field blank to use the default. The token is stored in your Keychain and never logged.")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }

            Section {
                HStack {
                    Spacer()
                    Button("Save & Reconnect") { save() }
                        .keyboardShortcut(.defaultAction)
                }
            }
        }
        .formStyle(.grouped)
        .frame(width: 460)
        .padding()
        .onAppear(perform: loadIfNeeded)
    }

    private var defaultURLPlaceholder: String {
        // Show the ArgusKit default so the user knows what "blank" resolves to.
        "http://127.0.0.1:7743"
    }

    private func loadIfNeeded() {
        guard !didLoad else { return }
        didLoad = true
        serverURL = app.preferences.serverURLString ?? ""
        token = app.preferences.tokenOverride ?? ""
    }

    private func save() {
        let trimmedURL = serverURL.trimmingCharacters(in: .whitespacesAndNewlines)
        let trimmedToken = token.trimmingCharacters(in: .whitespacesAndNewlines)
        app.preferences.serverURLString = trimmedURL.isEmpty ? nil : trimmedURL
        app.preferences.tokenOverride = trimmedToken.isEmpty ? nil : trimmedToken
        app.connect()
    }
}
