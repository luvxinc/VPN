import AppKit
import SwiftUI

@MainActor
class AppDelegate: NSObject, NSApplicationDelegate {

    private var statusItem: NSStatusItem?
    private var connectWindow: NSWindow?
    private var refreshTimer: Timer?

    let vpn     = VPNManager()
    let monitor = NetworkMonitor()

    // MARK: - Lifecycle

    func applicationDidFinishLaunching(_ notification: Notification) {
        // macOS App Translocation: if the user ran us from Downloads, macOS puts the app
        // in a read-only temp sandbox like /private/var/.../AppTranslocation/UUID/d/App.app
        // Auto-updaters that used the old install script would have placed the new binary
        // inside that sandbox, leaving the user's original Downloads copy untouched.
        // Detect this and silently self-relocate to ~/Applications/ then relaunch,
        // so the user always ends up with the app installed in a stable, persistent location.
        if Bundle.main.bundlePath.contains("/AppTranslocation/") {
            handleTranslocation()
            return  // do not continue startup; we will relaunch
        }

        NSApp.setActivationPolicy(.accessory)

        NotificationCenter.default.addObserver(
            self,
            selector: #selector(handleKillSwitchActivated(_:)),
            name: .vpnKillSwitchActivated,
            object: nil
        )
        NotificationCenter.default.addObserver(
            self,
            selector: #selector(handleQuotaExceeded(_:)),
            name: .vpnQuotaExceeded,
            object: nil
        )

        // If kill switch was active from a previous crash, handle before showing UI
        var killSwitchHandled = false
        KillSwitch.shared.startupCheck(
            onReconnect: { [weak self] in
                killSwitchHandled = true
                self?.showConnectWindow()
            },
            onRestore: {
                killSwitchHandled = true
                NSApp.terminate(nil)
            }
        )
        if !killSwitchHandled {
            showConnectWindow()
        }
    }

    func applicationShouldTerminateAfterLastWindowClosed(_ sender: NSApplication) -> Bool {
        false
    }

    // MARK: - Connect window

    func showConnectWindow() {
        let content = ConnectView(vpn: vpn) { [weak self] in
            self?.switchToMenuBar()
        }

        let win = NSWindow(
            contentRect: NSRect(x: 0, y: 0, width: 300, height: 380),
            styleMask:   [.titled, .closable],
            backing:     .buffered,
            defer:       false
        )
        win.title                = L("app.name")
        win.contentView          = NSHostingView(rootView: content)
        win.isReleasedWhenClosed = false
        win.delegate             = self
        win.center()
        win.makeKeyAndOrderFront(nil)
        NSApp.activate(ignoringOtherApps: true)
        connectWindow = win
    }

    // MARK: - Switch to menu bar

    private func switchToMenuBar() {
        connectWindow?.orderOut(nil)
        connectWindow = nil

        monitor.start()
        setupStatusItem()

        refreshTimer = Timer.scheduledTimer(withTimeInterval: 1.0, repeats: true) { [weak self] _ in
            Task { @MainActor [weak self] in self?.refreshStatusTitle() }
        }
    }

    // MARK: - Kill switch notification

    @objc private func handleKillSwitchActivated(_ notification: Notification) {
        KillSwitch.shared.showActivatedAlert(
            onReconnect: { [weak self] in
                self?.switchToConnectWindow()
            },
            onQuit: {
                NSApp.terminate(nil)
            }
        )
    }

    @objc private func handleQuotaExceeded(_ notification: Notification) {
        let resetsAt = notification.object as? Date
        let alert = NSAlert()
        alert.messageText     = L("ks.quota.title")
        alert.informativeText = quotaExceededMessage(resetsAt: resetsAt)
        alert.alertStyle      = .warning
        alert.addButton(withTitle: L("ks.quit.confirm"))
        NSApp.activate(ignoringOtherApps: true)
        alert.runModal()
        switchToConnectWindow()
    }

    private func quotaExceededMessage(resetsAt: Date?) -> String {
        guard let d = resetsAt else { return L("ks.quota.message") }
        let f = RelativeDateTimeFormatter()
        f.unitsStyle = .full
        return L("ks.quota.message") + "\n" + L("quota.resetsIn") + " " + f.localizedString(for: d, relativeTo: Date())
    }

