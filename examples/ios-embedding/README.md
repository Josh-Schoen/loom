# iOS Embedding Example

⚠️ Partial: verified in the iOS simulator and on macOS, not on a physical iPhone.

A demo that runs a Loom agent in-process through `pkg/mobile`, with the model, one tool and a progress listener implemented in Swift.

Run it in the booted iOS simulator with a scripted stand-in model (no API key, no network):

```bash
just run-ios-example
```

Run it on this Mac with Apple's on-device model (macOS 26 or later with Apple Intelligence enabled):

```bash
just run-macos-example
```

Files:

- `Shared/DemoSupport.swift` — the tool, progress listener, scripted model and the three demo turns
- `Shared/OnDeviceModel.swift` — a model provider backed by Apple's FoundationModels framework
- `LoomDemo/` — SwiftUI app shell and bundle metadata for the simulator
- `MacDemo/main.swift` — command-line entry point for macOS
- `run-simulator.sh`, `run-macos.sh` — build against `bin/LoomMobile.xcframework`, run, and print the demo's report

The on-device model also runs in the simulator (`just run-ios-example 16.0 on-device`) when the simulator's iOS version is not newer than the Mac's macOS version: it works in an iOS 26.5 simulator on macOS 26.6 and fails in iOS 27 simulators on that host. See the guide's Limitations.

See [docs/guides/ios-embedding.md](../../docs/guides/ios-embedding.md).
