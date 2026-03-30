import Foundation
import Security

enum KeychainKey: String {
    case username     = "com.weiai.vpn.username"
    case password     = "com.weiai.vpn.password"
    case accessToken  = "com.weiai.vpn.access_token"
    case refreshToken = "com.weiai.vpn.refresh_token"
}

struct KeychainHelper {

    static func save(_ value: String, for key: KeychainKey) {
        let data = Data(value.utf8)
        let query: [String: Any] = [
            kSecClass as String:       kSecClassGenericPassword,
            kSecAttrAccount as String: key.rawValue,
            kSecValueData as String:   data,
        ]
        SecItemDelete(query as CFDictionary)
        SecItemAdd(query as CFDictionary, nil)
    }

    static func load(_ key: KeychainKey) -> String? {
        let query: [String: Any] = [
            kSecClass as String:       kSecClassGenericPassword,
            kSecAttrAccount as String: key.rawValue,
            kSecReturnData as String:  true,
            kSecMatchLimit as String:  kSecMatchLimitOne,
        ]
        var result: AnyObject?
        guard SecItemCopyMatching(query as CFDictionary, &result) == errSecSuccess,
              let data = result as? Data
        else { return nil }
        return String(data: data, encoding: .utf8)
    }

    static func delete(_ key: KeychainKey) {
        let query: [String: Any] = [
            kSecClass as String:       kSecClassGenericPassword,
            kSecAttrAccount as String: key.rawValue,
        ]
        SecItemDelete(query as CFDictionary)
    }

    static func clearAll() {
        for key in [KeychainKey.username, .password, .accessToken, .refreshToken] {
            delete(key)
        }
    }
}
