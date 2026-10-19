# Sandboxed Execution of Agent-Written Code

**Status**: ⚠️ Partial. The spike below was run on 2026-10-19 (results in
[Spike results](#spike-results)). The first slice, a calculation-only
`run_script` tool, is implemented (`pkg/sandbox/script`,
[reference](../reference/run-script.md)); scripts that call tools or are
saved for reuse are not.

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

## Spike results

Run on 2026-10-19 (Linux, Go 1.25; gopher-lua v1.1.2, go.starlark.net
2026-10-16). Each engine got a fresh state per run, a 2 s wall-clock limit
(Lua: `LState.SetContext`; Starlark: `Thread.Cancel`), Starlark a 50 M
execution-step limit, and Lua only the base, table, string and math
libraries with `dofile`, `loadfile`, `load`, `loadstring`, `require`,
`module`, `getfenv`, `setfenv`, `collectgarbage`, `print`, `rawget`,
`rawset`, `rawequal`, `setmetatable`, `getmetatable` and `newproxy` removed.

| Test | Lua (gopher-lua) | Starlark |
|------|------------------|----------|
| Run a program (`os.execute` / `exec`) | Refused (no `os`) | Refused (undefined) |
| Open a file | Refused (no `io`) | Refused (undefined) |
| Load a module | Refused (`require` removed) | Refused (`load` not provided by the host) |
| Compile code at run time | Refused (`load` removed) | Refused (no `eval`) |
| Reach the string metatable | Refused (`getmetatable` removed) | Not applicable |
| Read the environment | Refused (no `os`) | Refused (undefined) |
| Infinite loop | Stopped at the 2 s deadline | Stopped by the step limit in 78 ms |
| Unbounded recursion | Stack overflow error, 0.5 ms | Stack overflow error, 390 ms, 20 MB |
| One 1 GiB string | **Allocated it** (1024 MB, 418 ms) | Refused (`excessive repeat`) |
| String doubling | 448 MB before the deadline | 384 MB before the deadline |
| Growing table / list | 422 MB before the deadline | 94 MB, stopped by the step limit |
| Fresh state + small script | 201 µs | 9 µs |

Findings:

1. **Both block file, network, process and module access** as configured.
   Starlark has no such access unless the host adds it; Lua is safe only
   after removing the right functions, so a missed one is a hole.
2. **Neither has a hard memory limit.** How much a script allocates before
   the time or step limit depends on machine speed. Run in the server's
   process, several such scripts together ran the spike's process out of
   memory (`runtime: out of memory`), which Go cannot recover from: it
   would take the whole server down.
3. Starlark's step limit stops loops deterministically and quickly; Lua
   relies on the wall clock.
4. Starlark's `%` formatting has no precision (`"%.2f"` fails with
   `unknown conversion %.`), and there is no `**` operator; models write
   both from Python habit. The tool adds `round(x, ndigits)` and its
   description points to `round` and `math.pow`.
5. Starting a child `looms` process costs about 57 ms (`looms --help`,
   20 runs), small next to an LLM turn.
6. Not yet measured: how reliably current models write correct code in
   each language. That needs a model API key (loom-mobile
   `tools/assistant-check`).

## Decision for the first slice

- **Language: Starlark** (`go.starlark.net`): no ambient access by default,
  a deterministic step limit, built-in guards on huge strings, and much
  lower start-up cost. Lua's open-by-default library model is the larger
  risk.
- **Isolation: a child process.** Each script runs in a short-lived
  `looms` child process (a hidden subcommand) that reads the script and its
  inputs on stdin and writes the result on stdout. The parent kills it at
  the time limit; the child also stops itself when its heap passes a cap
  (a watchdog polling the Go heap every 5 ms). A script that exhausts
  memory ends the child, not the server. An OS address-space limit
  (`RLIMIT_AS`) was not used: macOS does not enforce it, and Go reserves
  address space well beyond what it uses.
- **Scope: calculations only.** No modules, files, network, time or
  randomness; inputs come in as values, the result goes back as JSON. A
  `run_script` agent tool exposes it.