    private func switchToConnectWindow() {
        refreshTimer?.invalidate()
        refreshTimer = nil
        monitor.stop()
        if let item = statusItem {
            NSStatusBar.system.removeStatusItem(item)
            statusItem = nil
        }
        showConnectWindow()
    }

    // MARK: - Menu bar

    // Returns an SF Symbol image sized for menu items (16pt, template rendering).
    private func menuIcon(_ systemName: String) -> NSImage? {
        let img = NSImage(systemSymbolName: systemName, accessibilityDescription: nil)
        img?.isTemplate = true
        return img
    }

    private func setupStatusItem() {
        statusItem = NSStatusBar.system.statusItem(withLength: NSStatusItem.variableLength)
        guard let btn = statusItem?.button else { return }
        // SF Symbol heart — template image adapts to light/dark menu bar
        if let img = NSImage(systemSymbolName: "heart.fill", accessibilityDescription: "VPN") {
            img.isTemplate = true
            btn.image = img
        }
        btn.title = ""
        rebuildMenu()
    }

    private func refreshStatusTitle() {
        guard let btn = statusItem?.button else { return }
        var parts: [String] = []
        if let speed = monitor.currentSpeed {
            parts.append(speed.menuBarText)
        }
        // Quota usage (e.g. "345G/1024G")
        let p = vpn.policy
        if let quotaBytes = p.quotaBytes {
            let usedGB  = Double(p.quotaUsedBytes) / 1_073_741_824
            let totalGB = Double(quotaBytes) / 1_073_741_824
            if totalGB >= 1 {
                parts.append(String(format: "%.0fG/%.0fG", usedGB, totalGB))
            } else {
                parts.append(String(format: "%.1fG/%.1fG", usedGB, totalGB))
            }
        }
        // Title is text only; image (heart) is always visible on the left
        btn.title = parts.isEmpty ? "" : " " + parts.joined(separator: "  ")

        // Rebuild menu to show fresh latency / quota info
        rebuildMenu()
    }

