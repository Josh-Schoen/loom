# iOS Embedding Guide

> **Note:** Added after v1.4.0. ⚠️ Partial: the engine is verified in the iOS simulator and on macOS. It has not been run on a physical iPhone, and Apple's on-device model has been verified on macOS only.

## Table of Contents

- [Overview](#overview)
- [Prerequisites](#prerequisites)
- [Quick Start](#quick-start)
- [Common Tasks](#common-tasks)
  - [Create an engine](#task-create-an-engine)
  - [Supply the model](#task-supply-the-model)
  - [Use Apple's on-device model](#task-use-apples-on-device-model)
  - [Add a tool](#task-add-a-tool)
  - [Run a turn](#task-run-a-turn)
  - [Shut down](#task-shut-down)
- [Examples](#examples)
- [Limitations](#limitations)
- [Troubleshooting](#troubleshooting)
- [Next Steps](#next-steps)

## Overview

`pkg/mobile` runs a Loom agent inside an iOS app, with no `looms` server. The app links a framework built with gomobile and supplies two things through callbacks: the model and the tools. The engine runs the agent loop and stores sessions in SQLite inside the app's container.

Status:

- ✅ Agent loop with host-supplied model and tools
- ✅ Session persistence across app launches
- ✅ Progress streaming
- ✅ Optional tracing to a local SQLite trace database
- ✅ Example provider for Apple's on-device model, verified on macOS 26
- ⚠️ iOS simulator verified with a scripted model; physical device not yet run
- ⚠️ On-device model in the iOS simulator: fails where the simulator has no model assets (see [Limitations](#limitations))
- 📋 Model providers that ship with the framework (the host writes its own, or copies the example)
- 📋 Android

Turns use the same `WeaveRequest`, `WeaveResponse` and `WeaveProgress` messages as the gRPC API. The messages specific to embedding are in `proto/loom/v1/mobile.proto`. Every message crosses between Swift and the engine as proto3 JSON, with the field names written in the `.proto` files.

## Prerequisites

- macOS with Xcode and an iOS SDK
- Go (the version in `go.mod`)
- `just`
- A booted iOS simulator, to run the example

gomobile is pinned in `go.mod` as a module tool. You do not install it separately.

## Quick Start

Build the framework and run the example app in the booted simulator:

```bash
just run-ios-example
```

Expected output:

```
LOOM_DEMO_BEGIN
model: scripted / scripted-1
stream stages: EXECUTION_STAGE_LLM_GENERATION > EXECUTION_STAGE_TOOL_EXECUTION > EXECUTION_STAGE_LLM_GENERATION > EXECUTION_STAGE_COMPLETED
stream answer: Trail facts: {"result":{"aspect":"NE","length_m":1200,"max_pitch_deg":34,"trail":"[...] How steep is Corbet's Couloir?"}}
weave answer: Trail facts: {"result":{"aspect":"NE","length_m":1200,"max_pitch_deg":34,"trail":"[...] Which way does Rendezvous Bowl face?"}}
failed tool answer: The lookup failed: trail is closed
data directory: sessions.db, sessions.db-shm, sessions.db-wal, tool_results, traces.db, traces.db-shm, traces.db-wal
LOOM_DEMO_OK
LOOM_DEMO_END
```

This run uses a scripted stand-in for the model, written in Swift, so it needs no API key and makes no network call.

To run the same demo on your Mac with Apple's on-device model (macOS 26 or later with Apple Intelligence enabled):

```bash
just run-macos-example
```

Expected output (the wording of the answers varies between runs):

```
model: apple-on-device / system-language-model
stream stages: EXECUTION_STAGE_LLM_GENERATION > EXECUTION_STAGE_TOOL_EXECUTION > EXECUTION_STAGE_LLM_GENERATION > EXECUTION_STAGE_COMPLETED
stream answer: Corbet's Couloir has a maximum pitch of 34 degrees.
weave answer: Rendezvous Bowl faces north-east.
failed tool answer: Closed Run is closed.
data directory: sessions.db, sessions.db-shm, sessions.db-wal, tool_results, traces.db, traces.db-shm, traces.db-wal
LOOM_DEMO_OK
```

To build only the framework:

```bash
just build-apple-framework
```

This writes `bin/LoomMobile.xcframework` with iOS device and simulator slices. Add it to an Xcode target under "Frameworks, Libraries, and Embedded Content", then `import LoomMobile`. The recipe takes a gomobile target list and a minimum iOS version, for example `just build-apple-framework "ios,iossimulator,macos" 17.0`. Each build replaces the framework, so name every slice you need in one call.

## Common Tasks

### Task: Create an engine

Pass a `MobileEngineConfig` as JSON and a model provider. `data_dir` must be an absolute path the app can write to.

```swift
import LoomMobile

let dataDir = FileManager.default
    .urls(for: .applicationSupportDirectory, in: .userDomainMask)[0]
    .appendingPathComponent("loom")

let config = """
{
  "data_dir": "\(dataDir.path)",
  "agent": {
    "name": "trail-guide",
    "system_prompt": "Answer questions about ski trails with the trail_lookup tool."
  }
}
"""

var error: NSError?
guard let engine = LoomMobileNewEngine(config, MyModel(), &error) else {
    fatalError(error?.localizedDescription ?? "engine failed to start")
}
```

Supported configuration:

| Field | Meaning |
|-------|---------|
| `data_dir` | Required. Directory for the session database, trace database and tool-result overflow |
| `agent.name` | Required |
| `agent.system_prompt` | Required |
| `agent.description`, `agent.rom` | Optional |
| `agent.behavior.max_turns`, `max_tool_executions`, `max_iterations` | Optional; agent defaults apply when unset |
| `tool_result_memory_bytes` | Optional; defaults to 32 MiB |
| `enable_tracing` | Optional; writes spans to `traces.db` under `data_dir` |

Any other `AgentConfig` field is rejected with an error that names it.

### Task: Supply the model

Implement `LoomMobileModelProviderProtocol`. `chat` receives a `HostModelRequest` and returns a `HostModelResponse`, both as JSON.

```swift
final class MyModel: NSObject, LoomMobileModelProviderProtocol {
    func providerName() -> String { "my-relay" }
    func modelID() -> String { "my-model" }

    func chat(_ requestJSON: String?, error: NSErrorPointer) -> String {
        // requestJSON: {"messages":[{"role":"user","content":"..."}], "tools":[...]}
        // Call an on-device model or your relay server here, then return:
        // {"content":"...", "stop_reason":"end_turn"}
        // or, to run a tool first:
        // {"stop_reason":"tool_use",
        //  "tool_calls":[{"id":"call_1","name":"trail_lookup","input":{"name":"..."}}]}
        return callMyModel(requestJSON)
    }
}
```

To report a failure, set `error.pointee` to an `NSError`; the turn fails with that message.

Do not ship a model API key inside the app. Route cloud model calls through a server you control.

### Task: Use Apple's on-device model

`examples/ios-embedding/Shared/OnDeviceModel.swift` is a `ModelProvider` backed by the FoundationModels framework (iOS 26 / macOS 26 and later). Copy it into your app, check availability, and pass it to the engine:

```swift
if #available(iOS 26.0, *), OnDeviceModel.unavailableReason == nil {
    engine = LoomMobileNewEngine(config, OnDeviceModel(), &error)
}
```

How it works: Loom runs tools itself, while a FoundationModels session expects to run them. The provider presents each Loom tool to the session as a tool that records the call and stops the session, returns that call to Loom, and on the next call rebuilds the session history including the tool result.

Things to know:

- It converts each tool's JSON Schema to a generation schema. Objects, arrays, strings (including enums), numbers, integers and booleans are covered. A schema using anything else, such as `anyOf`, fails the model call with an error naming the tool.
- After tool results it asks the model to continue with a fixed prompt (`OnDeviceModel.continuationPrompt`), because a session only generates in response to a prompt.
- It reports no token usage.
- The on-device model has a small context window. Long system prompts, many tools or long conversations can exceed it.

### Task: Add a tool

Implement `LoomMobileHostToolProtocol` and register it before running turns.

```swift
final class TrailLookup: NSObject, LoomMobileHostToolProtocol {
    func toolName() -> String { "trail_lookup" }
    func toolDescription() -> String { "Look up pitch and aspect for a named ski trail." }
    func inputSchemaJSON() -> String {
        #"{"type":"object","properties":{"name":{"type":"string"}},"required":["name"]}"#
    }

    func execute(_ inputJSON: String?, error: NSErrorPointer) -> String {
        // inputJSON: {"name":"Corbet's Couloir"}
        return #"{"max_pitch_deg":34,"aspect":"NE"}"#
    }
}

try engine.register(TrailLookup())
```

`execute` returns any JSON value, or an empty string for no data. If it sets an error, or returns text that is not JSON, the model is told the tool failed and the turn continues.

### Task: Run a turn

For a single result, call `weave` with a `WeaveRequest`:

```swift
var error: NSError?
let responseJSON = engine.weave(#"{"session_id":"main","query":"How steep is Corbet's?"}"#, error: &error)
// responseJSON: {"text":"...", "session_id":"main", ...}
```

For progress while the turn runs, call `streamWeave` with a listener. The last event has stage `EXECUTION_STAGE_COMPLETED` and carries the answer in `partial_content`.

```swift
final class Listener: NSObject, LoomMobileProgressListenerProtocol {
    func onProgress(_ progressJSON: String?) {
        // {"stage":"EXECUTION_STAGE_TOOL_EXECUTION","tool_name":"trail_lookup", ...}
    }
}

try engine.streamWeave(#"{"session_id":"main","query":"How steep is Corbet's?"}"#, listener: Listener())
```

Both calls block until the turn finishes, so call them off the main thread. Reusing a `session_id` continues that conversation, including after the app restarts.

### Task: Shut down

```swift
try engine.close()
```

`close` cancels turns in flight, waits for them to stop and releases the databases. A turn that is inside one of your callbacks stops when the callback returns.

## Examples

### Example: one demo, two models

`examples/ios-embedding/Shared/DemoSupport.swift` holds the demo: a tool, a progress listener, a scripted model and three turns (one streamed, one plain, one whose tool call fails). `LoomDemo/main.swift` wraps it in a SwiftUI app for `just run-ios-example`; `MacDemo/main.swift` runs it from the command line with `OnDeviceModel` for `just run-macos-example`.

To try the on-device model in the simulator, pass the model name: `just run-ios-example 16.0 on-device`. See [Limitations](#limitations) for what to expect.

## Limitations

- **No physical device run.** The device slice of the framework builds, but nothing has been run on a physical iPhone.
- **On-device model verified on macOS only.** In the iOS 27.0 simulator on a macOS 26 host, the model reports itself available and then every generation fails with "no underlying assets ... com.apple.modelcatalog". The same provider source completes all three demo turns on macOS. It has not been run on an iPhone.
- **No bundled model providers.** Loom's Anthropic, Bedrock, OpenAI and other providers are not exposed through this API. The host implements the model call or copies the on-device example.
- **Text only.** Image content blocks are not carried to the host model.
- **Callbacks must be thread-safe.** The engine can call the model and tools from several threads at once.
- **Foreground only.** iOS suspends an app shortly after it leaves the screen, which pauses a turn in progress.
- **No subprocess tools.** `shell_execute` and stdio MCP servers cannot work on iOS and are not available.
- **One tool-result cache per process.** The first engine created in a process fixes the cache location and `tool_result_memory_bytes`; engines created later share it. Its disk overflow is capped at 1 GB with a one-hour lifetime and is not configurable here.
- **Binary size.** The example app's simulator binary is 65 MB before App Store processing. A release build for a device has not been measured.

## Troubleshooting

### `invalid config: not supported by the embedded engine: agent.llm`

The config sets an `AgentConfig` field the engine does not apply. Remove the named field. The model comes from the `ModelProvider` you pass to `LoomMobileNewEngine`, not from `agent.llm`.

### `invalid config: data_dir must be an absolute path`

Pass the full path of a directory in the app container, for example one under Application Support.

### `invalid request: ... unknown field`

A field name in the request JSON is misspelled. Names are the ones in the `.proto` files (`session_id`, `query`).

### `on-device model: Model Catalog error ... no underlying assets`

The simulator has no on-device model assets to run. Use the scripted model in the simulator (`just run-ios-example`) and test the on-device model with `just run-macos-example` or on a device.

### `error: no booted iOS simulator`

Boot one, then rerun:

```bash
xcrun simctl list devices available
xcrun simctl boot "<device name>"
```

### `engine is closed`

The engine was used after `close`. Create a new engine.

## Next Steps

- Contract: `proto/loom/v1/mobile.proto`
- Go API: `pkg/mobile`
- Streaming events: [docs/reference/streaming.md](../reference/streaming.md)
