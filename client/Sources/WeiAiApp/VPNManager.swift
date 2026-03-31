import Foundation
import SwiftUI
import Darwin

// Result type used by ConnectView
enum VPNConnectResult {
    case success
    case deviceNotRegistered
    case updateRequired(downloadURL: String)
    case error(String)
}

extension Notification.Name {
    static let vpnKillSwitchActivated = Notification.Name("vpnKillSwitchActivated")
}

@MainActor
class VPNManager: ObservableObject {
    @Published var isConnected  = false
    @Published var isConnecting = false

    private let configPath = NSTemporaryDirectory() + "weiai_client.json"
    private let pidPath    = "/tmp/weiai_sb.pid"
    private var monitorTimer: Timer?
    private var currentServerIP: String?

    private var singBoxPath: String {
        let bundlePath = Bundle.main.bundlePath + "/Contents/Resources/sing-box"
        if FileManager.default.fileExists(atPath: bundlePath) { return bundlePath }
        if let p = Bundle.main.path(forResource: "sing-box", ofType: nil) { return p }
        return bundlePath
    }

    // MARK: - Connect

    func connect(username: String, password: String,
                 completion: @escaping (VPNConnectResult) -> Void) {
        isConnecting = true
        AuthService.shared.connect(username: username, password: password) { [weak self] result in
            Task { @MainActor in
                switch result {
                case .failure(.deviceNotRegistered):
                    self?.isConnecting = false
                    completion(.deviceNotRegistered)
                case .failure(.updateRequired(let url)):
                    self?.isConnecting = false
                    completion(.updateRequired(downloadURL: url))
                case .failure(let err):
                    self?.isConnecting = false
                    completion(.error(err.errorDescription))
                case .success(let config):
                    self?.startSingBox(config: config) { errMsg in
                        if let msg = errMsg { completion(.error(msg)) }
                        else { completion(.success) }
                    }
                }
            }
        }
    }

    func verifyDevice(username: String, password: String, code: String,
                      completion: @escaping (VPNConnectResult) -> Void) {
        isConnecting = true
        AuthService.shared.verifyDevice(username: username, password: password, code: code) { [weak self] result in
            Task { @MainActor in
                switch result {
                case .failure(.deviceNotRegistered):
                    self?.isConnecting = false
                    completion(.deviceNotRegistered)
                case .failure(.updateRequired(let url)):
                    self?.isConnecting = false
                    completion(.updateRequired(downloadURL: url))
                case .failure(let err):
                    self?.isConnecting = false
                    completion(.error(err.errorDescription))
                case .success(let config):
                    self?.startSingBox(config: config) { errMsg in
                        if let msg = errMsg { completion(.error(msg)) }
                        else { completion(.success) }
                    }
                }
            }
        }
    }

    // MARK: - Disconnect

    func disconnect() {
        AuthService.shared.disconnect()
        monitorTimer?.invalidate()
        monitorTimer = nil
        isConnected  = false
        isConnecting = false
        let pidPath   = self.pidPath
        let serverIP  = self.currentServerIP ?? ""
        currentServerIP = nil
        KillSwitch.shared.deactivate()
        DispatchQueue.global(qos: .userInitiated).async {
            VPNManager.stopSingBoxStatic(pidPath: pidPath, serverIP: serverIP)
        }
    }

    // MARK: - Start sing-box

    private func startSingBox(config: VPNConfig, completion: @escaping (String?) -> Void) {
        writeClientConfig(config)
        currentServerIP = config.server

        let sbPath   = singBoxPath
        let cfgPath  = self.configPath
        let pidPath  = self.pidPath
        let serverIP = config.server
        let gateway  = VPNManager.defaultGateway() ?? "192.168.86.1"

        DispatchQueue.global(qos: .userInitiated).async { [weak self] in
            guard let self = self else { return }

            let launchScript = "/tmp/weiai_launch.sh"
            let scriptContent = """
            #!/bin/sh
            /sbin/route add -host \(serverIP) \(gateway) 2>/dev/null || true
            "\(sbPath)" run -c "\(cfgPath)" > /tmp/weiai_sb.log 2>&1 &
            echo $! > "\(pidPath)"
            """
            do {
                try scriptContent.write(toFile: launchScript, atomically: true, encoding: .utf8)
                try FileManager.default.setAttributes(
                    [.posixPermissions: 0o755], ofItemAtPath: launchScript)
            } catch {
                Task { @MainActor in
                    self.isConnecting = false
                    completion("\(L("error.launchScriptFailed")): \(error.localizedDescription)")
                }
                return
            }

            let appleScript = "do shell script \"\(launchScript)\" with administrator privileges"
            let task = Process()
            task.executableURL  = URL(fileURLWithPath: "/usr/bin/osascript")
            task.arguments      = ["-e", appleScript]
            task.standardOutput = Pipe()
            task.standardError  = Pipe()

            do { try task.run(); task.waitUntilExit() }
            catch {
                Task { @MainActor in
                    self.isConnecting = false
                    completion("\(L("error.osascriptFailed")): \(error.localizedDescription)")
                }
                return
            }

            guard task.terminationStatus == 0 else {
                Task { @MainActor in
                    self.isConnecting = false
                    completion(L("error.authCancelled"))
                }
                return
            }

            Thread.sleep(forTimeInterval: 2.0)

            Task { @MainActor in
                self.isConnected  = true
                self.isConnecting = false
                self.startMonitoring()
                completion(nil)
            }
        }
    }

