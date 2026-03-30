import Foundation
import CryptoKit
import IOKit

// MARK: - Types

struct VPNConfig {
    let uuid: String
    let server: String
    let port: Int
    let publicKey: String
    let shortId: String
    let serverName: String
}

enum AuthError: LocalizedError {
    case unexpectedResponse
    case invalidResponse
    case deviceNotRegistered
    case serverOffline
    case networkError(Error)
    case updateRequired(downloadURL: String)
    case serverError(Int, String)

    var errorDescription: String {
        switch self {
        case .unexpectedResponse:
            return "服务器无响应"
        case .invalidResponse:
            return "服务器返回格式错误"
        case .deviceNotRegistered:
            return "此设备未注册，请联系管理员获取验证码"
        case .serverOffline:
            return "服务器暂时离线，请稍后再试"
        case .networkError(let e):
            return _friendlyNetworkError(e)
        case .updateRequired:
            return "客户端版本过旧，需要更新后才能使用"
        case .serverError(let code, let msg):
            if code == 401 { return "用户名或密码错误" }
            if code == 429 { return "请求太频繁，请稍后再试" }
            if code == 403 { return "账号已被禁用" }
            return "服务器错误 (\(code)): \(msg)"
        }
    }
}

private func _friendlyNetworkError(_ error: Error) -> String {
    let code = (error as NSError).code
    switch code {
    case NSURLErrorNotConnectedToInternet:
        return "无网络连接，请检查 Wi-Fi 或移动数据"
    case NSURLErrorCannotConnectToHost, NSURLErrorCannotFindHost:
        return "服务器暂时离线，请稍后再试"
    case NSURLErrorTimedOut:
        return "连接超时，请检查网络后重试"
    case NSURLErrorNetworkConnectionLost:
        return "网络连接中断，请重试"
    case NSURLErrorSecureConnectionFailed, NSURLErrorServerCertificateUntrusted:
        return "安全连接失败，请联系管理员"
    default:
        return "网络错误，请重试"
    }
}

// MARK: - AuthService

final class AuthService: NSObject {

    static let shared = AuthService()

    private let config = AppConfig.load()

    private lazy var session: URLSession = {
        URLSession(configuration: .default, delegate: self, delegateQueue: nil)
    }()

    // MARK: - Device identity

    var deviceID: String {
        let service = IOServiceGetMatchingService(kIOMainPortDefault,
                                                  IOServiceMatching("IOPlatformExpertDevice"))
        defer { IOObjectRelease(service) }
        let cfKey = "IOPlatformUUID" as CFString
        let rawVal = IORegistryEntryCreateCFProperty(service, cfKey, kCFAllocatorDefault, 0)
        return (rawVal?.takeRetainedValue() as? String) ?? UUID().uuidString
    }

    var deviceName: String {
        Host.current().localizedName ?? "Mac"
    }

    // MARK: - Saved credentials (Keychain)

    var savedUsername: String? { KeychainHelper.load(.username) }
    var savedPassword: String? { KeychainHelper.load(.password) }

    // MARK: - Connect

    func connect(username: String, password: String,
                 completion: @escaping (Result<VPNConfig, AuthError>) -> Void) {
        KeychainHelper.save(username, for: .username)
        KeychainHelper.save(password, for: .password)

        let body: [String: Any] = [
            "username":    username,
            "password":    password,
            "device_id":   deviceID,
            "device_name": deviceName,
        ]
        post(path: "/connect", body: body, completion: completion)
    }

    func verifyDevice(username: String, password: String, code: String,
                      completion: @escaping (Result<VPNConfig, AuthError>) -> Void) {
        let body: [String: Any] = [
            "username":          username,
            "password":          password,
            "device_id":         deviceID,
            "device_name":       deviceName,
            "verification_code": code,
        ]
        post(path: "/verify-device", body: body, completion: completion)
    }

    // MARK: - Refresh

    func refreshToken(completion: @escaping (Bool) -> Void) {
        guard let refresh = KeychainHelper.load(.refreshToken) else {
            completion(false)
            return
        }
        guard let url = URL(string: "\(config.authURL)/refresh") else {
            completion(false)
            return
        }
        var req = URLRequest(url: url, timeoutInterval: 10)
        req.httpMethod = "POST"
        req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        req.httpBody = try? JSONSerialization.data(withJSONObject: ["refresh_token": refresh])

        session.dataTask(with: req) { data, response, _ in
            guard let http = response as? HTTPURLResponse,
                  http.statusCode == 200,
                  let data = data,
                  let json = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
                  let token = json["access_token"] as? String
            else {
                completion(false)
                return
            }
            KeychainHelper.save(token, for: .accessToken)
            completion(true)
        }.resume()
    }

