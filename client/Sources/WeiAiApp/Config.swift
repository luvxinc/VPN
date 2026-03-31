import Foundation

struct AppConfig {
    let authURL: String
    let certFingerprint: String
    /// Optional Cloudflare CDN URL for auth requests (no cert pinning, standard TLS).
    /// When set, the client tries this first; falls back to authURL on failure.
    let cdnAuthURL: String?

    /// Load order:
    /// 1. ~/.config/weiai/config.json  (user override)
    /// 2. Bundle.main/.../config.json  (shipped with app)
    /// 3. Hardcoded dev defaults       (compile-time fallback)
    static func load() -> AppConfig {
        if let cfg = loadFromFile(userConfigURL()) { return cfg }
        if let cfg = loadFromFile(bundleConfigURL()) { return cfg }
        return devDefaults()
    }

    // MARK: - Private

    private static func userConfigURL() -> URL? {
        let dir = FileManager.default.homeDirectoryForCurrentUser
            .appendingPathComponent(".config/weiai")
        let url = dir.appendingPathComponent("config.json")
        return FileManager.default.fileExists(atPath: url.path) ? url : nil
    }

    private static func bundleConfigURL() -> URL? {
        Bundle.main.url(forResource: "config", withExtension: "json")
    }

    private static func loadFromFile(_ url: URL?) -> AppConfig? {
        guard let url else { return nil }
        guard let data = try? Data(contentsOf: url),
              let json = try? JSONSerialization.jsonObject(with: data) as? [String: String],
              let authURL = json["auth_url"], !authURL.isEmpty,
              let fingerprint = json["cert_fingerprint"], !fingerprint.isEmpty
        else { return nil }
        let cdnAuthURL = json["cdn_auth_url"].flatMap { $0.isEmpty ? nil : $0 }
        return AppConfig(authURL: authURL, certFingerprint: fingerprint, cdnAuthURL: cdnAuthURL)
    }

    private static func devDefaults() -> AppConfig {
        // Fallback when no config.json is found.
        // Copy client/Resources/config.example.json → client/Resources/config.json and fill in your values.
        AppConfig(
            authURL: "https://YOUR_SERVER_IP",
            certFingerprint: "YOUR_CERT_SHA256_FINGERPRINT",
            cdnAuthURL: nil
        )
    }
}