    private func rebuildMenu() {
        guard let item = statusItem else { return }
        let menu = NSMenu()

        // Latency
        if let ms = vpn.latencyMs {
            let lat = NSMenuItem(title: "\(ms) ms", action: nil, keyEquivalent: "")
            lat.image = menuIcon("bolt.fill")
            lat.isEnabled = false
            menu.addItem(lat)
        }

        // Quota info
        let p = vpn.policy
        if let quotaBytes = p.quotaBytes, let period = p.quotaPeriod {
            let usedGB  = String(format: "%.1f", Double(p.quotaUsedBytes) / 1_073_741_824)
            let totalGB = String(format: "%.1f", Double(quotaBytes) / 1_073_741_824)
            let qi = NSMenuItem(title: "\(usedGB)G / \(totalGB)G  (\(period))", action: nil, keyEquivalent: "")
            qi.image = menuIcon("chart.bar.fill")
            qi.isEnabled = false
            menu.addItem(qi)
            if let resets = p.quotaResetsAt {
                let f = RelativeDateTimeFormatter()
                f.unitsStyle = .abbreviated
                let ri = NSMenuItem(title: f.localizedString(for: resets, relativeTo: Date()), action: nil, keyEquivalent: "")
                ri.image = menuIcon("arrow.clockwise")
                ri.isEnabled = false
                menu.addItem(ri)
            }
            menu.addItem(.separator())
        } else if vpn.latencyMs != nil {
            menu.addItem(.separator())
        }

        // Speed limits
        if p.speedLimitUpKbps != nil || p.speedLimitDownKbps != nil {
            var speedParts: [String] = []
            if let up   = p.speedLimitUpKbps   { speedParts.append("\u{2191}\(up/1000)M") }  // ↑
            if let down = p.speedLimitDownKbps { speedParts.append("\u{2193}\(down/1000)M") } // ↓
            let si = NSMenuItem(title: speedParts.joined(separator: "  "), action: nil, keyEquivalent: "")
            si.image = menuIcon("speedometer")
            si.isEnabled = false
            menu.addItem(si)
            menu.addItem(.separator())
        }

        let di = NSMenuItem(title: L("menu.disconnect"), action: #selector(disconnectVPN), keyEquivalent: "")
        di.image = menuIcon("xmark.circle")
        di.target = self
        menu.addItem(di)
        menu.addItem(.separator())
        let qi2 = NSMenuItem(title: L("menu.quit"), action: #selector(quitApp), keyEquivalent: "q")
        qi2.image = menuIcon("power")
        qi2.target = self
        menu.addItem(qi2)
        item.menu = menu
    }

    // MARK: - Disconnect / Quit

    @objc private func disconnectVPN() {
        vpn.disconnect()
        monitor.stop()
        refreshTimer?.invalidate()
        refreshTimer = nil

        if let item = statusItem {
            NSStatusBar.system.removeStatusItem(item)
            statusItem = nil
        }

        showConnectWindow()
    }

    @objc private func quitApp() {
        if KillSwitch.shared.isActive {
            guard KillSwitch.shared.confirmQuit() else { return }
            KillSwitch.shared.deactivate()
        }
        if vpn.isConnected { vpn.disconnect() }
        NSApp.terminate(nil)
    }

    // MARK: - Terminate

    func applicationWillTerminate(_ notification: Notification) {
        // Safety net: always release kill switch on any exit path.
        // Prevents network staying locked if the app crashes or is force-quit.
        KillSwitch.shared.deactivate()
    }
}

// MARK: - Translocation self-repair

extension AppDelegate {
    /// Silently moves the app out of the macOS App Translocation sandbox into
    /// ~/Applications/ and relaunches from there, so the installation persists.
    private func handleTranslocation() {
        let fm = FileManager.default
        let appName = URL(fileURLWithPath: Bundle.main.bundlePath).lastPathComponent
        let appsDir = fm.homeDirectoryForCurrentUser.appendingPathComponent("Applications")
        let dest    = appsDir.appendingPathComponent(appName)

        do {
            try fm.createDirectory(at: appsDir, withIntermediateDirectories: true)
            if fm.fileExists(atPath: dest.path) {
                try fm.removeItem(at: dest)
            }
            try fm.copyItem(atPath: Bundle.main.bundlePath, toPath: dest.path)
        } catch {
            // If the copy fails for any reason, skip self-relocation and continue normally.
            continueStartup()
            return
        }

        // Strip quarantine so the relocated app doesn't trigger Gatekeeper again.
        let xattr = Process()
        xattr.executableURL = URL(fileURLWithPath: "/usr/bin/xattr")
        xattr.arguments     = ["-dr", "com.apple.quarantine", dest.path]
        try? xattr.run()
        xattr.waitUntilExit()

        // Relaunch from the stable path, then quit the translocated instance.
        let ws = NSWorkspace.shared
        let cfg = NSWorkspace.OpenConfiguration()
        cfg.createsNewApplicationInstance = true
        ws.openApplication(at: dest, configuration: cfg)
        NSApp.terminate(nil)
    }

    private func continueStartup() {
        NSApp.setActivationPolicy(.accessory)

        NotificationCenter.default.addObserver(
            self,
            selector: #selector(handleKillSwitchActivated(_:)),
            name: .vpnKillSwitchActivated,
            object: nil
        )
        NotificationCenter.default.addObserver(
            self,
            selector: #selector(handleQuotaExceeded(_:)),
            name: .vpnQuotaExceeded,
            object: nil
        )

        var killSwitchHandled = false
        KillSwitch.shared.startupCheck(
            onReconnect: { [weak self] in
                killSwitchHandled = true
                self?.showConnectWindow()
            },
            onRestore: {
                killSwitchHandled = true
                NSApp.terminate(nil)
            }
        )
        if !killSwitchHandled {
            showConnectWindow()
        }
    }
}

// MARK: - NSWindowDelegate

extension AppDelegate: NSWindowDelegate {
    func windowWillClose(_ notification: Notification) {
        guard !vpn.isConnected && !vpn.isConnecting else { return }
        // Always release kill switch before quitting — user closing the window
        // must never leave their network locked.
        KillSwitch.shared.deactivate()
        NSApp.terminate(nil)
    }
}
