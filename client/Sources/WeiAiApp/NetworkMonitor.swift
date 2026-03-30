import Foundation
import Darwin
import SwiftUI

struct NetworkSpeed {
    let upload: Double
    let download: Double

    var menuBarText: String { "↑\(fmt(upload)) ↓\(fmt(download))" }

    private func fmt(_ bps: Double) -> String {
        switch bps {
        case 1_000_000...: return String(format: "%.1fM", bps / 1_000_000)
        case 1_000...:     return String(format: "%.0fK", bps / 1_000)
        default:           return String(format: "%.0fB", bps)
        }
    }
}

@MainActor
class NetworkMonitor: ObservableObject {
    @Published var currentSpeed: NetworkSpeed?

    private var timer: Timer?
    private var lastIn:  UInt64 = 0
    private var lastOut: UInt64 = 0
    private var lastAt:  Date   = Date()

    func start() {
        if let s = readStats() { lastIn = s.0; lastOut = s.1; lastAt = Date() }
        timer = Timer.scheduledTimer(withTimeInterval: 1.0, repeats: true) { [weak self] _ in
            Task { @MainActor in self?.tick() }
        }
    }

    func stop() {
        timer?.invalidate(); timer = nil
        currentSpeed = nil
    }

    private func tick() {
        guard let s = readStats() else { return }
        let dt = Date().timeIntervalSince(lastAt)
        guard dt > 0 else { return }

        let inD  = s.0 >= lastIn  ? s.0 - lastIn  : s.0 + (UInt64.max - lastIn)
        let outD = s.1 >= lastOut ? s.1 - lastOut : s.1 + (UInt64.max - lastOut)
        lastIn = s.0; lastOut = s.1; lastAt = Date()

        currentSpeed = NetworkSpeed(upload: Double(outD) / dt, download: Double(inD) / dt)
    }

    private func readStats() -> (UInt64, UInt64)? {
        let iface = primaryInterface() ?? "en0"
        var addrs: UnsafeMutablePointer<ifaddrs>?
        guard getifaddrs(&addrs) == 0 else { return nil }
        defer { freeifaddrs(addrs) }

        var ptr = addrs
        while let addr = ptr {
            if String(cString: addr.pointee.ifa_name) == iface,
               addr.pointee.ifa_addr?.pointee.sa_family == UInt8(AF_LINK),
               let raw = addr.pointee.ifa_data {
                let d = raw.assumingMemoryBound(to: if_data.self)
                return (UInt64(d.pointee.ifi_ibytes), UInt64(d.pointee.ifi_obytes))
            }
            ptr = addr.pointee.ifa_next
        }
        return nil
    }

    private func primaryInterface() -> String? {
        let p = Process(); let pipe = Pipe()
        p.executableURL = URL(fileURLWithPath: "/bin/sh")
        p.arguments = ["-c", "route -n get default 2>/dev/null | awk '/interface:/{print $2}'"]
        p.standardOutput = pipe
        try? p.run(); p.waitUntilExit()
        let out = String(data: pipe.fileHandleForReading.readDataToEndOfFile(), encoding: .utf8)?
            .trimmingCharacters(in: .whitespacesAndNewlines)
        return out?.isEmpty == false ? out : nil
    }
}
