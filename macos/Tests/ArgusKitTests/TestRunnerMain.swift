import Testing

// Entry point for the executable test runner (see the ArgusKitTests target
// comment in Package.swift for why this is not a testTarget). This is the
// same call SwiftPM's generated .xctest runner makes for swift-testing; it
// discovers every @Suite/@Test in the binary, runs them, prints the report,
// and exits non-zero on failure.
@main
struct TestRunnerMain {
    static func main() async {
        await Testing.__swiftPMEntryPoint() as Never
    }
}
