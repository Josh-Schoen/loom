---
title: "run_script Tool Reference"
weight: 48
---

# run_script Tool Reference

An agent tool that runs a short calculation script, written by the agent in
Starlark, and returns its result. Each script runs in its own child process
with no access to files, the network, processes, modules, the clock, or
randomness.

**Status**: ✅ Implemented (`pkg/sandbox/script`; `looms serve`). Design and
measurements: [Sandboxed Execution plan](../plans/sandboxed-script-execution.md).

---

## Quick Reference

| Item | Value |
|------|-------|
| Tool name | `run_script` |
| Enable | list `run_script` in the agent's `tools.builtin` |
| Language | Starlark (`go.starlark.net`), no recursion |
| Parameters | `script` (string, required), `inputs` (object, optional) |
| Returns | `{"result": <value>, "output": "<printed text>"}` |
| Limits | 10,000,000 steps, 5 s, 256 MB heap, 20,000-character script, 64 KiB result and output |
| Isolation | child process `looms sandbox-run`, empty environment |

---

## Enabling

`looms serve` registers the tool on every agent whose YAML lists it, at
startup and on hot reload. It is not offered otherwise, including under
`tools.none: true` unless listed.

```yaml
spec:
  tools:
    builtin:
      - run_script
```

---

## Parameters

### script

**Type**: `string`
**Required**: Yes
**Constraints**: at most 20,000 characters

Starlark source. It must set the global `result`. Available names:

| Name | What it is |
|------|------------|
| `inputs` | dict of the `inputs` parameter (JSON integers become `int`, other numbers `float`) |
| `round(x, ndigits=0)` | Python-style rounding, half away from zero; `int` result when `ndigits` is omitted |
| `sum(iterable, start=0)` | Python-style sum of numbers (Starlark has none of its own) |
| `math` | `go.starlark.net/lib/math`: `sqrt`, `pow`, `floor`, `ceil`, `log`, `exp`, `pi`, `e`, trigonometry, ... |
| `json` | `go.starlark.net/lib/json`: `encode`, `decode`, `indent` |
| `print(...)` | appends a line to `output` |
| Starlark built-ins | `len`, `range`, `sorted`, `min`, `max`, `abs`, `int`, `float`, `str`, `dict`, `list`, `enumerate`, `zip`, ... |

Dialect options: top-level `for`/`if`/`while`, `set`, reassignable
globals. Not available: `load`, recursion, `**` (use `math.pow`), `%`
formatting with a precision such as `"%.2f"` (use `round`).

### inputs

**Type**: `object`
**Required**: No

Values the script reads as `inputs["name"]`. Nested objects and arrays
become dicts and lists, at most 32 levels deep.

---

## Result

On success `Success` is true and `Data` is:

| Field | Type | Meaning |
|-------|------|---------|
| `result` | any JSON value | The script's `result`: `None` → `null`, `bool`, `int` (as a string when it does not fit in int64), `float`, `string`, `list`/`tuple` → array, `dict` with string keys → object |
| `output` | string | Text printed with `print`, if any; cut off with `[more output was cut off]` past 64 KiB |

### Errors

Failures return `Success: false`, `Error.Code` and a message the agent can
act on; `Data.output` holds anything printed before the failure.

| Code | Message (examples) | Cause |
|------|--------------------|-------|
| `INVALID_PARAMS` | `script is required` | No script |
| `SCRIPT_FAILED` | `syntax error at line 2: got '**', want newline` | The script does not parse |
| `SCRIPT_FAILED` | `floating-point division by zero (script:1:12: in <toplevel>)` | Runtime error, with its location |
| `SCRIPT_FAILED` | `the script did not set \`result\`` | No `result` global |
| `SCRIPT_FAILED` | `the script took too many steps (more than 10000000); simplify it or loop less` | Step limit |
| `SCRIPT_FAILED` | `the script ran longer than 5s` | Time limit (the child is killed 1 s later if it has not stopped) |
| `SCRIPT_FAILED` | `the script used more than 256 MB of memory` | Heap limit (the child exits) |
| `SCRIPT_FAILED` | `the script stopped unexpectedly (it may have used too much memory)` | The child ended without an answer |
| `SCRIPT_FAILED` | `a function cannot be returned; use numbers, strings, lists or dicts` | `result` cannot be sent as JSON |

---

## Examples

Split a bill with a tip:

```json
{
  "script": "each = round(inputs[\"total\"] * (1 + inputs[\"tip\"]) / inputs[\"people\"], 2)\nresult = {\"each\": each}",
  "inputs": {"total": 84.5, "tip": 0.2, "people": 3}
}
```

Result data: `{"result": {"each": 33.8}}`

Monthly loan payment:

```json
{
  "script": "r = inputs[\"rate\"] / 12\nn = inputs[\"years\"] * 12\np = inputs[\"amount\"] * r / (1 - math.pow(1 + r, -n))\nresult = round(p, 2)",
  "inputs": {"amount": 20000, "rate": 0.06, "years": 5}
}
```

Result data: `{"result": 386.66}`

---

## Isolation

Each call starts `looms sandbox-run` (the running server's own binary,
`os.Executable()`) with an empty environment, sends the script, inputs and
limits as JSON on stdin, and reads one JSON answer from stdout (capped at
the output limit plus 64 KiB). The child handles `sandbox-run` before
loading any configuration or keys.

- **Steps and time**: the Starlark step limit and a thread cancel at the
  timeout, inside the child; the parent kills the child at the timeout plus
  one second.
- **Memory**: the child sets the Go soft memory limit to the heap cap and
  polls the heap every 5 ms; past the cap it answers with the memory error
  and exits. Overshoot before the check is tens of MB at most. Running out
  of memory in the child does not affect the server.
- **Access**: Starlark has no I/O built-ins; `load` fails; only the names in
  [script](#script) are defined.

Start-up costs about 57 ms per call (measured as `looms --help`), plus the
script's own time.

## Limitations

- Calculations only: scripts cannot call other tools, read the person's
  data, or be saved and reused.
- Limits are fixed (`script.DefaultLimits`), not configurable per agent.
- No CPU-time accounting beyond the step and wall-clock limits.

## See Also

- [Sandboxed Execution plan](../plans/sandboxed-script-execution.md)
- [Spending Limit](spending-limit.md)
