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
        NSApp.setActivationPolicy(.accessory)

        NotificationCenter.default.addObserver(
            self,
            selector: #selector(handleKillSwitchActivated(_:)),
            name: .vpnKillSwitchActivated,
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

    private func setupStatusItem() {
        statusItem = NSStatusBar.system.statusItem(withLength: NSStatusItem.variableLength)

        guard let btn = statusItem?.button else { return }
        btn.title = "❤"

        let menu = NSMenu()

        let di = NSMenuItem(title: L("menu.disconnect"), action: #selector(disconnectVPN), keyEquivalent: "")
        di.target = self
        menu.addItem(di)

        menu.addItem(.separator())

        let qi = NSMenuItem(title: L("menu.quit"), action: #selector(quitApp), keyEquivalent: "")
        qi.target = self
        menu.addItem(qi)

        statusItem?.menu = menu
    }

    private func refreshStatusTitle() {
        guard let btn = statusItem?.button else { return }
        if let speed = monitor.currentSpeed {
            btn.title = "❤ \(speed.menuBarText)"
        } else {
            btn.title = "❤"
        }
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
}

// MARK: - NSWindowDelegate

extension AppDelegate: NSWindowDelegate {
    func windowWillClose(_ notification: Notification) {
        guard !vpn.isConnected && !vpn.isConnecting else { return }
        NSApp.terminate(nil)
    }
}
