# Tool-Context Benchmark (toolbench)

Measures how many tool definitions loom sends to the model on each call when
several MCP servers are connected, what that costs, and whether the planned
work still gets done when tools are loaded on demand. It exists so that
changes to tool loading are compared against a fixed baseline instead of
guessed.

**Status**: ✅ Deterministic benchmark and regression gate implemented
(`pkg/agent/toolbench_test.go`). 📋 Real-model runs (`pkg/evals`, Ollama
first) are planned, not implemented.

---

## What it runs

A scripted model plays fixed conversations against a real `Agent`. Every
provider call is recorded: the advertised tool list, the compiled messages as
loom sends them, and the model's reply. No API key, no network.

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

**Model behaviors**:

| Behavior | Meaning |
|---|---|
| `lenient` | After `tool_search`, calls the found tool by name even though it is not in the tool list it was given |
| `strict` | Calls only tools in the list it was given, as provider APIs constrain real models (OpenAI strict mode enforces it; other models generally comply); a tool it cannot get is skipped |

`strict` is the closer stand-in for a real model. `lenient` shows what loom's
fallback (the executor registering any indexed tool by name) makes possible.
`eager_all` runs only as `lenient`: every tool is already in its list, so the
model never searches and both behaviors play identically.

**Scenarios**:

| Scenario | Shape |
|---|---|
| `short` | 1 conversation, 1 turn, 2 tools from one server |
| `long` | 1 conversation, 10 turns, 11 tool uses across 4 servers, with reuse; a 10-minute pause at turn 6 (the prompt cache expires) |
| `two_sessions` | 2 conversations on one agent, interleaved, different servers |
| `long_fold` | 1 conversation, 24 turns, ~5k-token tool results, 64k context window: old history must be released |

## Metrics

| Metric | Meaning |
|---|---|
| `failed_turns` | Turns the agent could not complete (`first_error` holds the first error) |
| `tool_calls_ok` / `planned_tool_uses` | Planned tool uses that were made and succeeded |
| `provider_calls` | Model calls (searches add round trips) |
| `advertised_tools_max` | Largest tool list sent on one call |
| `tool_tokens_total` | Tokens of tool definitions summed over all calls (cl100k tokenizer) |
| `input_tokens_total`, `output_tokens_total` | Request and reply tokens summed over all calls |
| `cached_input_tokens` | Input served from the modeled prompt cache |
| `billed_tokens_modeled` | Cost in input-token equivalents: cache read 0.1x, cache write 1.25x, output 5x |
| `tool_set_changes` | Calls whose tool list differs from the conversation's previous call |
| `history_rewrites` | Calls with the same tool list whose messages do not extend the previous call (history released or folded) |
| `cross_session_leaks` | Distinct tools advertised to a conversation that were neither in the agent's first tool list nor used by that conversation |

**Cache model**: one prompt cache per agent, shared by its conversations (a
provider caches by prefix, not by conversation). A request's cached part is its
longest prefix (tool list, then messages in order) seen within the last 5
minutes; prefixes under 1,024 tokens are never cached. Simulated time: 20 s
before each user turn unless the scenario says otherwise, 5 s per call within a
turn. Every prefix of a request is treated as cached after it is sent, which is
more generous than real cache breakpoints for all setups alike.

**Comparing cost**: compare `billed_tokens_modeled` only between rows with the
same `tool_calls_ok`. A row that skipped work is cheap because it did less.

## Running it

```bash
just toolbench          # gate against the baseline, writes ./toolbench.json
just toolbench-update   # regenerate the baseline (intended changes only)
just toolbench-speed    # speed benchmarks, for benchstat
```

CI runs the gate as part of `go test ./pkg/...` (it is skipped under `-short`);
it takes about 10 s, about 28 s with `-race` (measured on 4 cores; the
`long_fold` scenario is most of it). That adds about a quarter to the
`pkg/agent` test run (103 s → 139 s with `-race` before these two savings:
one shared tool index for all rows and no duplicate `eager_all` rows).

**Gate rules**, per scenario, setup and behavior, against
`pkg/agent/testdata/toolbench/baseline.json`:

- `failed_turns` does not go up; every tool call made succeeds
- `tool_calls_ok` does not drop. If it rises, the row is reported as improved
  and not compared further (it is a different amount of work); lock it in with
  `just toolbench-update`
- otherwise `provider_calls`, `tool_set_changes` and `cross_session_leaks` do
  not go up, and `tool_tokens_total` does not go up by more than 2%

Message tokens and cost are reported with their change against the baseline
but not gated: an unrelated change (a longer system prompt) moves them.

The gate was checked by disabling change 1 below: it fails on the leaks, the
tool-list changes and the tool tokens of the `two_sessions` rows.

**Speed**: `toolbench-speed` times playing each scenario (agent setup and
measurement are outside the timer), building the advertised tool list, and
`tool_search`. Machine-dependent and not gated; compare two runs on the same
machine with `benchstat old.txt new.txt`.

## Baseline

