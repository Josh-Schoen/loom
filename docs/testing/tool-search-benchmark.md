# Tool Search Benchmark Harness

**Version**: v1.4.0 · **Package**: `pkg/tools/toolbench`

Offline harness for comparing ways of getting tools in front of the model:
the registry's FTS5 retrieval, and the prompt-prefix cost of different
delivery strategies. It runs without an LLM or API key.

## Status

### ✅ Implemented

| Capability | What it measures |
|---|---|
| Retrieval quality | recall@k and MRR of `Registry.Search` in FAST mode (FTS5/BM25 only) against a labeled synthetic corpus |
| Retrieval latency | p50/p95 per query, and `BenchmarkSearchFast` for 50-400 tools |
| Prefix-cache model | bytes reused vs rewritten per request, cache breaks, and relative cost for four strategies |

Strategies simulated:

- `all-upfront` - every tool definition on every request.
- `search-append` - core tools plus `tool_search`; discovered tools are appended to the tools array.
- `dispatcher` - core tools, `tool_search` and a generic `call_tool`; schemas travel in the search result text, so the tools array never changes.
- `hybrid` - dispatcher, promoting a tool into the tools array after `PromoteAfter` uses.

### 📋 Planned (not measured)

- Tool-selection accuracy and argument accuracy per strategy (needs a live model).
- End-to-end latency and real cache-hit rates from provider usage fields.
- BALANCED and ACCURATE search modes (they add LLM calls; only the FTS5 stage is measured).
- Speculative prefetch of search results in parallel with the first model call.

## Running

```bash
# Tables (retrieval + strategy comparison at 50/100/200/400 tools)
go test -tags fts5 -race -count=1 -v -run 'TestReportScaling' ./pkg/tools/toolbench/

# Retrieval latency benchmarks
just bench-pkg pkg/tools/toolbench
```

## Model assumptions

The simulator is a model, not a measurement of any provider.

- A request is the concatenation tools, system, messages. The provider reuses the longest prefix shared with the previous request, counted in whole segments, with ideal breakpoint placement.
- Cache write costs 1.25x and cache read 0.1x of the base input price (`SimConfig.CacheWriteCost` / `CacheReadCost`); change them for other providers.
- Cache TTL and breakpoint-count limits are ignored. A cold or expired cache penalizes `all-upfront` most; the model does not capture that.
- A turn that needs an unavailable tool costs one extra request. A search whose top-k lacks the needed tool fails the turn, and the same tool is searched again the next time it is needed.
- Bytes stand in for tokens (about 4 bytes per token).
- The corpus is generated from templates (16 service domains x 5 nouns x 6 verbs, max 480 tools). Queries use verb synonyms that do not appear in descriptions, so the verb is ambiguous to BM25; recall falls as the corpus fills. Treat absolute numbers as indicative and use the harness to compare strategies or catch regressions.

## Results from the default run (30 turns, 70% of turns on 4 hot tools, top-5)

| Tools | recall@5 | `all-upfront` first request | requests (upfront / deferred) | cached cost vs uncached: upfront / append / dispatcher / hybrid |
|---|---|---|---|---|
| 50 | 1.00 | 24 KB | 60 / 67 | 13% / 18% / 13% / 18% |
| 100 | 1.00 | 47 KB | 60 / 69 | 13% / 19% / 13% / 19% |
| 200 | 1.00 | 94 KB | 60 / 71 | 12% / 22% / 13% / 18% |
| 400 | 0.91 | 185 KB | 60 / 69 (6 failed turns) | 12% / 22% / 13% / 16% |

What the model shows:

1. FTS5 retrieval takes about 1-2 ms per query at 50-400 tools. The cost of search is the extra model request it adds (+7 to +11 requests per 30 turns here), not the lookup.
2. With a warm prefix cache, `all-upfront` costs about the same as `dispatcher`. Deferred loading mainly shrinks the first request (1 KB vs 24-185 KB) and the context-window footprint.
3. `search-append` costs more than `dispatcher` because each discovery breaks the cached prefix (7-15 breaks per run, 100-240 KB rewritten).
4. `dispatcher` never breaks the prefix. Whether the model calls tools through `call_tool` as accurately as through native tool definitions is not measured here.
5. At 400 tools the 6 failed turns are searches whose top-5 missed the needed tool, so retrieval quality, not cache behavior, limits very large catalogs.
