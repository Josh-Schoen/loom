# iOS Embedding Example

⚠️ Partial: the engine is verified in the iOS simulator and on macOS, not on a physical iPhone.

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

The on-device model does not generate in an iOS 27 simulator on a macOS 26 host, because the simulator hands generation to the Mac and the Mac's older model rejects the request; see the guide's Limitations.

See [docs/guides/ios-embedding.md](../../docs/guides/ios-embedding.md).
