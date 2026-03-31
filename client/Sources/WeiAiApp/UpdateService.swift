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

        let fm = FileManager.default
        let zipDest = URL(fileURLWithPath: "/tmp/weiai-update.zip")
        let updateDir = URL(fileURLWithPath: "/tmp/weiai-update")

        // Move to stable zip path.
        if tmp.path != zipDest.path {
            try? fm.removeItem(at: zipDest)
            do {
                try fm.moveItem(at: tmp, to: zipDest)
            } catch {
                state = .failed(L("update.error.invalidPackage"))
                return
            }
        }

        // Clean previous extraction and unzip.
        try? fm.removeItem(at: updateDir)
        let unzip = Process()
        unzip.executableURL = URL(fileURLWithPath: "/usr/bin/unzip")
        unzip.arguments = ["-o", zipDest.path, "-d", updateDir.path]
        unzip.standardOutput = Pipe()
        unzip.standardError = Pipe()
        try? unzip.run()
        unzip.waitUntilExit()

        // Find the .app inside the extracted folder (search one level deep).
        let contents = (try? fm.contentsOfDirectory(atPath: updateDir.path)) ?? []
        guard let appName = contents.first(where: { $0.hasSuffix(".app") }) else {
            state = .failed(L("update.error.invalidPackage"))
            return
        }
        let newAppURL = updateDir.appendingPathComponent(appName)

        // Choose install destination.
        // Priority: wherever the current app lives (if writable and not translocated),
        // otherwise ~/Applications/. This avoids AppTranslocation write failures and
        // the dirname(translocated_path) bug that caused the infinite update loop.
        let currentPath = Bundle.main.bundlePath
        let isTranslocated = currentPath.contains("/AppTranslocation/")
        let currentDir = URL(fileURLWithPath: currentPath).deletingLastPathComponent()

        let installDir: URL
        if !isTranslocated && fm.isWritableFile(atPath: currentDir.path) {
            installDir = currentDir
        } else {
            installDir = fm.homeDirectoryForCurrentUser.appendingPathComponent("Applications")
            try? fm.createDirectory(at: installDir, withIntermediateDirectories: true)
        }
        let finalAppURL = installDir.appendingPathComponent(appName)

        // Copy new app directly from Swift — no shell script, no silent failures.
        do {
            if fm.fileExists(atPath: finalAppURL.path) {
                try fm.removeItem(at: finalAppURL)
            }
            try fm.copyItem(at: newAppURL, to: finalAppURL)
        } catch {
            state = .failed(L("update.error.invalidPackage"))
            return
        }

        // Strip quarantine so macOS doesn't sandbox the newly installed app.
        let xattr = Process()
        xattr.executableURL = URL(fileURLWithPath: "/usr/bin/xattr")
        xattr.arguments = ["-dr", "com.apple.quarantine", finalAppURL.path]
        try? xattr.run()
        xattr.waitUntilExit()

        // Relaunch via a tiny detached script so the open happens after we quit.
        let launchScript = """
        #!/bin/sh
        sleep 0.8
        open "\(finalAppURL.path)"
        rm -rf "\(updateDir.path)" "\(zipDest.path)" /tmp/weiai-install.sh
        """
        let scriptPath = "/tmp/weiai-install.sh"
        do {
            try launchScript.write(toFile: scriptPath, atomically: true, encoding: .utf8)
            try fm.setAttributes([.posixPermissions: 0o755], ofItemAtPath: scriptPath)
        } catch {
            // Even if script write fails, the app is already copied — just open it directly.
            NSWorkspace.shared.openApplication(at: finalAppURL,
                                               configuration: NSWorkspace.OpenConfiguration())
            NSApp.terminate(nil)
            return
        }

        let launcher = Process()
        launcher.executableURL = URL(fileURLWithPath: "/bin/sh")
        launcher.arguments = [scriptPath]
        try? launcher.run()

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
