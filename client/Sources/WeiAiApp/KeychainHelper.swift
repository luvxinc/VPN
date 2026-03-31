import Foundation
import Security

enum KeychainKey: String {
    case username     = "com.weiai.vpn.username"
    case password     = "com.weiai.vpn.password"
    case accessToken  = "com.weiai.vpn.access_token"
    case refreshToken = "com.weiai.vpn.refresh_token"
}

struct KeychainHelper {

    // Base attributes shared by all operations.
    // kSecUseDataProtectionKeychain = true: stores in the data-protection keychain,
    // which is NOT tied to the app's code-signing identity. This prevents macOS from
    // showing "allow/deny" dialogs when the app is updated (ad-hoc re-signed).
    private static func base(_ key: KeychainKey) -> [String: Any] {
        [
            kSecClass as String:                    kSecClassGenericPassword,
            kSecAttrService as String:              "com.weiai.vpn",
            kSecAttrAccount as String:              key.rawValue,
            kSecUseDataProtectionKeychain as String: true,
        ]
    }

    static func save(_ value: String, for key: KeychainKey) {
        var q = base(key)
        q[kSecValueData as String] = Data(value.utf8)
        SecItemDelete(q as CFDictionary)
        SecItemAdd(q as CFDictionary, nil)
    }

    static func load(_ key: KeychainKey) -> String? {
        var q = base(key)
        q[kSecReturnData as String] = true
        q[kSecMatchLimit as String] = kSecMatchLimitOne
        var result: AnyObject?
        guard SecItemCopyMatching(q as CFDictionary, &result) == errSecSuccess,
              let data = result as? Data
        else { return nil }
        return String(data: data, encoding: .utf8)
    }

    static func delete(_ key: KeychainKey) {
        SecItemDelete(base(key) as CFDictionary)
    }

    static func clearAll() {
        for key in [KeychainKey.username, .password, .accessToken, .refreshToken] {
            delete(key)
        }
    }
}
