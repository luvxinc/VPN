import AppKit
import Foundation

/// Handles in-app update: download with progress, extract, replace bundle, relaunch.
@MainActor
final class UpdateService: NSObject, ObservableObject {

    static let shared = UpdateService()

    enum State: Equatable {
        case idle
        case downloading(Double)  // 0.0 – 1.0
        case installing
        case failed(String)
    }

    @Published var state: State = .idle

    // Download uses standard CA validation — the update URL goes through Cloudflare CDN,
    // which serves its own certificate. Cert pinning is only needed for direct server auth.
    // Standard CA validation — update URL is served via Cloudflare CDN, not the pinned server.
    private lazy var downloadSession: URLSession = {
        URLSession(configuration: .default, delegate: self, delegateQueue: nil)
    }()

    // MARK: - Public

    func start(downloadURL: String) {
        guard let url = URL(string: downloadURL) else {
            state = .failed(L("error.networkGeneric"))
            return
        }
        state = .downloading(0)
        downloadSession.downloadTask(with: url).resume()
    }

    // MARK: - Install

    private func install(from tmp: URL) {
        state = .installing

        let zipDest = URL(fileURLWithPath: "/tmp/weiai-update.zip")
        let updateDir = URL(fileURLWithPath: "/tmp/weiai-update")

        // Move to final zip path (src is already stable; rename is near-instant).
        if tmp.path != zipDest.path {
            try? FileManager.default.removeItem(at: zipDest)
            do {
                try FileManager.default.moveItem(at: tmp, to: zipDest)
            } catch {
                state = .failed(L("update.error.invalidPackage"))
                return
            }
        }

        // Clean previous extraction
        try? FileManager.default.removeItem(at: updateDir)

        // Unzip
        let unzip = Process()
        unzip.executableURL = URL(fileURLWithPath: "/usr/bin/unzip")
        unzip.arguments = ["-o", zipDest.path, "-d", updateDir.path]
        unzip.standardOutput = Pipe()
        unzip.standardError = Pipe()
        try? unzip.run()
        unzip.waitUntilExit()

        // Find the .app inside the extracted folder
        let contents = (try? FileManager.default.contentsOfDirectory(atPath: updateDir.path)) ?? []
        guard let appName = contents.first(where: { $0.hasSuffix(".app") }) else {
            state = .failed(L("update.error.invalidPackage"))
            return
        }

        let newAppPath = updateDir.appendingPathComponent(appName).path
        let currentAppPath = Bundle.main.bundlePath

        // macOS App Translocation: if launched from Downloads without being moved,
        // macOS runs the app from a read-only temp path like
        // /private/var/folders/.../AppTranslocation/UUID/d/为爱鼓掌.app
        // Installing there leaves the original in Downloads untouched → infinite update loop.
        // Fix: detect translocation and install to ~/Applications/ instead.
        let installDir: String
        if currentAppPath.contains("/AppTranslocation/") {
            let appsDir = FileManager.default.homeDirectoryForCurrentUser
                .appendingPathComponent("Applications")
            try? FileManager.default.createDirectory(at: appsDir, withIntermediateDirectories: true)
            installDir = appsDir.path
        } else {
            installDir = URL(fileURLWithPath: currentAppPath).deletingLastPathComponent().path
        }
        let finalAppPath = installDir + "/" + appName

        // Detached installer script: runs after this process quits
        let script = """
        #!/bin/sh
        sleep 1
        pkill -9 -f WeiAiVPN 2>/dev/null || true
        sleep 0.5
        rm -rf "\(finalAppPath)"
        cp -Rf "\(newAppPath)" "\(installDir)/"
        xattr -dr com.apple.quarantine "\(finalAppPath)" 2>/dev/null || true
        open "\(finalAppPath)"
        rm -rf "\(updateDir.path)" "\(zipDest.path)"
        """
        let scriptPath = "/tmp/weiai-install.sh"
        do {
            try script.write(toFile: scriptPath, atomically: true, encoding: .utf8)
            try FileManager.default.setAttributes([.posixPermissions: 0o755], ofItemAtPath: scriptPath)
        } catch {
            state = .failed(L("update.error.invalidPackage"))
            return
        }

        let installer = Process()
        installer.executableURL = URL(fileURLWithPath: "/bin/sh")
        installer.arguments = [scriptPath]
        try? installer.run()
        // Do NOT waitUntilExit — the script runs detached after we quit

        NSApp.terminate(nil)
    }
}

// MARK: - URLSessionDownloadDelegate

extension UpdateService: URLSessionDownloadDelegate {

    nonisolated func urlSession(_ session: URLSession,
                                downloadTask: URLSessionDownloadTask,
                                didWriteData _: Int64,
                                totalBytesWritten: Int64,
                                totalBytesExpectedToWrite: Int64) {
        guard totalBytesExpectedToWrite > 0 else { return }
        let p = Double(totalBytesWritten) / Double(totalBytesExpectedToWrite)
        Task { @MainActor in self.state = .downloading(p) }
    }

    nonisolated func urlSession(_ session: URLSession,
                                downloadTask: URLSessionDownloadTask,
                                didFinishDownloadingTo location: URL) {
        // URLSession deletes `location` as soon as this method returns.
        // Copy to a stable path SYNCHRONOUSLY here, before returning.
        let stable = URL(fileURLWithPath: "/tmp/weiai-update-dl.zip")
        try? FileManager.default.removeItem(at: stable)
        do {
            try FileManager.default.copyItem(at: location, to: stable)
        } catch {
            Task { @MainActor in self.state = .failed(L("update.error.invalidPackage")) }
            return
        }
        Task { @MainActor in self.install(from: stable) }
    }

    nonisolated func urlSession(_ session: URLSession,
                                task: URLSessionTask,
                                didCompleteWithError error: Error?) {
        guard let error = error else { return }
        Task { @MainActor in self.state = .failed(error.localizedDescription) }
    }
}

// MARK: - URLSessionDelegate (standard CA validation for CDN downloads)

extension UpdateService: URLSessionDelegate {
    nonisolated func urlSession(_ session: URLSession,
                                didReceive challenge: URLAuthenticationChallenge,
                                completionHandler: @escaping (URLSession.AuthChallengeDisposition, URLCredential?) -> Void) {
        completionHandler(.performDefaultHandling, nil)
    }
}
