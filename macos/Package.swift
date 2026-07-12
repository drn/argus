// swift-tools-version: 6.0
import PackageDescription
import Foundation

// ArgusKit — a typed Swift client SDK for the argus daemon's REST + SSE API.
//
// Pure Foundation, no external dependencies. Builds with the Swift 6.3 Command
// Line Tools toolchain (`swift build` / `swift test`) — no Xcode, no xcodebuild.
//
// Argus is the SwiftUI app shell (a Conductor-style GUI) built on top of the
// ArgusKit library. It has no third-party dependencies (SwiftTerm is added by a
// later phase). Run it with `swift run Argus`.

// swift-testing wiring for Command Line Tools-only installs.
//
// The `Testing` module (swift-testing) ships inside the active developer
// directory, but on a CLT-only install (no Xcode) it is NOT on SwiftPM's
// default framework / rpath search path, so `swift test` fails to compile
// (`no such module 'Testing'`) and — even when the module is found — the test
// bundle cannot dlopen `Testing.framework` + `lib_TestingInterop.dylib` at
// runtime. These flags add the two directories where CLT stows them:
//   <devdir>/Library/Developer/Frameworks       (Testing.framework)
//   <devdir>/Library/Developer/usr/lib          (lib_TestingInterop.dylib)
// On an Xcode install those paths don't exist, so the flags are skipped and the
// default (already working) resolution is left untouched.
// The SwiftPM manifest is compiled and evaluated inside a sandbox that forbids
// spawning subprocesses, so we can't shell out to `xcode-select -p`. Instead we
// probe the known developer-directory locations (honoring DEVELOPER_DIR) with
// FileManager, which the manifest sandbox permits.
func swiftTestingFlags() -> (swift: [String], linker: [String]) {
    let fm = FileManager.default
    var candidates: [String] = []
    if let env = ProcessInfo.processInfo.environment["DEVELOPER_DIR"], !env.isEmpty {
        candidates.append(env)
    }
    candidates.append("/Library/Developer/CommandLineTools")
    candidates.append("/Applications/Xcode.app/Contents/Developer")

    for devDir in candidates {
        let frameworks = "\(devDir)/Library/Developer/Frameworks"
        let interopLib = "\(devDir)/Library/Developer/usr/lib"
        let macroPlugins = "\(devDir)/usr/lib/swift/host/plugins/testing"
        guard fm.fileExists(atPath: "\(frameworks)/Testing.framework") else { continue }
        var swiftFlags = ["-F", frameworks]
        // The @Test/@Suite macros live in a plugin dir SwiftPM only wires up
        // for testTargets; the executable test runner needs it explicitly.
        if fm.fileExists(atPath: macroPlugins) {
            swiftFlags += ["-plugin-path", macroPlugins]
        }
        return (
            swift: swiftFlags,
            linker: [
                "-F", frameworks,
                "-Xlinker", "-rpath", "-Xlinker", frameworks,
                "-Xlinker", "-rpath", "-Xlinker", interopLib,
            ]
        )
    }
    return ([], [])
}

let testingFlags = swiftTestingFlags()

let package = Package(
    name: "ArgusKit",
    platforms: [
        .macOS(.v15),
    ],
    products: [
        .library(name: "ArgusKit", targets: ["ArgusKit"]),
        .executable(name: "Argus", targets: ["Argus"]),
    ],
    dependencies: [
        // SwiftTerm powers the live agent terminal in Argus ONLY. ArgusKit
        // stays pure Foundation (no third-party deps) so its stream-session
        // state machine remains trivially testable.
        // Pinned exact: the only third-party dependency, with no CVE/changelog
        // gate in CI — bumps are deliberate edits here, not `swift package update`.
        .package(url: "https://github.com/migueldeicaza/SwiftTerm.git", exact: "1.13.0"),
    ],
    targets: [
        .target(
            name: "ArgusKit"
        ),
        .executableTarget(
            name: "Argus",
            dependencies: [
                "ArgusKit",
                .product(name: "SwiftTerm", package: "SwiftTerm"),
            ],
            resources: [.copy("Resources/AppIcon.icns")]
            // Argus builds clean in the default Swift 6 language mode. If a
            // future phase hits an intractable strict-concurrency fight in UI
            // glue, scope the escape hatch to THIS target only by adding:
            //   swiftSettings: [.swiftLanguageMode(.v5)]
        ),
        // Tests are an EXECUTABLE, not a testTarget: on a Command Line
        // Tools-only install (no Xcode), `swift test` builds the .xctest
        // bundle but swiftpm-testing-helper cannot execute it and silently
        // exits 0 without running a single test — even failures "pass".
        // Running swift-testing's entry point from a plain executable
        // (`swift run ArgusKitTests`, wired to `make mac-test`) executes the
        // suite for real and propagates a correct exit code.
        .executableTarget(
            name: "ArgusKitTests",
            dependencies: ["ArgusKit"],
            path: "Tests/ArgusKitTests",
            swiftSettings: testingFlags.swift.isEmpty
                ? [] : [.unsafeFlags(testingFlags.swift)],
            linkerSettings: testingFlags.linker.isEmpty
                ? [] : [.unsafeFlags(testingFlags.linker)]
        ),
    ]
)
