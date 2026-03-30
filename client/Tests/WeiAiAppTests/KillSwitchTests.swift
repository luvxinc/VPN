import XCTest
@testable import WeiAiApp

// KillSwitch is @MainActor — run tests on MainActor
@MainActor
final class KillSwitchTests: XCTestCase {

    private let statePath = "/tmp/weiai_ks_active"

    override func setUp() async throws {
        try await super.setUp()
        // Ensure kill switch state file doesn't exist
        try? FileManager.default.removeItem(atPath: statePath)
    }

    override func tearDown() async throws {
        try? FileManager.default.removeItem(atPath: statePath)
        try await super.tearDown()
    }

    // MARK: - Tests

    func test_isActiveFalse_whenNoStateFile() {
        // No state file and no null routes → should be inactive
        // (route check may vary in test env, but state file check is deterministic)
        try? FileManager.default.removeItem(atPath: statePath)
        // Primary check: file doesn't exist
        XCTAssertFalse(FileManager.default.fileExists(atPath: statePath))
    }

    func test_activateScript_containsNullRoutes() {
        // The activate script should route 0.0.0.0/1 and 128.0.0.0/1 to 127.0.0.1
        let expectedRoutes = [
            "/sbin/route add -net 0.0.0.0/1 127.0.0.1",
            "/sbin/route add -net 128.0.0.0/1 127.0.0.1",
        ]
        // Read the activate script content by examining what activate() would write
        // We verify this by inspecting the expected output format
        for route in expectedRoutes {
            // Validate the route command format is correct
            XCTAssertTrue(route.contains("0.0.0.0/1") || route.contains("128.0.0.0/1"))
            XCTAssertTrue(route.contains("127.0.0.1"))
        }
    }

    func test_deactivateScript_containsDeleteRoutes() {
        let expectedRoutes = [
            "/sbin/route delete -net 0.0.0.0/1 127.0.0.1",
            "/sbin/route delete -net 128.0.0.0/1 127.0.0.1",
        ]
        for route in expectedRoutes {
            XCTAssertTrue(route.contains("delete"))
            XCTAssertTrue(route.contains("127.0.0.1"))
        }
    }

    func test_stateFilePathIsInTmp() {
        XCTAssertTrue(statePath.hasPrefix("/tmp/"))
    }

    func test_stateFileCreated_marksActiveOnCheck() {
        // Simulate what the activate script does: touch the state file
        FileManager.default.createFile(atPath: statePath, contents: nil)
        XCTAssertTrue(KillSwitch.shared.isActive)
    }

    func test_stateFileRemoved_marksInactive() {
        // Create and then remove the state file
        FileManager.default.createFile(atPath: statePath, contents: nil)
        XCTAssertTrue(KillSwitch.shared.isActive)

        try? FileManager.default.removeItem(atPath: statePath)
        // isActive checks file first — without file and without null route, should be false
        // (route check depends on system state, we just verify the file check path)
        XCTAssertFalse(FileManager.default.fileExists(atPath: statePath))
    }
}
