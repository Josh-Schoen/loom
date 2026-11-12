# iOS Embedding Example

⚠️ Partial: verified in the iOS simulator only.

`LoomDemo` is a single-file SwiftUI app that runs a Loom agent in-process through `pkg/mobile`. The model, one tool and a progress listener are implemented in Swift. The model is a scripted stand-in, so the example needs no API key.

Run it in the booted iOS simulator (macOS with Xcode):

```bash
just run-ios-example
```

Files:

- `LoomDemo/main.swift` — the app
- `LoomDemo/Info.plist` — bundle metadata
- `run-simulator.sh` — builds the app against `bin/LoomMobile.xcframework`, launches it and prints its report

See [docs/guides/ios-embedding.md](../../docs/guides/ios-embedding.md).
