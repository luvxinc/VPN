// swift-tools-version:5.7
import PackageDescription

let package = Package(
    name: "WeiAiApp",
    platforms: [.macOS(.v13)],
    targets: [
        .executableTarget(
            name: "WeiAiApp",
            path: "Sources/WeiAiApp"
        ),
        .testTarget(
            name: "WeiAiAppTests",
            dependencies: ["WeiAiApp"],
            path: "Tests/WeiAiAppTests"
        ),
    ]
)
