import Foundation
import AppKit

/// Kill switch: blocks all outbound network traffic when VPN drops unexpectedly.
/// Uses null routes (0.0.0.0/1 and 128.0.0.0/1 → 127.0.0.1) via root shell scripts.
/// When TUN is active its routes take precedence; when TUN dies these null routes
/// become the only match and silently drop all traffic.
@MainActor
final class KillSwitch {

    static let shared = KillSwitch()
    private let statePath = "/tmp/weiai_ks_active"

    // MARK: - Public API

    var isActive: Bool {
        // Primary check: state file presence
        if FileManager.default.fileExists(atPath: statePath) { return true }
        // Secondary check: null route exists in routing table
        return routeExists()
    }

    /// Block all outbound traffic. Requires admin (runs via osascript).
    func activate(serverIP: String) {
        let script = """
        #!/bin/sh
        /sbin/route add -net 0.0.0.0/1 127.0.0.1 2>/dev/null || true
        /sbin/route add -net 128.0.0.0/1 127.0.0.1 2>/dev/null || true
        touch \(statePath)
        """
        runAsAdmin(script: script, label: "weiai_ks_on")
    }

    /// Restore normal network access. Requires admin.
    func deactivate() {
        let script = """
        #!/bin/sh
        /sbin/route delete -net 0.0.0.0/1 127.0.0.1 2>/dev/null || true
        /sbin/route delete -net 128.0.0.0/1 127.0.0.1 2>/dev/null || true
        rm -f \(statePath)
        """
        runAsAdmin(script: script, label: "weiai_ks_off")
    }

    // MARK: - Startup check

    /// Call on app launch. If kill switch is still active from a previous crash,
    /// shows an alert letting the user reconnect or restore network.
    func startupCheck(onReconnect: @escaping () -> Void, onRestore: @escaping () -> Void) {
        guard isActive else { return }
        showKillSwitchAlert(
            title: "上次 VPN 异常断开",
            message: "检测到上次 VPN 异常退出，网络当前处于封锁状态，防止数据泄漏。",
            reconnectLabel: "重新连接",
            restoreLabel: "恢复网络并退出",
            onReconnect: onReconnect,
            onRestore: {
                self.deactivate()
                onRestore()
            }
        )
    }

    // MARK: - Alert

    func showActivatedAlert(onReconnect: @escaping () -> Void, onQuit: @escaping () -> Void) {
        // macOS user notification (visible even when app is in background)
        sendNotification(
            title: "VPN 已断开",
            body: "网络已封锁，防止数据泄漏。点击重新连接。"
        )
        showKillSwitchAlert(
            title: "VPN 已意外断开",
            message: "为防止数据泄漏，已自动封锁所有网络连接。",
            reconnectLabel: "重新连接",
            restoreLabel: "退出并恢复网络",
            onReconnect: onReconnect,
            onRestore: {
                self.deactivate()
                onQuit()
            }
        )
    }

    /// Shows a confirmation alert when user tries to quit while kill switch is active.
    /// Returns true if user confirmed quit (caller should deactivate + terminate).
    func confirmQuit() -> Bool {
        let alert = NSAlert()
        alert.messageText = "VPN 处于封锁状态"
        alert.informativeText = "退出后将自动恢复正常网络连接。确认退出？"
        alert.alertStyle = .warning
        alert.addButton(withTitle: "退出并恢复网络")
        alert.addButton(withTitle: "取消")
        return alert.runModal() == .alertFirstButtonReturn
    }

    // MARK: - Private helpers

    private func routeExists() -> Bool {
        let p = Process()
        let pipe = Pipe()
        p.executableURL = URL(fileURLWithPath: "/bin/sh")
        p.arguments = ["-c", "/sbin/route -n get 1.1.1.1 2>/dev/null | grep -q '127.0.0.1'"]
        p.standardOutput = pipe
        p.standardError = Pipe()
        try? p.run()
        p.waitUntilExit()
        return p.terminationStatus == 0
    }

    private func runAsAdmin(script: String, label: String) {
        let scriptPath = "/tmp/\(label).sh"
        do {
            try script.write(toFile: scriptPath, atomically: true, encoding: .utf8)
            try FileManager.default.setAttributes([.posixPermissions: 0o755], ofItemAtPath: scriptPath)
        } catch { return }

        let appleScript = "do shell script \"\(scriptPath)\" with administrator privileges"
        let task = Process()
        task.executableURL = URL(fileURLWithPath: "/usr/bin/osascript")
        task.arguments = ["-e", appleScript]
        task.standardOutput = Pipe()
        task.standardError = Pipe()
        try? task.run()
        task.waitUntilExit()
    }

    private func showKillSwitchAlert(title: String, message: String,
                                      reconnectLabel: String, restoreLabel: String,
                                      onReconnect: @escaping () -> Void,
                                      onRestore: @escaping () -> Void) {
        let alert = NSAlert()
        alert.messageText = title
        alert.informativeText = message
        alert.alertStyle = .critical
        alert.addButton(withTitle: reconnectLabel)
        alert.addButton(withTitle: restoreLabel)
        NSApp.activate(ignoringOtherApps: true)
        let response = alert.runModal()
        if response == .alertFirstButtonReturn {
            onReconnect()
        } else {
            onRestore()
        }
    }

    private func sendNotification(title: String, body: String) {
        let n = NSUserNotification()
        n.title = title
        n.informativeText = body
        n.soundName = NSUserNotificationDefaultSoundName
        NSUserNotificationCenter.default.deliver(n)
    }
}
