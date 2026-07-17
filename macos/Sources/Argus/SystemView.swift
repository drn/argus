import ArgusKit
import SwiftUI

/// The Settings window's System tab: daemon status (`GET /api/status`) and
/// the cached host-load snapshot (`GET /api/system-metrics`), mirroring the
/// web SPA's Settings System panel (`internal/api/static/index.html`,
/// `renderSystemMetrics`). `*Avail` fields gate each metric — when false the
/// row renders "—" rather than a stale/zero number, matching the SPA.
///
/// Polls every 2s (matching the SPA's `SYS_METRICS_INTERVAL`) but only while
/// this tab is on screen: the poll loop is a `.task` modifier, which SwiftUI
/// cancels automatically when the tab is switched away from.
struct SystemView: View {
    @Environment(AppState.self) private var app

    @State private var status: DaemonStatus?
    @State private var metrics: SystemMetrics?
    @State private var errorMessage: String?
    @State private var isReloading = false

    private static let pollInterval: Duration = .seconds(2)

    var body: some View {
        Form {
            if let errorMessage {
                Section {
                    Label(errorMessage, systemImage: "exclamationmark.triangle.fill")
                        .foregroundStyle(.red)
                        .font(.callout)
                }
            }

            Section("Daemon") {
                LabeledContent("Status", value: status?.ok == true ? "Running" : "—")
                LabeledContent("Tasks", value: taskCountsLabel)
                LabeledContent("Sessions", value: sessionsLabel(status?.sessions))
            }

            Section("Host") {
                metricRow("CPU", value: cpuLabel, pct: metrics?.cpuAvail == true ? metrics?.cpuPercent : nil)
                LabeledContent("Load avg", value: loadLabel)
                metricRow("Memory", value: memLabel, pct: metrics?.memAvail == true ? metrics?.memPercent : nil)
                metricRow(
                    "Swap", value: swapLabel,
                    pct: (metrics?.swapAvail == true && (metrics?.swapTotal ?? 0) > 0) ? metrics?.swapPercent : nil
                )
                metricRow(
                    diskLabelTitle, value: diskLabel,
                    pct: metrics?.diskAvail == true ? metrics?.diskPercent : nil
                )
                LabeledContent("Argus process", value: procLabel)
                LabeledContent("Agent sessions", value: sessionsLabel(metrics?.sessions))
                LabeledContent("Uptime", value: uptimeLabel)
            }

            Section {
                HStack {
                    Spacer()
                    Button {
                        _Concurrency.Task { await reload() }
                    } label: {
                        if isReloading {
                            ProgressView().controlSize(.small).frame(width: 50)
                        } else {
                            Text("Reload").frame(width: 50)
                        }
                    }
                    .disabled(isReloading)
                }
            }
        }
        .formStyle(.grouped)
        .task {
            await refresh()
            while !_Concurrency.Task.isCancelled {
                try? await _Concurrency.Task.sleep(for: Self.pollInterval)
                if _Concurrency.Task.isCancelled { break }
                await refresh(silent: true)
            }
        }
    }

    @ViewBuilder
    private func metricRow(_ label: String, value: String, pct: Double?) -> some View {
        VStack(alignment: .leading, spacing: 4) {
            LabeledContent(label, value: value)
            if let pct {
                ProgressView(value: min(max(pct, 0), 100), total: 100)
                    .tint(pct >= 90 ? .red : pct >= 75 ? .orange : .accentColor)
            }
        }
    }

    // MARK: - Data

    private func refresh(silent: Bool = false) async {
        do {
            async let s = app.daemonStatus()
            async let m = app.systemMetrics()
            let (newStatus, newMetrics) = try await (s, m)
            status = newStatus
            metrics = newMetrics
            errorMessage = nil
        } catch is CancellationError {
            return
        } catch {
            // A transient poll failure shouldn't blank an already-loaded
            // panel; only surface the banner if nothing has loaded yet.
            if status == nil && metrics == nil {
                errorMessage = AppState.describe(error)
            }
        }
    }

    private func reload() async {
        isReloading = true
        await app.reloadConfig()
        await refresh()
        isReloading = false
    }

    // MARK: - Formatting

    private var taskCountsLabel: String {
        guard let t = status?.tasks else { return "—" }
        return "\(t.pending) pending, \(t.inProgress) in progress, \(t.inReview) in review, \(t.complete) complete"
    }

    private func sessionsLabel(_ counts: SessionCounts?) -> String {
        guard let counts else { return "—" }
        return "\(counts.running) running, \(counts.idle) idle"
    }

    private var cpuLabel: String {
        guard let m = metrics, m.cpuAvail else { return "—" }
        return String(format: "%.0f%%", m.cpuPercent)
    }

    private var loadLabel: String {
        guard let m = metrics, m.loadAvail else { return "—" }
        return [m.load1, m.load5, m.load15].map { String(format: "%.2f", $0) }.joined(separator: "  ")
    }

    private var memLabel: String {
        guard let m = metrics, m.memAvail else { return "—" }
        return "\(Self.fmtSize(m.memUsed)) / \(Self.fmtSize(m.memTotal))  (\(Int(m.memPercent.rounded()))%)"
    }

    private var swapLabel: String {
        guard let m = metrics, m.swapAvail else { return "—" }
        guard m.swapTotal > 0 else { return "none" }
        return "\(Self.fmtSize(m.swapUsed)) / \(Self.fmtSize(m.swapTotal))  (\(Int(m.swapPercent.rounded()))%)"
    }

    private var diskLabelTitle: String {
        guard let path = metrics?.diskPath, !path.isEmpty else { return "Disk" }
        return "Disk (\(path))"
    }

    private var diskLabel: String {
        guard let m = metrics, m.diskAvail else { return "—" }
        return "\(Self.fmtSize(m.diskUsed)) / \(Self.fmtSize(m.diskTotal))" +
            "  (\(Int(m.diskPercent.rounded()))%, \(Self.fmtSize(m.diskFree)) free)"
    }

    private var procLabel: String {
        guard let m = metrics, m.procAvail else { return "—" }
        return "\(Self.fmtSize(m.procRSS)) RSS"
    }

    private var uptimeLabel: String {
        guard let m = metrics, m.uptimeAvail else { return "—" }
        return Self.fmtUptime(m.uptimeSec)
    }

    private static func fmtSize(_ n: UInt64) -> String {
        let units = ["B", "KiB", "MiB", "GiB", "TiB"]
        var value = Double(n)
        var i = 0
        while value >= 1024 && i < units.count - 1 {
            value /= 1024
            i += 1
        }
        let formatted = i == 0 ? String(Int(value)) : String(format: "%.1f", value)
        return "\(formatted) \(units[i])"
    }

    private static func fmtUptime(_ sec: UInt64) -> String {
        let d = sec / 86400, h = (sec % 86400) / 3600, m = (sec % 3600) / 60
        if d > 0 { return "\(d)d \(h)h" }
        if h > 0 { return "\(h)h \(m)m" }
        return "\(m)m"
    }
}
