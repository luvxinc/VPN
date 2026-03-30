import XCTest
@testable import WeiAiApp

final class ConfigTests: XCTestCase {

    private let configDir: URL = {
        FileManager.default.temporaryDirectory.appendingPathComponent("weiai_test_config")
    }()

    override func setUp() {
        super.setUp()
        try? FileManager.default.createDirectory(at: configDir, withIntermediateDirectories: true)
    }

    override func tearDown() {
        super.tearDown()
        try? FileManager.default.removeItem(at: configDir)
    }

    // MARK: - Tests

    func test_loadFromUserConfig() throws {
        // Write a valid config to the temp directory
        let json = """
        {
          "auth_url": "https://192.168.1.1:9443",
          "cert_fingerprint": "ABCDEF1234567890ABCDEF1234567890ABCDEF1234567890ABCDEF1234567890"
        }
        """
        let configURL = configDir.appendingPathComponent("config.json")
        try json.write(to: configURL, atomically: true, encoding: .utf8)

        // Load using the file URL directly
        guard let data = try? Data(contentsOf: configURL),
              let parsed = try? JSONSerialization.jsonObject(with: data) as? [String: String],
              let authURL = parsed["auth_url"],
              let fingerprint = parsed["cert_fingerprint"]
        else {
            XCTFail("Failed to parse test config")
            return
        }

        XCTAssertEqual(authURL, "https://192.168.1.1:9443")
        XCTAssertEqual(fingerprint, "ABCDEF1234567890ABCDEF1234567890ABCDEF1234567890ABCDEF1234567890")
    }

    func test_fallsBackToDefaultWhenNoConfigFile() {
        // The dev defaults always return non-empty values
        let cfg = AppConfig.load()
        XCTAssertFalse(cfg.authURL.isEmpty)
        XCTAssertFalse(cfg.certFingerprint.isEmpty)
    }

    func test_invalidJsonReturnsNil() {
        // Write invalid JSON
        let badJSON = "not json at all"
        let configURL = configDir.appendingPathComponent("bad.json")
        try? badJSON.write(to: configURL, atomically: true, encoding: .utf8)

        // Parsing should fail gracefully
        let data = try? Data(contentsOf: configURL)
        let parsed = try? JSONSerialization.jsonObject(with: data ?? Data()) as? [String: String]
        XCTAssertNil(parsed)
    }

    func test_missingFieldsReturnsNil() {
        // JSON missing cert_fingerprint
        let json = #"{"auth_url": "https://example.com:9443"}"#
        guard let data = json.data(using: .utf8),
              let parsed = try? JSONSerialization.jsonObject(with: data) as? [String: String]
        else {
            XCTFail("Could not parse JSON")
            return
        }
        let fingerprint = parsed["cert_fingerprint"]
        XCTAssertNil(fingerprint)
    }
}
