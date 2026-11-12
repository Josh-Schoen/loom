# iOS Embedding Guide

> **Note:** Added after v1.4.0. ⚠️ Partial: verified in the iOS simulator only. It has not been run on a physical device.

## Table of Contents

- [Overview](#overview)
- [Prerequisites](#prerequisites)
- [Quick Start](#quick-start)
- [Common Tasks](#common-tasks)
  - [Create an engine](#task-create-an-engine)
  - [Supply the model](#task-supply-the-model)
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
- ⚠️ iOS simulator verified; physical device not yet run
- 📋 Model providers that ship with the framework (the host writes its own today)
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
stream stages: EXECUTION_STAGE_LLM_GENERATION > EXECUTION_STAGE_TOOL_EXECUTION > EXECUTION_STAGE_LLM_GENERATION > EXECUTION_STAGE_COMPLETED
stream answer: Trail facts: {"result":{"aspect":"NE","length_m":1200,"max_pitch_deg":34,"trail":"[...] Corbet's Couloir"}}
weave answer: Trail facts: {"result":{"aspect":"NE","length_m":1200,"max_pitch_deg":34,"trail":"[...] Rendezvous Bowl"}}
failed tool answer: The lookup failed: trail is closed
data directory: sessions.db, sessions.db-shm, sessions.db-wal, tool_results, traces.db, traces.db-shm, traces.db-wal
LOOM_DEMO_OK
LOOM_DEMO_END
```

The example's model is a scripted stand-in written in Swift, so the run needs no API key and makes no network call.

To build only the framework:

```bash
just build-ios-framework
```

This writes `bin/LoomMobile.xcframework`. Add it to an Xcode target under "Frameworks, Libraries, and Embedded Content", then `import LoomMobile`. To target a different minimum iOS version, pass it: `just build-ios-framework 17.0`.

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

### Example: Swift app with a scripted model and one tool

`examples/ios-embedding/LoomDemo/main.swift` is the app that `just run-ios-example` builds and runs. It implements the model, a tool and a progress listener in Swift, runs one streamed turn, one plain turn and one turn whose tool call fails, and prints what it stored in its data directory.

## Limitations

- **Simulator only so far.** The device slice of the framework builds, but no run on a physical iPhone has been done.
- **No bundled model providers.** Loom's Anthropic, Bedrock, OpenAI and other providers are not exposed through this API. The host implements the model call.
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
