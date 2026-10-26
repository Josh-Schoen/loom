---
title: "Spending Limit Reference"
weight: 47
---

# Spending Limit Reference

A monthly limit on the estimated cost of a `looms serve` process's LLM calls.
When the month's total reaches the limit, new turns are refused until the
month changes or the limit is raised. Providers that cost nothing (local
Ollama models) are never refused.

**Status**: ✅ Implemented (`pkg/llm/spending`, `looms serve`;
`MultiAgentServer` Weave and StreamWeave).

---

## Quick Reference

| Item | Value |
|------|-------|
| Setting | `server.spending.monthly_limit_usd` (float, US dollars) |
| Default | `0` (no limit; the total is still kept) |
| Ledger file | `$LOOM_DATA_DIR/spending.json` (mode 0600) |
| Month | Calendar month in the server's local time zone |
| Refusal | gRPC `RESOURCE_EXHAUSTED` with `google.rpc.ErrorInfo{reason: "SPENDING_LIMIT_REACHED", domain: "loom"}`; HTTP 429; SSE error frame with `"reason": "SPENDING_LIMIT_REACHED"` |

---

## Configuration

### server.spending.monthly_limit_usd

**Type**: `float64`
**Default**: `0`
**Required**: No
**Constraints**: `>= 0`; a negative value fails config validation.

**Behavior**:
- `0`: no limit. The month's total is still recorded in the ledger file.
- `> 0`: once the month's total is at or over the value, new turns are
  refused (see [Refusals](#refusals)).
- Read at startup. Changing it needs a server restart.

**Example**:

```yaml
server:
  spending:
    monthly_limit_usd: 20
```

---

## What is counted

Every LLM provider `looms serve` builds is wrapped by `spending.Wrap`: the
server default provider, per-agent `llm`, `compressor_llm` (graph memory
extraction) and `classifier_llm`, providers the agent registry builds
(runtime-created and hot-reloaded agents), the provider pool, providers made
by `SwitchModel`, and the skills-import classifier. Each call that returns a
response adds its `Usage.CostUSD` to the month's total.

`Usage.CostUSD` is the provider client's estimate from the token counts and
the per-million-token rates in `pkg/llm/catalog` (or the client's fallback
rates for models the catalog does not list). It is not the provider's bill:

- Anthropic cache reads are estimated at 0.10× the input rate; some models
  charge less (Claude Opus 5.5: $0.20 per million tokens against $4 input),
  so the estimate can be higher than the bill.
- The catalog keeps one rate per model. Claude Haiku 5.5 is listed at its
  rate for prompts up to 100K tokens ($0.10 / $0.50); longer prompts cost
  more ($0.50 / $2.50) and are under-estimated.
- Providers that report no cost (the scripted provider, local models) add 0.

Wrapping is idempotent (`Wrap` looks through `Unwrap()` for an existing
ledger), so a provider shared by several agents is counted once per call.

---

## Refusals

### Turns

`Weave` and `StreamWeave` check the ledger after resolving the agent and
before running it (`MultiAgentServer.admitTurn`). Over the limit they
return the error below, unless the agent's main LLM costs nothing (see
[Free providers](#free-providers)):

```
code: RESOURCE_EXHAUSTED
message: this month's AI spending limit of $20.00 has been reached ($20.13 spent in 2026-10); it resets next month or when the limit is raised
details: [google.rpc.ErrorInfo{reason: "SPENDING_LIMIT_REACHED", domain: "loom"}]
```

Over the HTTP gateway (`POST /v1/weave`) this is HTTP 429 with the details:

```json
{"code":8,"message":"this month's AI spending limit of $5.00 has been reached ($5.20 spent in 2026-10); it resets next month or when the limit is raised","details":[{"@type":"type.googleapis.com/google.rpc.ErrorInfo","reason":"SPENDING_LIMIT_REACHED","domain":"loom"}]}
```

On `POST /v1/weave:stream` (SSE) the refusal is one error frame. Error frames
now carry the gRPC code name and, when the status has an `ErrorInfo`, its
reason:

```
data: {"code":"ResourceExhausted","error":"rpc error: code = ResourceExhausted desc = this month's AI spending limit of $5.00 has been reached ($5.20 spent in 2026-10); it resets next month or when the limit is raised","progress":0,"reason":"SPENDING_LIMIT_REACHED","stage":"EXECUTION_STAGE_FAILED"}
```

### Calls inside a turn

A turn admitted under the limit runs to the end, even if the limit is
reached during it, so an answer is not cut off after tools have run. The
total can therefore pass the limit by the cost of the turns in progress.
Work started by an admitted turn with its context (graph memory extraction)
counts as part of the turn.

### Calls outside a turn

Calls not started from an admitted turn (scheduled workflows, judges,
background agents) fail with a `*spending.LimitError`
(`errors.Is(err, spending.ErrLimitReached)`) once the limit is reached.
Provider health checks are never refused.

### Free providers

A provider that implements `CostsNothing() bool` returning `true`, itself or
anywhere in its `Unwrap()` chain, is free (`spending.IsFree`). Implemented by
`ollama.Client`.

- Calls to a free provider are never refused, inside or outside a turn.
- A turn whose agent's main LLM (`GetLLMForRole(LLM_ROLE_UNSPECIFIED)`) is
  free runs over the limit. Its context is not admitted, so a paid provider
  it calls during that turn (a cloud `compressor_llm`, judge or classifier)
  still fails with `*spending.LimitError`.
- Free calls still pass through `Record`; they report `CostUSD` 0, so they
  add nothing.

---

## Ledger file

`$LOOM_DATA_DIR/spending.json`, written atomically after every counted call
and at startup:

```json
{
  "month": "2026-10",
  "spent_usd": 5.2,
  "limit_usd": 50,
  "reached": false
}
```

| Field | Type | Meaning |
|-------|------|---------|
| `month` | string | Month the total covers, `YYYY-MM`, server time zone |
| `spent_usd` | number | Estimated cost of this month's calls |
| `limit_usd` | number | The configured limit; `0` means none |
| `reached` | bool | `limit_usd > 0 && spent_usd >= limit_usd` |

A file for an earlier month starts over at 0. Other programs on the server
machine (a setup app) may read it to show the month's spending; only
`looms serve` should write it.

---

## Limitations

- One limit for the whole server, not per user or per agent.
- Only `MultiAgentServer` (what `looms serve` runs) admits turns; the
  single-agent `Server` does not.
- Totals are estimates (see [What is counted](#what-is-counted)); set the
  provider console's own budget alerts for a hard guarantee.
- The ledger is kept per data directory; two servers sharing one directory
  would overwrite each other's totals.

## See Also

- [HTTP Server Reference](http-server.md): SSE endpoint and error frames
- [Streaming Reference](streaming.md)
- [Anthropic Reference](llm-anthropic.md): cost estimation
