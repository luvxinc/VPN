import Foundation
import SwiftUI
import Darwin
import Network

// Result type used by ConnectView
enum VPNConnectResult {
    case success
    case deviceNotRegistered
    case updateRequired(downloadURL: String)
    case quotaExceeded(resetsAt: Date?)
    case error(String)
}

extension Notification.Name {
    static let vpnKillSwitchActivated = Notification.Name("vpnKillSwitchActivated")
    static let vpnQuotaExceeded = Notification.Name("vpnQuotaExceeded")
}

@MainActor
class VPNManager: ObservableObject {
    @Published var isConnected  = false
    @Published var isConnecting = false

    @Published var policy: UserPolicy = .unlimited
    @Published var latencyMs: Int?     // nil = not yet measured

    private let configPath = NSTemporaryDirectory() + "weiai_client.json"
    private let pidPath    = "/tmp/weiai_sb.pid"
    private var monitorTimer: Timer?
    private var statusTimer: Timer?
    private var latencyTimer: Timer?
    private var currentServerIP: String?
    private var currentServerPort: Int = 443
    private var currentWSIPs: [String] = []

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
                case .failure(.quotaExceeded(let d)):
                    self?.isConnecting = false
                    completion(.quotaExceeded(resetsAt: d))
                case .failure(let err):
                    self?.isConnecting = false
                    completion(.error(err.errorDescription))
                case .success(let (config, policy)):
                    self?.policy = policy
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
                case .failure(.quotaExceeded(let d)):
                    self?.isConnecting = false
                    completion(.quotaExceeded(resetsAt: d))
                case .failure(let err):
                    self?.isConnecting = false
                    completion(.error(err.errorDescription))
                case .success(let (config, policy)):
                    self?.policy = policy
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
        statusTimer?.invalidate()
        statusTimer = nil
        latencyTimer?.invalidate()
        latencyTimer = nil
        isConnected  = false
        isConnecting = false
        policy = .unlimited
        latencyMs = nil
        let pidPath  = self.pidPath
        let serverIP = self.currentServerIP ?? ""
        let wsIPs    = self.currentWSIPs
        currentServerIP = nil
        currentWSIPs = []
        KillSwitch.shared.deactivate()
        DispatchQueue.global(qos: .userInitiated).async {
            VPNManager.stopSingBoxStatic(pidPath: pidPath, serverIP: serverIP, wsIPs: wsIPs)
        }
    }

    // MARK: - Start sing-box

    private func startSingBox(config: VPNConfig, completion: @escaping (String?) -> Void) {
        writeClientConfig(config)
        currentServerIP = config.server
        currentServerPort = config.port

        let sbPath   = singBoxPath
        let cfgPath  = self.configPath
        let pidPath  = self.pidPath
        let serverIP = config.server
        let gateway  = VPNManager.defaultGateway() ?? "192.168.86.1"

        // Resolve WS domain IPs so we can add bypass routes for them.
        let wsIPs: [String]
        if let wsServer = config.wsServer {
            wsIPs = VPNManager.resolveHostIPv4(wsServer)
        } else {
            wsIPs = []
        }
        currentWSIPs = wsIPs

        DispatchQueue.global(qos: .userInitiated).async { [weak self] in
            guard let self = self else { return }

            let launchScript = "/tmp/weiai_launch.sh"
            // Add bypass routes for direct server IP and any WS CDN IPs
            // so sing-box's own connections don't loop through the TUN interface.
            var routeLines = "/sbin/route add -host \(serverIP) \(gateway) 2>/dev/null || true"
            for ip in wsIPs {
                routeLines += "\n/sbin/route add -host \(ip) \(gateway) 2>/dev/null || true"
            }
            let scriptContent = """
            #!/bin/sh
            /sbin/route delete -net 0.0.0.0/1 127.0.0.1 2>/dev/null || true
            /sbin/route delete -net 128.0.0.0/1 127.0.0.1 2>/dev/null || true
            rm -f /tmp/weiai_ks_active
            \(routeLines)
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

    nonisolated private static func stopSingBoxStatic(pidPath: String, serverIP: String, wsIPs: [String] = []) {
        let stopScript = "/tmp/weiai_stop.sh"
        var routeDeletes = "/sbin/route delete -host \(serverIP) 2>/dev/null || true"
        for ip in wsIPs {
            routeDeletes += "\n/sbin/route delete -host \(ip) 2>/dev/null || true"
        }
        let content = """
        #!/bin/sh
        PID=$(cat "\(pidPath)" 2>/dev/null)
        [ -n "$PID" ] && kill "$PID" 2>/dev/null
        \(routeDeletes)
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

        // Poll /status every 30 seconds
        statusTimer = Timer.scheduledTimer(withTimeInterval: 30.0, repeats: true) { [weak self] _ in
            Task { @MainActor [weak self] in self?.pollStatus() }
        }

        // Measure latency every 15 seconds
        measureLatency()
        latencyTimer = Timer.scheduledTimer(withTimeInterval: 15.0, repeats: true) { [weak self] _ in
            Task { @MainActor [weak self] in self?.measureLatency() }
        }
    }

    private func pollStatus() {
        AuthService.shared.fetchStatus { [weak self] status in
            guard let self = self, let status = status else { return }
            Task { @MainActor in
                self.policy = UserPolicy(
                    speedLimitUpKbps:   status.speedLimitUpKbps,
                    speedLimitDownKbps: status.speedLimitDownKbps,
                    quotaBytes:         status.quotaBytes,
                    quotaPeriod:        status.quotaPeriod,
                    quotaUsedBytes:     status.quotaUsedBytes,
                    quotaResetsAt:      status.quotaResetsAt,
                    quotaExceeded:      status.quotaExceeded
                )
                if status.quotaExceeded && self.isConnected {
                    self.disconnect()
                    NotificationCenter.default.post(
                        name: .vpnQuotaExceeded,
                        object: status.quotaResetsAt
                    )
                }
            }
        }
    }

    private func measureLatency() {
        guard let serverIP = currentServerIP else { return }
        let port = currentServerPort
        let start = Date()
        let conn = NWConnection(
            host: NWEndpoint.Host(serverIP),
            port: NWEndpoint.Port(integerLiteral: UInt16(port)),
            using: .tcp
        )
        conn.stateUpdateHandler = { [weak self] state in
            switch state {
            case .ready:
                let ms = Int(Date().timeIntervalSince(start) * 1000)
                conn.cancel()
                Task { @MainActor [weak self] in self?.latencyMs = ms }
            case .failed:
                conn.cancel()
            default: break
            }
        }
        conn.start(queue: .global(qos: .utility))
        // Cancel after 5s if no response
        DispatchQueue.global(qos: .utility).asyncAfter(deadline: .now() + 5) {
            conn.cancel()
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
        let realityOutbound: [String: Any] = [
            "type":        "vless",
            "tag":         "reality-direct",
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
        ]

        var outbounds: [[String: Any]] = [realityOutbound]
        var finalOutbound = "reality-direct"

        // If a CDN WebSocket fallback is configured, add a second outbound and use
        // urltest to automatically select the fastest working path.
        if let wsServer = vpn.wsServer,
           let wsPort   = vpn.wsPort,
           let wsPath   = vpn.wsPath {
            let wsOutbound: [String: Any] = [
                "type":        "vless",
                "tag":         "ws-cdn",
                "server":      wsServer,
                "server_port": wsPort,
                "uuid":        vpn.uuid,
                "tls": [
                    "enabled":     true,
                    "server_name": wsServer,
                ],
                "transport": [
                    "type": "ws",
                    "path": wsPath,
                ]
            ]
            let fallbackOutbound: [String: Any] = [
                "type":      "fallback",
                "tag":       "proxy",
                "outbounds": ["reality-direct", "ws-cdn"],
                "url":       "https://www.gstatic.com/generate_204",
                "interval":  "3m",
            ]
            outbounds.append(wsOutbound)
            outbounds.append(fallbackOutbound)
            finalOutbound = "proxy"
        }

        outbounds.append(["type": "direct", "tag": "direct"])

        let config: [String: Any] = [
            "log": ["level": "warn"],
            "inbounds": [[
                "type":         "tun",
                "tag":          "tun-in",
                "address":      ["172.19.0.1/30"],
                "auto_route":   true,
                "strict_route": true,
            ]],
            "outbounds": outbounds,
            "route": [
                "rules": [["action": "sniff"]],
                "final": finalOutbound,
            ]
        ]
        let data = try? JSONSerialization.data(withJSONObject: config, options: .prettyPrinted)
        try? data?.write(to: URL(fileURLWithPath: configPath))
    }

    // MARK: - DNS helper

    /// Resolves a hostname to its IPv4 addresses using getaddrinfo.
    /// Used to add bypass routes for CDN IPs before sing-box starts.
    nonisolated static func resolveHostIPv4(_ hostname: String) -> [String] {
        var hints = addrinfo()
        hints.ai_family = AF_INET
        hints.ai_socktype = SOCK_STREAM
        var result: UnsafeMutablePointer<addrinfo>? = nil
        guard getaddrinfo(hostname, nil, &hints, &result) == 0, let head = result else { return [] }
        defer { freeaddrinfo(head) }
        var ips: [String] = []
        var ptr: UnsafeMutablePointer<addrinfo>? = head
        while let node = ptr {
            var host = [CChar](repeating: 0, count: Int(NI_MAXHOST))
            if getnameinfo(node.pointee.ai_addr, node.pointee.ai_addrlen,
                           &host, socklen_t(host.count), nil, 0, NI_NUMERICHOST) == 0 {
                let ip = String(cString: host)
                if !ips.contains(ip) { ips.append(ip) }
            }
            ptr = node.pointee.ai_next
        }
        return ips
    }
}
