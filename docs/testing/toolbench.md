# Tool-Context Benchmark (toolbench)

Measures how many tool definitions loom sends to the model on each call when
several MCP servers are connected, what that costs in input tokens, and
whether loading tools on demand changes behavior. It exists so that changes to
tool loading (on-demand servers, per-conversation tool lists, unloading) are
compared against a fixed baseline instead of guessed.

**Status**: ✅ Deterministic benchmark and regression gate implemented
(`pkg/agent/toolbench_test.go`). 📋 Real-model accuracy runs (`pkg/evals`) are
planned, not implemented.

---

## What it runs

A scripted model plays fixed conversations against a real `Agent`. Every
provider call is recorded: the advertised tool list and the compiled messages
as loom sends them. No API key, no network.

**Fixture**: 5 synthetic MCP servers (github 40, sheets 25, calendar 15,
drive 30, db 40 = 150 tools). Each tool has a ~300-character description and a
6-9 property JSON schema, about 270 tokens per tool. The schemas are generated,
not captured from real servers.

**Setups**:

| Setup | Meaning |
|---|---|
| `eager_all` | Every tool of every server registered up front (MCP config with an empty or `*` tool list) |
| `on_demand` | Only `tool_search` up front; tools found in the on-disk FTS5 index and registered on first call |
| `mixed` | `github` eager, the other four servers on demand |

**Scenarios**:

| Scenario | Shape |
|---|---|
| `short` | 1 conversation, 1 turn, 2 tools from one server |
| `long` | 1 conversation, 10 turns, 11 calls to tools from 4 servers, with reuse |
| `two_sessions` | 2 conversations on one agent, interleaved, different servers |

In the on-demand setups the scripted model calls `tool_search` for any tool it
was not given, then calls the tool by the name the index returned.

## Metrics

| Metric | Meaning |
|---|---|
| `provider_calls` | Model calls in the scenario (search adds round trips) |
| `tool_calls_ok` / `tool_calls` | Scripted tool calls that succeeded |
| `advertised_tools_max` | Largest tool list sent on one call |
| `tool_tokens_total` | Tokens of tool definitions summed over all calls (cl100k tokenizer) |
| `input_tokens_total` | Tool tokens + message tokens summed over all calls |
| `billed_tokens_modeled` | Input priced with prompt caching: the prefix identical to the same conversation's previous call is a cache read (0.1x), the rest a cache write (1.25x); a changed tool list makes the whole request a write |
| `tool_set_changes` | Calls whose tool list differs from the conversation's previous call (each resets the prompt cache) |
| `cross_session_leaks` | Distinct tools advertised to a conversation that were neither in the agent's first tool list nor used by that conversation |

## Running it

```bash
just toolbench          # gate against the baseline, writes ./toolbench.json
just toolbench-update   # regenerate the baseline (intended changes only)
just toolbench-speed    # speed benchmarks, for benchstat
```

CI runs the gate as part of `go test ./pkg/...` (it is skipped under `-short`).

**Gate rules**, per scenario and setup, against
`pkg/agent/testdata/toolbench/baseline.json`:

- every tool call succeeds, and successes do not drop
- `provider_calls`, `tool_set_changes`, `cross_session_leaks` do not go up
- `input_tokens_total` and `billed_tokens_modeled` do not go up by more than 2%

An improvement passes the gate. Lock it in with `just toolbench-update` so later
changes cannot give it back; the baseline diff is the review record.

**Speed**: `toolbench-speed` reports ns/op and allocations for full
conversations, for building the advertised tool list, and for `tool_search`.
These are machine-dependent and not gated; compare two runs on the same
machine with `benchstat old.txt new.txt`.

## Baseline (main @ 29b10ed)

| Scenario | Setup | Calls | Tools max | Tool tokens | Input tokens | Billed (modeled) | Tool-list changes | Leaks |
|---|---|---|---|---|---|---|---|---|
| short | eager_all | 3 | 153 | 123,387 | 124,536 | 60,280 | 0 | 0 |
| short | on_demand | 5 | 6 | 4,764 | 13,249 | 12,470 | 2 | 0 |
| short | mixed | 3 | 44 | 34,749 | 35,898 | 17,439 | 0 | 0 |
| long | eager_all | 21 | 153 | 863,709 | 882,468 | 137,242 | 0 | 0 |
| long | on_demand | 29 | 12 | 53,237 | 227,190 | 107,406 | 8 | 0 |
| long | mixed | 26 | 49 | 318,136 | 421,605 | 159,030 | 5 | 0 |
| two_sessions | eager_all | 10 | 153 | 411,290 | 415,596 | 137,414 | 0 | 0 |
| two_sessions | on_demand | 14 | 8 | 17,895 | 43,834 | 40,230 | 7 | 4 |
| two_sessions | mixed | 12 | 46 | 141,678 | 154,720 | 106,708 | 4 | 2 |

All tool calls succeeded in every row.

### What the baseline shows

1. **On demand cuts raw input sharply** (long: 882k → 227k input tokens), but
   with prompt caching the saving is smaller (137k → 107k billed), because
   every first use of a tool changes the tool list and resets the cache, and
   each search adds a model round trip (21 → 29 calls).
2. **Mixed can cost more than eager.** In `long`, `mixed` is 159k billed vs
   137k for `eager_all`: a 44-tool list that still changes 5 times pays cache
   writes on a large prefix each time.
3. **Tools leak across conversations.** In `two_sessions/on_demand`, 4 tools
   loaded by one conversation are sent to the other. Tools registered on first
   call go into the agent-wide registry, not the conversation's own tool list.

## Limitations

- The scripted model always finds and picks the right tool. Tool-selection
  accuracy with fewer visible tools needs real models (planned `pkg/evals` run).
- Token counts use the cl100k tokenizer, not each provider's own; the cache
  pricing model follows Anthropic's multipliers and ignores the minimum
  cacheable length and cache expiry.
- The fixture is synthetic; real MCP servers vary from ~100 to ~800 tokens
  per tool.
- The scenarios are short; folding and context-pressure relief do not trigger.
