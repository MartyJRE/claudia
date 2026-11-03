// swift-tools-version:5.9
import PackageDescription

let package = Package(
    name: "claudia-notify",
    platforms: [.macOS(.v14)],
    products: [
        .executable(name: "claudia-notify", targets: ["claudia-notify"]),
        .executable(name: "claudia-icon", targets: ["claudia-icon"]),
    ],
    targets: [
        .target(name: "NotifierCore"),
        .executableTarget(name: "claudia-notify", dependencies: ["NotifierCore"]),
        .executableTarget(name: "claudia-icon", dependencies: ["NotifierCore"]),
        .testTarget(name: "NotifierCoreTests", dependencies: ["NotifierCore"]),
    ]
)
