import Foundation

/// Manages the one-time installation of the privileged helper tool.
///
/// On first connect the app installs:
///   /usr/local/bin/weiai-helper        — the shell script that does all root operations
///   /etc/sudoers.d/weiai-vpn           — NOPASSWD rule so sudo needs no password
///
/// After installation every privileged action uses:
///   sudo /usr/local/bin/weiai-helper <action> <args...>
/// with no password prompt — ever again.
enum PrivilegedHelper {

    static let helperPath  = "/usr/local/bin/weiai-helper"
    static let sudoersPath = "/etc/sudoers.d/weiai-vpn"

    /// Returns true if the helper and sudoers rule are already installed.
    static var isInstalled: Bool {
        FileManager.default.fileExists(atPath: helperPath) &&
        FileManager.default.fileExists(atPath: sudoersPath)
    }

    /// Runs a privileged action without a password (requires prior installation).
    /// Returns the termination status.
    @discardableResult
    static func run(_ args: [String]) -> Int32 {
        let task = Process()
        task.executableURL  = URL(fileURLWithPath: "/usr/bin/sudo")
        task.arguments      = [helperPath] + args
        task.standardOutput = Pipe()
        task.standardError  = Pipe()
        try? task.run()
        task.waitUntilExit()
        return task.terminationStatus
    }

    /// Installs the helper via a single osascript admin prompt.
    /// `bundleHelperPath` is the path to weiai-helper.sh inside the app bundle.
    /// Returns an error message or nil on success.
    static func install(bundleHelperPath: String) -> String? {
        let installScript = """
        #!/bin/sh
        cp '\(bundleHelperPath)' \(helperPath)
        chmod 755 \(helperPath)
        echo '%admin ALL=(root) NOPASSWD: \(helperPath)' > \(sudoersPath)
        chmod 440 \(sudoersPath)
        """
        let scriptPath = "/tmp/weiai_install_helper.sh"
        do {
            try installScript.write(toFile: scriptPath, atomically: true, encoding: .utf8)
            try FileManager.default.setAttributes([.posixPermissions: 0o755], ofItemAtPath: scriptPath)
        } catch {
            return "Failed to write install script: \(error.localizedDescription)"
        }

        let appleScript = "do shell script \"\(scriptPath)\" with administrator privileges"
        let task = Process()
        task.executableURL  = URL(fileURLWithPath: "/usr/bin/osascript")
        task.arguments      = ["-e", appleScript]
        task.standardOutput = Pipe()
        task.standardError  = Pipe()
        do { try task.run(); task.waitUntilExit() } catch {
            return "osascript failed: \(error.localizedDescription)"
        }
        guard task.terminationStatus == 0 else {
            return Bundle.main.localizedString(forKey: "error.authCancelled",
                                               value: "Authorization cancelled",
                                               table: nil)
        }
        return nil
    }
}