    // MARK: - Disconnect

    func disconnect(completion: (() -> Void)? = nil) {
        let deviceID = self.deviceID
        guard let url = URL(string: "\(config.authURL)/disconnect") else {
            KeychainHelper.clearAll()
            completion?()
            return
        }
        var req = URLRequest(url: url, timeoutInterval: 10)
        req.httpMethod = "POST"
        req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        if let token = KeychainHelper.load(.accessToken) {
            req.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        }
        req.httpBody = try? JSONSerialization.data(withJSONObject: ["device_id": deviceID])

        session.dataTask(with: req) { _, _, _ in
            KeychainHelper.clearAll()
            completion?()
        }.resume()
    }

    // MARK: - Private

    private func post(path: String, body: [String: Any],
                      completion: @escaping (Result<VPNConfig, AuthError>) -> Void) {
        guard let url = URL(string: "\(config.authURL)\(path)") else { return }

        var req = URLRequest(url: url, timeoutInterval: 15)
        req.httpMethod = "POST"
        req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        req.setValue(AppVersion.headerValue, forHTTPHeaderField: "X-Client-Version")
        req.httpBody = try? JSONSerialization.data(withJSONObject: body)

        session.dataTask(with: req) { data, response, error in
            if let error = error {
                let nsErr = error as NSError
                if nsErr.code == NSURLErrorCannotConnectToHost ||
                   nsErr.code == NSURLErrorCannotFindHost ||
                   nsErr.code == NSURLErrorTimedOut {
                    completion(.failure(.serverOffline))
                } else {
                    completion(.failure(.networkError(error)))
                }
                return
            }
            guard let http = response as? HTTPURLResponse else {
                completion(.failure(.unexpectedResponse))
                return
            }

            // 426 Upgrade Required — client version too old
            if http.statusCode == 426,
               let data = data,
               let json = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
               let detail = json["detail"] as? [String: Any],
               (detail["error"] as? String) == "client_version_outdated" {
                let url = (detail["download_url"] as? String) ?? ""
                completion(.failure(.updateRequired(downloadURL: url)))
                return
            }

            // 403 device_not_registered is a distinct state, not a generic error
            if http.statusCode == 403,
               let data = data,
               let json = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
               let detail = json["detail"] as? [String: Any],
               (detail["error"] as? String) == "device_not_registered" {
                completion(.failure(.deviceNotRegistered))
                return
            }

            guard http.statusCode == 200, let data = data else {
                let msg = String(data: data ?? Data(), encoding: .utf8) ?? "Unknown error"
                completion(.failure(.serverError(http.statusCode, msg)))
                return
            }

            guard let json  = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
                  let at    = json["access_token"]  as? String,
                  let rt    = json["refresh_token"] as? String,
                  let vc    = json["vless_config"]  as? [String: Any],
                  let uuid  = vc["uuid"]       as? String,
                  let srv   = vc["server"]     as? String,
                  let port  = vc["port"]       as? Int,
                  let pub   = vc["public_key"] as? String,
                  let sid   = vc["short_id"]   as? String,
                  let sni   = vc["server_name"] as? String
            else {
                completion(.failure(.invalidResponse))
                return
            }

            KeychainHelper.save(at, for: .accessToken)
            KeychainHelper.save(rt, for: .refreshToken)

            let vpnConfig = VPNConfig(uuid: uuid, server: srv, port: port,
                                      publicKey: pub, shortId: sid, serverName: sni)
            completion(.success(vpnConfig))
        }.resume()
    }
}

// MARK: - Certificate Pinning

extension AuthService: URLSessionDelegate {
    func urlSession(_ session: URLSession,
                    didReceive challenge: URLAuthenticationChallenge,
                    completionHandler: @escaping (URLSession.AuthChallengeDisposition, URLCredential?) -> Void) {

        guard challenge.protectionSpace.authenticationMethod == NSURLAuthenticationMethodServerTrust,
              let serverTrust = challenge.protectionSpace.serverTrust
        else {
            completionHandler(.cancelAuthenticationChallenge, nil)
            return
        }

        let cert: SecCertificate?
        if let cfChain = SecTrustCopyCertificateChain(serverTrust) as NSArray?,
           let first = cfChain.firstObject {
            cert = (first as! SecCertificate)
        } else {
            cert = nil
        }

        if let cert = cert {
            let certData = SecCertificateCopyData(cert) as Data
            let hash = SHA256.hash(data: certData)
            let fingerprint = hash.map { String(format: "%02X", $0) }.joined()
            if fingerprint == config.certFingerprint {
                completionHandler(.useCredential, URLCredential(trust: serverTrust))
                return
            }
        }
        completionHandler(.cancelAuthenticationChallenge, nil)
    }
}
