/// Single source of truth for WeiAi VPN client version.
///
/// Format: MAJOR.MINOR.PATCH
///   MAJOR — breaking protocol change (requires server update)
///   MINOR — new feature, backward-compatible
///   PATCH — bug fix, no API change
enum AppVersion {
    static let current     = "1.0.9"
    static let releaseDate = "2026-03-30"
    static let author      = "Aaron Tong"

    /// Sent to the server in X-Client-Version header.
    static let headerValue = "WeiAiVPN/\(current)"
}
