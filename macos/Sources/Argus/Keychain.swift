import Foundation
import Security

/// A tiny generic-password wrapper over the Security framework for the single
/// secret Argus persists: the API token override.
///
/// The token never leaves the Keychain except to build an ``ArgusKit/ArgusConfig``
/// in memory; it is never logged or printed.
enum Keychain {
    private static let service = "com.thanx.argusmac"
    private static let account = "api-token-override"

    /// Reads the stored token, or `nil` if none is set.
    static func readToken() -> String? {
        let query: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: account,
            kSecReturnData as String: true,
            kSecMatchLimit as String: kSecMatchLimitOne,
        ]
        var item: CFTypeRef?
        let status = SecItemCopyMatching(query as CFDictionary, &item)
        guard status == errSecSuccess,
              let data = item as? Data,
              let token = String(data: data, encoding: .utf8),
              !token.isEmpty
        else {
            return nil
        }
        return token
    }

    /// Stores (or, for `nil`/empty, clears) the token. Delete-then-add keeps the
    /// call idempotent without branching on prior existence.
    static func writeToken(_ token: String?) {
        let base: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: account,
        ]
        SecItemDelete(base as CFDictionary)

        guard let token, !token.isEmpty,
              let data = token.data(using: .utf8) else {
            return
        }
        var add = base
        add[kSecValueData as String] = data
        add[kSecAttrAccessible as String] = kSecAttrAccessibleAfterFirstUnlock
        SecItemAdd(add as CFDictionary, nil)
    }
}
