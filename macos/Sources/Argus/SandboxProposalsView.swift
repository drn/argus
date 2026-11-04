import ArgusKit
import SwiftUI

/// Settings "Sandbox" tab: pending agent-proposed sandbox write paths that a
/// human approves or rejects. All proposal strings are untrusted agent text and
/// are rendered as plain `Text`.
struct SandboxProposalsView: View {
    @Environment(AppState.self) private var app

    @State private var proposals: [SandboxProposal] = []
    @State private var isLoading = true
    @State private var loadError: String?

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack {
                Text("Sandbox write proposals").font(.headline)
                Spacer()
                Button { _Concurrency.Task { await load() } } label: {
                    Label("Refresh", systemImage: "arrow.clockwise")
                }
            }
            Text(SandboxProposalLogic.appliesNote)
                .font(.caption).foregroundStyle(.secondary)
            if let loadError {
                Text(loadError).foregroundStyle(.red).font(.callout)
            }
            if isLoading && proposals.isEmpty {
                ProgressView().frame(maxWidth: .infinity)
            } else if proposals.isEmpty {
                Text("No pending proposals.").foregroundStyle(.secondary)
                    .frame(maxWidth: .infinity, alignment: .center).padding(.top, 20)
            } else {
                ScrollView {
                    VStack(spacing: 12) {
                        ForEach(proposals) { p in
                            SandboxProposalCard(proposal: p) { await load() }
                        }
                    }
                }
            }
            Spacer(minLength: 0)
        }
        .padding()
        .frame(width: 460, height: 440)
        .task { await load() }
    }

    private func load() async {
        isLoading = true
        defer { isLoading = false }
        do {
            proposals = try await app.sandboxProposals()
            loadError = nil
        } catch {
            loadError = SandboxProposalLogic.errorText(error)
        }
    }
}

private struct SandboxProposalCard: View {
    @Environment(AppState.self) private var app
    let proposal: SandboxProposal
    let onChanged: () async -> Void

    @State private var scope = "global"
    @State private var paths: [String] = []
    @State private var didInit = false
    @State private var busy = false
    @State private var errorText: String?
    @State private var resultLines: [String] = []

    private var problem: String? { SandboxProposalLogic.blockingProblem(paths: paths) }

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack {
                Text("Task \(proposal.taskID)").font(.subheadline.bold())
                Spacer()
                Text(proposal.project.isEmpty ? "no project" : proposal.project)
                    .font(.caption).foregroundStyle(.secondary)
            }

            ForEach(Array(proposal.entries.enumerated()), id: \.offset) { i, entry in
                VStack(alignment: .leading, spacing: 2) {
                    if i < paths.count {
                        TextField("Path", text: $paths[i]).textFieldStyle(.roundedBorder)
                        if let hint = SandboxProposalLogic.validationHint(paths[i]) {
                            Text(hint).font(.caption).foregroundStyle(.orange)
                        }
                    }
                    if !entry.reason.isEmpty {
                        Text(entry.reason).font(.caption).foregroundStyle(.secondary)
                    }
                    if let w = entry.warning {
                        Label(w, systemImage: "exclamationmark.triangle.fill")
                            .font(.caption.bold()).foregroundStyle(.red)
                    }
                }
            }

            Picker("Scope", selection: $scope) {
                if SandboxProposalLogic.projectScopeAvailable(proposal) {
                    Text("Project").tag("project")
                }
                Text("Global").tag("global")
            }
            .pickerStyle(.segmented)
            if !SandboxProposalLogic.projectScopeAvailable(proposal) {
                Text("Project scope unavailable: this proposal has no project.")
                    .font(.caption).foregroundStyle(.secondary)
            }

            if let errorText { Text(errorText).font(.callout).foregroundStyle(.red) }
            if !resultLines.isEmpty {
                VStack(alignment: .leading, spacing: 2) {
                    ForEach(resultLines, id: \.self) { Text($0).font(.caption) }
                }
            }

            HStack {
                Spacer()
                Button("Reject") { act { try await app.rejectSandboxProposal(id: proposal.id) } }
                    .disabled(busy)
                Button("Approve") { approve() }
                    .disabled(busy || problem != nil)
                    .help(problem ?? "Approve for \(scope) scope")
            }
        }
        .padding(10)
        .background(RoundedRectangle(cornerRadius: 8).fill(.quaternary.opacity(0.4)))
        .onAppear {
            guard !didInit else { return }
            didInit = true
            scope = SandboxProposalLogic.defaultScope(for: proposal)
            paths = proposal.entries.map(\.path)
        }
    }

    private func approve() {
        let req = SandboxApproveRequest(
            scope: scope,
            paths: SandboxProposalLogic.pathsToSend(edited: paths, for: proposal))
        busy = true
        errorText = nil
        _Concurrency.Task {
            do {
                let r = try await app.approveSandboxProposal(id: proposal.id, req)
                resultLines = SandboxProposalLogic.resultSummary(r)
                busy = false
                // Leave the summary visible briefly; the refreshed list drops this card.
                try? await _Concurrency.Task.sleep(nanoseconds: 2_500_000_000)
                await onChanged()
            } catch {
                errorText = SandboxProposalLogic.errorText(error)
                busy = false
            }
        }
    }

    private func act(_ op: @escaping () async throws -> Void) {
        busy = true
        errorText = nil
        _Concurrency.Task {
            do {
                try await op()
                busy = false
                await onChanged()
            } catch {
                errorText = SandboxProposalLogic.errorText(error)
                busy = false
            }
        }
    }
}