| Scenario | Setup | Behavior | Failed turns | Done | Calls | Tools max | Tool tokens | Billed (modeled) | Tool-list changes | History rewrites |
|---|---|---|---|---|---|---|---|---|---|---|
| short | eager_all | lenient | 0 | 2/2 | 3 | 153 | 123,387 | 60,613 | 0 | 0 |
| short | on_demand | lenient | 0 | 2/2 | 5 | 6 | 4,764 | 13,085 | 2 | 0 |
| short | on_demand | strict | 0 | 0/2 | 3 | 4 | 2,295 | 4,738 | 0 | 0 |
| short | mixed | lenient | 0 | 2/2 | 3 | 44 | 34,749 | 17,772 | 0 | 0 |
| short | mixed | strict | 0 | 2/2 | 3 | 44 | 34,749 | 17,772 | 0 | 0 |
| long | eager_all | lenient | 0 | 11/11 | 21 | 153 | 863,709 | 187,636 | 0 | 0 |
| long | on_demand | lenient | 0 | 11/11 | 29 | 12 | 53,237 | 122,410 | 8 | 0 |
| long | on_demand | strict | 0 | 0/11 | 21 | 4 | 16,065 | 42,723 | 0 | 0 |
| long | mixed | lenient | 0 | 11/11 | 26 | 49 | 318,136 | 182,756 | 5 | 0 |
| long | mixed | strict | 0 | 4/11 | 21 | 44 | 243,243 | 78,016 | 0 | 0 |
| two_sessions | eager_all | lenient | 0 | 5/5 | 10 | 153 | 411,290 | 90,668 | 0 | 0 |
| two_sessions | on_demand | lenient | 0 | 5/5 | 14 | 6 | 14,063 | 26,986 | 4 | 0 |
| two_sessions | on_demand | strict | 0 | 0/5 | 10 | 4 | 7,650 | 11,156 | 0 | 0 |
| two_sessions | mixed | lenient | 0 | 5/5 | 12 | 46 | 140,222 | 65,594 | 2 | 0 |
| two_sessions | mixed | strict | 0 | 3/5 | 10 | 44 | 115,830 | 29,976 | 0 | 0 |
| long_fold | eager_all | lenient | 24 | 0/24 | 24 | 153 | 987,096 | 161,383 | 0 | 23 |
| long_fold | on_demand | lenient | 0 | 24/24 | 56 | 12 | 128,679 | 493,682 | 8 | 2 |
| long_fold | on_demand | strict | 0 | 0/24 | 48 | 4 | 36,720 | 109,524 | 0 | 0 |
| long_fold | mixed | lenient | 0 | 24/24 | 54 | 50 | 687,116 | 492,180 | 6 | 3 |
| long_fold | mixed | strict | 0 | 6/24 | 48 | 44 | 555,984 | 212,770 | 0 | 1 |

### What the baseline shows

1. **On demand does not work with a constrained model today.** With `strict`,
   `on_demand` completes 0 planned tool uses in every scenario, and `mixed`
   completes only the eager server's. `tool_search` returns the tools in its
   result text but never adds them to the tool list, so a model that may only
   call listed tools cannot use them. The `lenient` rows work only because the
   executor registers any indexed tool called by name.
2. **Loading every tool does not fit a small context window.** In `long_fold`
   (64k), `eager_all` fails every turn: the ~41k tokens of tool definitions
   leave too little room ("turn produced too many tool results to fit the
   context window"). `on_demand` and `mixed` complete all 24 tool uses
   (`lenient`).
3. **Cost, with equal work done** (`lenient`):
   - `long`: on demand 122k vs eager 188k (−35%); the pause re-writes the
     41k-token eager tool list to the cache.
   - `two_sessions`: on demand 27k vs eager 91k; with a shared cache the eager
     tool list is a cache read for the second conversation, so the gap is
     smaller than a per-conversation cache would suggest.
   - `long_fold`: on demand 494k vs mixed 492k: once history is large, the
     tool list is a small share, and mixed's 44 extra tools are offset by
     fewer searches.
   - On demand pays for it in round trips: 21 → 29 calls in `long`, 24 → 56
     in `long_fold` (each first use costs a search call).

## History of changes

The numbers in this section came from an earlier version of the benchmark
(one cache per conversation, no expiry, output not priced) and are not
comparable with the table above, only with each other.

**Change 1: tools loaded on first call belong to the conversation**
(`62c843c`). A tool the executor resolves from the index is recorded in the
calling conversation's own tool list before it enters the shared registry
(executor `DynamicRegistrationHook`, `Agent.scopeDynamicTool`); a second
conversation that calls it adds it to its own list (`Agent.adoptScopedTool`);
deleting a conversation drops its list. In `two_sessions`, cross-conversation
leaks went 4 → 0 (`on_demand`) and 2 → 0 (`mixed`), tool-list changes 7 → 4
and 4 → 2; single-conversation scenarios did not change.

## Limitations

- The scripted model always picks the right tool and its searches use the
  tool's own words, so search quality and tool choice with fewer visible
  tools are not measured. That needs real models (planned).
- Token counts use the cl100k tokenizer, not each provider's own; the cost
  model follows Anthropic's multipliers and treats every prefix as cacheable.
- The fixture is synthetic; real MCP servers vary from ~100 to ~800 tokens
  per tool.
- The benchmark agent has no prompt registry, so released history is dropped
  with an "(unsummarized)" marker instead of being summarized by the model;
  summarizing calls are not counted.
- Latency is not modeled; `provider_calls` is the proxy for it.
