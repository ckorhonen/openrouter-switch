// swift-tools-version:5.9
import PackageDescription

let package = Package(
    name: "OpenRouterSwitch",
    platforms: [.macOS(.v13)],
    targets: [
        .executableTarget(name: "OpenRouterSwitch", path: "Sources/OpenRouterSwitch"),
        .testTarget(name: "OpenRouterSwitchTests", dependencies: ["OpenRouterSwitch"])
    ]
)
