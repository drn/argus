import Foundation

/// Pure decision logic for the sandbox-proposal UI.
public enum SandboxProposalLogic {
    public static let appliesNote = "Applies to NEW sessions only — restart affected tasks."

    /// Project scope when the proposal has a project, else global.
    public static func defaultScope(for proposal: SandboxProposal) -> String {
        proposal.project.isEmpty ? "global" : "project"
    }

    public static func projectScopeAvailable(_ proposal: SandboxProposal) -> Bool {
        !proposal.project.isEmpty
    }

    /// Trim, drop blanks, de-duplicate preserving order.
    public static func normalizePaths(_ raw: [String]) -> [String] {
        var seen = Set<String>()
        var out: [String] = []
        for p in raw {
            let t = p.trimmingCharacters(in: .whitespacesAndNewlines)
            if t.isEmpty || !seen.insert(t).inserted { continue }
            out.append(t)
        }
        return out
    }

    /// Client-side hint only (the daemon is the authority); nil when fine.
    public static func validationHint(_ path: String) -> String? {
        let t = path.trimmingCharacters(in: .whitespacesAndNewlines)
        if t.isEmpty { return "Path is empty" }
        if t.hasPrefix("~") { return nil }
        if !t.hasPrefix("/") { return "Path must be absolute (start with / or ~)" }
        if t.split(separator: "/").contains("..") { return "Path must not contain .." }
        if t == "/" { return "Refusing to grant the filesystem root" }
        return nil
    }

    /// Nil when the path list may be submitted; otherwise a user-facing reason.
    public static func blockingProblem(paths: [String]) -> String? {
        let norm = normalizePaths(paths)
        if norm.isEmpty { return "Add at least one path to approve" }
        for p in norm { if let h = validationHint(p) { return "\(p): \(h)" } }
        return nil
    }

    /// `paths` to send: nil when unchanged from the proposal (approve as-is).
    public static func pathsToSend(edited: [String], for proposal: SandboxProposal) -> [String]? {
        let norm = normalizePaths(edited)
        return norm == normalizePaths(proposal.entries.map(\.path)) ? nil : norm
    }

    public static func resultSummary(_ r: SandboxApproveResult) -> [String] {
        var lines = ["Approved (\(r.scope) scope)."]
        if !r.applied.isEmpty { lines.append("Applied: " + r.applied.joined(separator: ", ")) }
        if !r.alreadyGranted.isEmpty { lines.append("Already granted: " + r.alreadyGranted.joined(separator: ", ")) }
        for w in r.warnings { lines.append("Warning: " + w) }
        lines.append(appliesNote)
        return lines
    }

    public static func errorText(_ error: Error) -> String {
        guard let e = error as? ArgusError, let status = e.httpStatus else {
            return String(describing: error)
        }
        if case let .http(_, body) = e {
            var msg = body
            if let d = body.data(using: .utf8),
               let obj = try? JSONSerialization.jsonObject(with: d) as? [String: Any],
               let m = obj["error"] as? String { msg = m }
            switch status {
            case 403: return "Master token required to decide proposals. Set the master API token in General settings."
            case 409: return "Already decided: " + msg
            case 404: return "Proposal not found (it may have been removed): " + msg
            default: return msg.isEmpty ? "HTTP \(status)" : msg
            }
        }
        return String(describing: e)
    }
}