    // MARK: - Stop sing-box

    nonisolated private static func stopSingBoxStatic(pidPath: String, serverIP: String) {
        let stopScript = "/tmp/weiai_stop.sh"
        let content = """
        #!/bin/sh
        PID=$(cat "\(pidPath)" 2>/dev/null)
        [ -n "$PID" ] && kill "$PID" 2>/dev/null
        /sbin/route delete -host \(serverIP) 2>/dev/null || true
        rm -f "\(pidPath)"
        """
        try? content.write(toFile: stopScript, atomically: true, encoding: .utf8)
        try? FileManager.default.setAttributes(
            [.posixPermissions: 0o755], ofItemAtPath: stopScript)

        let appleScript = "do shell script \"\(stopScript)\" with administrator privileges"
        let task = Process()
        task.executableURL  = URL(fileURLWithPath: "/usr/bin/osascript")
        task.arguments      = ["-e", appleScript]
        task.standardOutput = Pipe()
        task.standardError  = Pipe()
        try? task.run()
        task.waitUntilExit()
    }

    // MARK: - Monitoring

    private func startMonitoring() {
        monitorTimer = Timer.scheduledTimer(withTimeInterval: 5.0, repeats: true) { [weak self] _ in
            Task { @MainActor [weak self] in
                guard let self = self, self.isConnected else { return }
                if !self.isSingBoxRunning() {
                    let serverIP = self.currentServerIP ?? ""
                    self.isConnected  = false
                    self.isConnecting = false
                    self.monitorTimer?.invalidate()
                    self.monitorTimer = nil
                    KillSwitch.shared.activate(serverIP: serverIP)
                    NotificationCenter.default.post(
                        name: .vpnKillSwitchActivated,
                        object: serverIP
                    )
                }
            }
        }
    }

    // MARK: - Helpers

    nonisolated static func defaultGateway() -> String? {
        let p = Process(); let pipe = Pipe()
        p.executableURL = URL(fileURLWithPath: "/bin/sh")
        p.arguments = ["-c", "route -n get default 2>/dev/null | awk '/gateway:/{print $2}'"]
        p.standardOutput = pipe
        try? p.run(); p.waitUntilExit()
        let out = String(data: pipe.fileHandleForReading.readDataToEndOfFile(), encoding: .utf8)?
            .trimmingCharacters(in: .whitespacesAndNewlines)
        return out?.isEmpty == false ? out : nil
    }

    private func isSingBoxRunning() -> Bool {
        guard let pidStr = try? String(contentsOfFile: pidPath, encoding: .utf8),
              let pid = Int32(pidStr.trimmingCharacters(in: .whitespacesAndNewlines)),
              pid > 0 else { return false }
        let result = Darwin.kill(pid, 0)
        return result == 0 || (result == -1 && errno == EPERM)
    }

    // MARK: - Client config

    private func writeClientConfig(_ vpn: VPNConfig) {
        let config: [String: Any] = [
            "log": ["level": "warn"],
            "inbounds": [[
                "type":         "tun",
                "tag":          "tun-in",
                "address":      ["172.19.0.1/30"],
                "auto_route":   true,
                "strict_route": true,
            ]],
            "outbounds": [
                [
                    "type":        "vless",
                    "tag":         "proxy",
                    "server":      vpn.server,
                    "server_port": vpn.port,
                    "uuid":        vpn.uuid,
                    "flow":        "xtls-rprx-vision",
                    "tls": [
                        "enabled":     true,
                        "server_name": vpn.serverName,
                        "utls": ["enabled": true, "fingerprint": "chrome"],
                        "reality": [
                            "enabled":    true,
                            "public_key": vpn.publicKey,
                            "short_id":   vpn.shortId,
                        ]
                    ]
                ],
                ["type": "direct", "tag": "direct"],
            ],
            "route": [
                "rules": [["action": "sniff"]],
                "final": "proxy",
            ]
        ]
        let data = try? JSONSerialization.data(withJSONObject: config, options: .prettyPrinted)
        try? data?.write(to: URL(fileURLWithPath: configPath))
    }
}
