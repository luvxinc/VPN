import XCTest
@testable import WeiAiApp

final class KeychainHelperTests: XCTestCase {

    override func setUp() {
        super.setUp()
        // Clean slate
        KeychainHelper.clearAll()
    }

    override func tearDown() {
        super.tearDown()
        KeychainHelper.clearAll()
    }

    // MARK: - Tests

    func test_saveAndLoad() {
        KeychainHelper.save("testuser", for: .username)
        XCTAssertEqual(KeychainHelper.load(.username), "testuser")
    }

    func test_overwriteExistingKey() {
        KeychainHelper.save("first", for: .accessToken)
        KeychainHelper.save("second", for: .accessToken)
        XCTAssertEqual(KeychainHelper.load(.accessToken), "second")
    }

    func test_loadMissingKeyReturnsNil() {
        XCTAssertNil(KeychainHelper.load(.refreshToken))
    }

    func test_deleteRemovesKey() {
        KeychainHelper.save("mypassword", for: .password)
        XCTAssertNotNil(KeychainHelper.load(.password))

        KeychainHelper.delete(.password)
        XCTAssertNil(KeychainHelper.load(.password))
    }

    func test_clearAllRemovesAllKeys() {
        KeychainHelper.save("user1",   for: .username)
        KeychainHelper.save("pass1",   for: .password)
        KeychainHelper.save("access1", for: .accessToken)
        KeychainHelper.save("refresh1", for: .refreshToken)

        KeychainHelper.clearAll()

        XCTAssertNil(KeychainHelper.load(.username))
        XCTAssertNil(KeychainHelper.load(.password))
        XCTAssertNil(KeychainHelper.load(.accessToken))
        XCTAssertNil(KeychainHelper.load(.refreshToken))
    }

    func test_savesAndLoadsUnicodeValues() {
        let value = "用户名测试🔑"
        KeychainHelper.save(value, for: .username)
        XCTAssertEqual(KeychainHelper.load(.username), value)
    }

    func test_eachKeyIsIndependent() {
        KeychainHelper.save("alice", for: .username)
        KeychainHelper.save("hunter2", for: .password)

        XCTAssertEqual(KeychainHelper.load(.username), "alice")
        XCTAssertEqual(KeychainHelper.load(.password), "hunter2")

        KeychainHelper.delete(.username)
        XCTAssertNil(KeychainHelper.load(.username))
        XCTAssertEqual(KeychainHelper.load(.password), "hunter2")
    }
}
