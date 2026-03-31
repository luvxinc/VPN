import Foundation
import AppKit

/// Kill switch: blocks all outbound network traffic when VPN drops unexpectedly.
/// Uses null routes (0.0.0.0/1 and 128.0.0.0/1 → 127.0.0.1) via root shell scripts.
@MainActor
final class KillSwitch {

    static let shared = KillSwitch()
    private let statePath = "/tmp/weiai_ks_active"

    // MARK: - Public API

    var isActive: Bool {
        if FileManager.default.fileExists(atPath: statePath) { return true }
        return routeExists()
    }

    func activate(serverIP: String) {
        let script = """
        #!/bin/sh
        /sbin/route add -net 0.0.0.0/1 127.0.0.1 2>/dev/null || true
        /sbin/route add -net 128.0.0.0/1 127.0.0.1 2>/dev/null || true
        touch \(statePath)
        """
        runAsAdmin(script: script, label: "weiai_ks_on")
    }

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

    func startupCheck(onReconnect: @escaping () -> Void, onRestore: @escaping () -> Void) {
        // If the state file exists but routes were already cleared (e.g. after reboot),
        // the kill switch isn't actually blocking anything — silently clean up the stale file.
        if FileManager.default.fileExists(atPath: statePath) && !routeExists() {
            try? FileManager.default.removeItem(atPath: statePath)
            return
        }
        guard isActive else { return }
        showKillSwitchAlert(
            title:          L("ks.startup.title"),
            message:        L("ks.startup.message"),
            reconnectLabel: L("ks.reconnect"),
            restoreLabel:   L("ks.restoreAndQuit"),
            onReconnect:    onReconnect,
            onRestore: {
                self.deactivate()
                onRestore()
            }
        )
    }

    // MARK: - Alert

    func showActivatedAlert(onReconnect: @escaping () -> Void, onQuit: @escaping () -> Void) {
        sendNotification(
            title: L("ks.activated.notification.title"),
            body:  L("ks.activated.notification.body")
        )
        showKillSwitchAlert(
            title:          L("ks.activated.title"),
            message:        L("ks.activated.message"),
            reconnectLabel: L("ks.reconnect"),
            restoreLabel:   L("ks.restoreAndQuit"),
            onReconnect:    onReconnect,
            onRestore: {
                self.deactivate()
                onQuit()
            }
        )
    }

    func confirmQuit() -> Bool {
        let alert = NSAlert()
        alert.messageText     = L("ks.quit.title")
        alert.informativeText = L("ks.quit.message")
        alert.alertStyle      = .warning
        alert.addButton(withTitle: L("ks.quit.confirm"))
        alert.addButton(withTitle: L("ks.quit.cancel"))
        return alert.runModal() == .alertFirstButtonReturn
    }

    // MARK: - Private helpers

    private func routeExists() -> Bool {
        let p = Process(); let pipe = Pipe()
        p.executableURL = URL(fileURLWithPath: "/bin/sh")
        p.arguments = ["-c", "/sbin/route -n get 1.1.1.1 2>/dev/null | grep -q '127.0.0.1'"]
        p.standardOutput = pipe
        p.standardError  = Pipe()
        try? p.run(); p.waitUntilExit()
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
        task.arguments     = ["-e", appleScript]
        task.standardOutput = Pipe()
        task.standardError  = Pipe()
        try? task.run()
        task.waitUntilExit()
    }

    private func showKillSwitchAlert(title: String, message: String,
                                      reconnectLabel: String, restoreLabel: String,
                                      onReconnect: @escaping () -> Void,
                                      onRestore: @escaping () -> Void) {
        let alert = NSAlert()
        alert.messageText     = title
        alert.informativeText = message
        alert.alertStyle      = .critical
        alert.addButton(withTitle: reconnectLabel)
        alert.addButton(withTitle: restoreLabel)
        NSApp.activate(ignoringOtherApps: true)
        if alert.runModal() == .alertFirstButtonReturn {
            onReconnect()
        } else {
            onRestore()
        }
    }

    private func sendNotification(title: String, body: String) {
        let n = NSUserNotification()
        n.title           = title
        n.informativeText = body
        n.soundName       = NSUserNotificationDefaultSoundName
        NSUserNotificationCenter.default.deliver(n)
    }
}
