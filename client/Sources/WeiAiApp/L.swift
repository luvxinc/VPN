import Foundation

/// Shorthand for NSLocalizedString. Falls back to the key itself if no
/// translation is found (safe in dev builds where .lproj isn't present).
func L(_ key: String) -> String {
    Bundle.main.localizedString(forKey: key, value: key, table: nil)
}
