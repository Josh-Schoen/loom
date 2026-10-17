# Sandboxed Execution of Agent-Written Code

**Status**: 📋 Planned (not started). Recorded so it is not lost; to be
picked up after the client-tools work.

## Need

Agents will write small pieces of code on a user's behalf (a filter, a
transform, the logic of a new skill or workflow step) and run them. That code
comes from an LLM acting on a user request, so it must run with no ambient
access: no file system, network, or process access unless loom grants it,
and with hard limits on CPU time and memory.

## What exists today

- **Docker backend** (`docs/architecture/docker-backend.md`): runs Python,
  Node.js, and custom runtimes in containers with cgroup limits. Suitable for
  heavy or general-purpose code; container start costs seconds and the host
  needs Docker.
- **`shell_execute`** (`pkg/shuttle/builtin/shell_execute.go`): restricts the
  working directory (`LOOM_SANDBOX_DIR`). This is a path restriction, not
  isolation.

Neither gives a fast, in-process sandbox for small scripts.

## Where it can run

- **Server (loom)**: the intended location. Generated code stays on the
  user's own server.
- **iOS client**: not by default. App Store guideline 2.5.2 bars apps from
  downloading or executing code that adds or changes features; guideline 4.7
  permits HTML5/JavaScript mini apps "or another language approved by Apple",
  without native API access unless Apple permits it. An embedded Lua
  interpreter running agent-written scripts on the phone would likely be
  rejected. If on-device scripting is ever needed, JavaScriptCore is the
  sanctioned engine. The phone exposes only fixed, reviewed device tools.

## Candidates to evaluate

| Option | Strengths | Weaknesses |
|--------|-----------|------------|
| Lua (gopher-lua, pure Go) | Small, widely known, LLMs write it well | Lua 5.1 only; sandboxing means removing `os`, `io`, `load`, `debug`, `package`/`require`; cancellation via context but no hard memory cap |
| Starlark (go.starlark.net) | No I/O unless granted; built-in execution step limit; deterministic | Fewer language features; Python-like rather than Python |
| WebAssembly (wazero, pure Go) | Strongest isolation; hard memory and CPU limits | Code must be compiled to WebAssembly first |
| Docker backend (existing) | Full languages; already built | Seconds to start; needs Docker |

## Planned spike

A `run_script` tool backed by Lua and Starlark side by side, measured on:

1. Escape attempts (file, network, process, module loading).
2. Infinite loops and deep recursion (CPU / step limits, cancellation).
3. Memory blow-up (allocation limits).
4. Start-up latency and per-call overhead versus the Docker backend.
5. How reliably current models write correct code in each language for the
   same task set.

The choice is made from these measurements, not from the table above.
