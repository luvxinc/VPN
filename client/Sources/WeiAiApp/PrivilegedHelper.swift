import Foundation

/// Manages the one-time installation of the privileged helper tool.
///
/// On first connect the app installs:
///   /usr/local/bin/weiai-helper        — the shell script that does all root operations
///   /etc/sudoers.d/weiai-vpn           — NOPASSWD rule so sudo needs no password
///
/// After installation every privileged action uses:
///   sudo -n /usr/local/bin/weiai-helper <action> <args...>
/// with no password prompt — ever again.
enum PrivilegedHelper {

    static let helperPath  = "/usr/local/bin/weiai-helper"
    static let sudoersPath = "/etc/sudoers.d/weiai-vpn"

    /// True only when helper exists, sudoers rule exists, AND sudo actually works without a password.
    static var isInstalled: Bool {
        guard FileManager.default.fileExists(atPath: helperPath),
              FileManager.default.fileExists(atPath: sudoersPath) else { return false }
        return _sudoWorks()
    }

    /// Runs `sudo -n helper ks-off` to confirm the NOPASSWD rule is active.
    private static func _sudoWorks() -> Bool {
        let t = Process()
        t.executableURL  = URL(fileURLWithPath: "/usr/bin/sudo")
        t.arguments      = ["-n", helperPath, "ks-off"]
        t.standardOutput = Pipe()
        t.standardError  = Pipe()
        try? t.run(); t.waitUntilExit()
        return t.terminationStatus == 0
    }

    /// Runs a privileged action without a password prompt (-n = non-interactive, fail if password needed).
    @discardableResult
    static func run(_ args: [String]) -> Int32 {
        let task = Process()
        task.executableURL  = URL(fileURLWithPath: "/usr/bin/sudo")
        task.arguments      = ["-n", helperPath] + args
        task.standardOutput = Pipe()
        task.standardError  = Pipe()
        try? task.run()
        task.waitUntilExit()
        return task.terminationStatus
    }

    /// Installs the helper via a single osascript admin prompt (one-time, lifetime).
    /// `bundleHelperPath` is the path to weiai-helper.sh inside the app bundle.
    /// Returns an error message on failure, or nil on success.
    static func install(bundleHelperPath: String) -> String? {
        let tmpSudoers = "/tmp/weiai-vpn-sudoers-tmp"

        // set -e: any failed command aborts the script → osascript returns non-zero → we detect it.
        // mkdir -p: /usr/local/bin may not exist on a fresh macOS install.
        // chown root:wheel: required for sudoers.d files to be accepted by sudo.
        // visudo -c -f: validates syntax before writing to the real path.
        let installScript = """
        #!/bin/sh
        set -e
        mkdir -p /usr/local/bin
        cp '\(bundleHelperPath)' '\(helperPath)'
        chmod 755 '\(helperPath)'
        chown root:wheel '\(helperPath)'
        echo '%admin ALL=(root) NOPASSWD: \(helperPath)' > '\(tmpSudoers)'
        chmod 440 '\(tmpSudoers)'
        chown root:wheel '\(tmpSudoers)'
        /usr/sbin/visudo -c -f '\(tmpSudoers)'
        cp '\(tmpSudoers)' '\(sudoersPath)'
        chmod 440 '\(sudoersPath)'
        chown root:wheel '\(sudoersPath)'
        rm -f '\(tmpSudoers)'
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

        // Confirm the sudoers rule is actually accepted — sudo must work without a password now.
        guard _sudoWorks() else {
            return "Helper installed but sudo verification failed — please quit and reopen the app"
        }
        return nil
    }
}
