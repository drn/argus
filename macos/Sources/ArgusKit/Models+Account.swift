import Foundation

/// Which tools an account can run, from `GET /api/accounts`' `supports`.
public struct AccountSupports: Sendable, Equatable, Decodable {
    public let claude: Bool
    public let codex: Bool

    public init(claude: Bool = false, codex: Bool = false) {
        self.claude = claude
        self.codex = codex
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        claude = try c.decodeIfPresent(Bool.self, forKey: .claude) ?? false
        codex = try c.decodeIfPresent(Bool.self, forKey: .codex) ?? false
    }

    enum CodingKeys: String, CodingKey { case claude, codex }
}

/// An account (e.g. work / personal) as returned by `GET /api/accounts`. It can
/// carry a Claude config dir and/or a Codex home; the sign-in fields describe
/// the Claude login only. The daemon never returns credentials.
public struct Account: Sendable, Equatable, Identifiable, Decodable {
    public let name: String
    public let label: String
    public let claudeConfigDir: String
    public let codexHome: String
    public let supports: AccountSupports
    public let isDefault: Bool
    public let loggedIn: Bool
    public let email: String?
    public let org: String?
    public let plan: String?

    public var id: String { name }

    /// Label for pickers; falls back to the name when the daemon sends none.
    public var displayLabel: String { label.isEmpty ? name : label }

    enum CodingKeys: String, CodingKey {
        case name, label, email, org, plan, supports
        case claudeConfigDir = "claude_config_dir"
        case codexHome = "codex_home"
        case isDefault = "is_default"
        case loggedIn = "logged_in"
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        name = try c.decode(String.self, forKey: .name)
        label = try c.decodeIfPresent(String.self, forKey: .label) ?? ""
        claudeConfigDir = try c.decodeIfPresent(String.self, forKey: .claudeConfigDir) ?? ""
        codexHome = try c.decodeIfPresent(String.self, forKey: .codexHome) ?? ""
        supports = try c.decodeIfPresent(AccountSupports.self, forKey: .supports) ?? AccountSupports()
        isDefault = try c.decodeIfPresent(Bool.self, forKey: .isDefault) ?? false
        loggedIn = try c.decodeIfPresent(Bool.self, forKey: .loggedIn) ?? false
        email = try c.decodeIfPresent(String.self, forKey: .email)
        org = try c.decodeIfPresent(String.self, forKey: .org)
        plan = try c.decodeIfPresent(String.self, forKey: .plan)
    }

    public init(name: String, label: String = "", claudeConfigDir: String = "", codexHome: String = "",
                supports: AccountSupports = AccountSupports(), isDefault: Bool = false,
                loggedIn: Bool = false, email: String? = nil, org: String? = nil, plan: String? = nil) {
        self.name = name
        self.label = label
        self.claudeConfigDir = claudeConfigDir
        self.codexHome = codexHome
        self.supports = supports
        self.isDefault = isDefault
        self.loggedIn = loggedIn
        self.email = email
        self.org = org
        self.plan = plan
    }
}

/// Pure decision logic for the New Task sheet's account picker.
public enum NewTaskAccount {
    public enum Tool: Sendable, Equatable { case claude, codex, other }

    /// Classifies a backend by name (the macOS client sees names, not commands).
    /// An empty or unknown name is treated as Claude, the daemon's usual default;
    /// the daemon's `config.AccountSupports` check stays the real authority.
    public static func tool(forBackend backend: String) -> Tool {
        switch backend.lowercased() {
        case "codex": return .codex
        case "pi", "opencode": return .other
        default: return .claude
        }
    }

    /// The reserved default account plus every account supporting the backend.
    public static func accounts(_ accounts: [Account], forBackend backend: String) -> [Account] {
        let t = tool(forBackend: backend)
        return accounts.filter { a in
            if a.name == "default" { return true }
            switch t {
            case .claude: return a.supports.claude
            case .codex: return a.supports.codex
            case .other: return false
            }
        }
    }

    /// The account to preselect: the user's last pick when it is usable for the
    /// backend, else the account the daemon resolves for the project
    /// (`isDefault`), else the built-in default.
    public static func preselect(_ all: [Account], backend: String, picked: String) -> String {
        let usable = accounts(all, forBackend: backend)
        if !picked.isEmpty, usable.contains(where: { $0.name == picked }) { return picked }
        return usable.first(where: \.isDefault)?.name ?? "default"
    }

    /// The `account` to send. A shown picker is always an explicit choice, so
    /// the selected name is sent ("default" included). nil means no choice was
    /// offered (one or zero usable accounts) or the selection is not usable for
    /// the backend; the daemon then resolves the project / global default.
    public static func requestValue(selected: String, accounts all: [Account], backend: String) -> String? {
        let usable = accounts(all, forBackend: backend)
        guard usable.count > 1, usable.contains(where: { $0.name == selected }) else { return nil }
        return selected
    }
}
