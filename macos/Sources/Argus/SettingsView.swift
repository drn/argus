import SwiftUI

/// The Settings scene (Cmd+,): a General tab (server URL + token overrides,
/// notification/menu-bar toggles) and a System tab (daemon status + host
/// metrics, see ``SystemView``).
struct SettingsView: View {
    var body: some View {
        TabView {
            GeneralSettingsTab()
                .tabItem { Label("General", systemImage: "gearshape") }
            SystemView()
                .tabItem { Label("System", systemImage: "cpu") }
        }
        .frame(width: 460)
    }
}

/// The original single-Form settings surface, now the TabView's General tab.
private struct GeneralSettingsTab: View {
    @Environment(AppState.self) private var app

    @State private var serverURL = ""
    @State private var token = ""
    @State private var didLoad = false

    var body: some View {
        @Bindable var app = app
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
                Toggle("Notify when a task needs input", isOn: $app.notifyOnNeedsInput)
                Toggle("Notify when a task goes idle", isOn: $app.notifyOnIdle)
            } header: {
                Text("Notifications")
            } footer: {
                Text("Idle notifications appear only when Argus is not the frontmost app.")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }

            Section {
                Toggle("Show menu-bar extra", isOn: $app.showMenuBarExtra)
            } header: {
                Text("Menu Bar")
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
