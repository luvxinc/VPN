import AppKit
import CryptoKit
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

    private let config = AppConfig.load()

    private lazy var session: URLSession = {
        URLSession(configuration: .default, delegate: self, delegateQueue: nil)
    }()

    // MARK: - Public

    func start(downloadURL: String) {
        guard let url = URL(string: downloadURL) else {
            state = .failed(L("error.networkGeneric"))
            return
        }
        state = .downloading(0)
        session.downloadTask(with: url).resume()
    }

    // MARK: - Install

    private func install(from tmp: URL) {
        state = .installing

        let zipDest = URL(fileURLWithPath: "/tmp/weiai-update.zip")
        let updateDir = URL(fileURLWithPath: "/tmp/weiai-update")

        // Move downloaded file to a stable path
        try? FileManager.default.removeItem(at: zipDest)
        do {
            try FileManager.default.moveItem(at: tmp, to: zipDest)
        } catch {
            state = .failed(L("update.error.invalidPackage"))
            return
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

        // Detached installer script: runs after this process quits
        let script = """
        #!/bin/sh
        sleep 1.5
        cp -Rf "\(newAppPath)" "\(currentAppPath)"
        open "\(currentAppPath)"
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
        Task { @MainActor in self.install(from: location) }
    }

    nonisolated func urlSession(_ session: URLSession,
                                task: URLSessionTask,
                                didCompleteWithError error: Error?) {
        guard let error = error else { return }
        Task { @MainActor in self.state = .failed(error.localizedDescription) }
    }
}

// MARK: - Certificate Pinning (same logic as AuthService)

extension UpdateService: URLSessionDelegate {

    nonisolated func urlSession(_ session: URLSession,
                                didReceive challenge: URLAuthenticationChallenge,
                                completionHandler: @escaping (URLSession.AuthChallengeDisposition, URLCredential?) -> Void) {
        guard challenge.protectionSpace.authenticationMethod == NSURLAuthenticationMethodServerTrust,
              let serverTrust = challenge.protectionSpace.serverTrust
        else {
            completionHandler(.cancelAuthenticationChallenge, nil)
            return
        }

        let cert: SecCertificate?
        if let chain = SecTrustCopyCertificateChain(serverTrust) as NSArray?,
           let first = chain.firstObject {
            cert = (first as! SecCertificate)
        } else {
            cert = nil
        }

        if let cert = cert {
            let data = SecCertificateCopyData(cert) as Data
            let hash = SHA256.hash(data: data)
            let fp = hash.map { String(format: "%02X", $0) }.joined()
            if fp == config.certFingerprint {
                completionHandler(.useCredential, URLCredential(trust: serverTrust))
                return
            }
        }
        completionHandler(.cancelAuthenticationChallenge, nil)
    }
}
