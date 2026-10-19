# Changelog

All notable changes to Loom will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Changed

- **`tools.none` keeps declared UI app tools** — under `tools.none: true`, an agent that lists a UI app tool in its YAML now gets the UI app tools (always offered); previously `tools.none` removed them from every agent, contradicting "the agent's tool list is what its YAML declares". Agents that do not declare them still get none.
- **`agents.install_bundled`** (default `true`) — `false` stops `looms serve` from installing the bundled `weaver` and `guide` agents and weaver skills, for servers whose users should only reach their own assistant (no shell access, no agent creation). With `tools.none: true`, a personal-assistant server exposes only the tools its agents declare.

- **UI apps belong to the user who created them** — dynamic apps now have an owner, the caller's user ID (for agent tools, the user who sent the message). `ListUIApps`, `GetUIApp`, `UpdateUIApp`, `DeleteUIApp`, `SetUIAppChecklistItem`, `/apps/`, and the UI app agent tools see the shared embedded apps and the caller's own apps only; another user's app answers as not found. Two users can each have an app with the same name, and the 100-app / 50 MB limits apply per user. Previously every user of a server saw and could change every app. Apps stored before this belong to `default-user` and stay where they are; other users' apps are stored under `$LOOM_DATA_DIR/apps/users/<key>/`. The MCP resource view (`loom-mcp`) has no user identity and shows `default-user`'s apps. Separation is enforced only when identity is (`server.auth.enabled` or `server.device_pairing.required`); otherwise the user ID is a header any caller can set. Go API: `server.AppProvider`, `server.AppSpecProvider`, `server.AppHTMLProvider`, and the matching `apps.UIResourceRegistry` methods take an owner argument. Reference: `docs/reference/mcp-apps.md#ownership`.

### Fixed

- **Token counting no longer downloads its encoding.** `pkg/agent`'s token counter fetched `cl100k_base` from `openaipublic.blob.core.windows.net` on first use; without network access it fell back to a characters/4 estimate for the life of the process (and the `TestBaseline_*` token tests failed). It now loads the encoding bundled by `github.com/pkoukk/tiktoken-go-loader` (MIT; its `cl100k_base.tiktoken` has the SHA-256 OpenAI's tiktoken verifies). The binary grows by about 7 MB.

- **Hot-reloaded agents keep their own models.** The agent hot-reload path in `looms serve` built the reloaded agent on the server's default provider, dropping its `llm`, `compressor_llm` and `classifier_llm`, and wrapped the shared default provider in another `InstrumentedProvider` on every reload (written from the watcher goroutine). Startup and reload now share `buildAgentLLMs`, and the shared provider is no longer changed.

- **Scripted provider: `after_tool` rules match with system notes present** — the provider treated the newest message as the newest turn, so a trailing system note (the new request context, a soft reminder) hid a tool result: `after_tool` rules stopped matching and tool-calling rules fired again in a loop. Trailing system messages are now skipped.

- **`get_ui_app` in agent YAML no longer logged as an unknown builtin tool** — declaring it under `tools` logged "Unknown builtin tool" although the UI app subsystem provides it.

- **`WeaveRequest.context` now reaches the model** — the field was documented as "context variables for prompt interpolation" but nothing read it. Its entries are now shown to the model as a system note on every LLM call of that request (`Context sent with this message ...`), never stored in the session or history, so a client can send facts like its `local_time` and `time_zone` (at most 16 entries; keys 64, values 256 characters). Reference: `docs/reference/streaming.md`.

- **Graph memory per-user follow-ups** — `GraphMemoryService.Forget` returned `INTERNAL` for a memory that does not exist or belongs to another user; it now returns `NOT_FOUND`. The skill-eviction salience boost ran without a user identity, so with per-user graph memory it reached only `default-user`'s memories; it now runs as the user whose session used the skill.

- **Sessions readable after a restart** — `GetSession`, `GetConversationHistory`, and `ListSessions` looked only at sessions loaded in memory, and a session is loaded from the store only when its next message arrives. After `looms serve` restarted, a client reopening a conversation got `NOT_FOUND` and an empty list although the next message continued it. They now fall back to the session store (owner-scoped, as before: another user's session still answers as not found). Reading does not load the session into memory.

- **Anthropic: current models no longer rejected** — the Anthropic client always sent `temperature` (default 1.0), which Claude Opus 4.7 and later Opus, Fable, and Mythos reject with a 400 (Sonnet 5.x and Haiku 5.x reject non-default values). It now omits sampling parameters for those models (`pkg/llm/anthropic/models.go`); older models are unchanged. On the Claude API endpoint, Claude Opus 5.5, Opus 5, Fable 5.1, and Sonnet 5.5 requests also send `"fallbacks": "default"` (beta `server-side-fallback-2026-07-01`), so a safety-classifier refusal is retried on another model instead of returning an empty reply. If the API rejects that option (an unknown `fallbacks` field or beta on the endpoint or account: a 400 naming either), the client sends the request again without it, logs a warning once, and leaves it out of later requests.

- **UI app runtime message handling** — the runtime treated any incoming message with an `id` as a response, including its own `ui/initialize` request when the page was not framed (a native web view), which set the trusted origin to `"null"` and made later `postMessage` calls throw. It now treats only messages with `result` or `error` as responses, never trusts a `"null"` origin, and skips the `ui/initialize` handshake when not framed.

### Security

- **Pinned TLS for paired devices.** `server.device_pairing.pinned_tls: true` makes the HTTP gateway serve HTTPS with a self-signed certificate made once and kept in `$LOOM_DATA_DIR/gateway-tls.{crt,key}` (ECDSA P-256), and puts its SHA-256 (`pin`, unpadded base64url) in pairing links and the new `CreatePairingCodeResponse.cert_pin`. Devices that trust exactly that certificate get encrypted traffic on a home network without a certificate authority, and nobody else on the network can pose as the server. A `public_url` must then be `https` and reach the gateway's TLS unchanged (e.g. Tailscale TCP forwarding). Reference: `docs/reference/device-pairing.md#pinned-tls`.

- **Required device pairing no longer accepts unverified bearers.** With `server.device_pairing.required: true` and `server.auth.enabled: false`, a bearer that was not a device token passed on to the legacy interceptor, which took the identity from `x-user-id` (`Grpc-Metadata-X-User-Id` over HTTP). Any caller that could reach the server could act as any user with `Authorization: Bearer x`. Such bearers are now rejected with `Unauthenticated` unless the Supabase-JWT authenticator runs after the device authenticator. The admin token still passes.
- **Device tokens are limited to the RPCs a phone needs.** A device token used to authorize every RPC except the DeviceService admin calls, including `TestMCPServerConnection` and `AddMCPServer` (which start a program on the host) and `CreateAgentFromConfig`. It is now accepted only for conversations (Weave, StreamWeave, sessions, history, client tool results, clarification answers), the caller's own UI apps and user memories, and `ListAgents`/`GetAgent`/`GetHealth`; other RPCs return `PermissionDenied` and need the admin token.
- **`get_ui_app` marks shared apps.** Anyone on the server can change a shared app, so its text can carry instructions from another person. Reading one now returns `shared`, `shared_by` and a note telling the agent to treat the text as content and not to share other apps or personal facts because of it. Agents also cannot share apps any more (see Shared UI apps under Added).
- **HTTP request bodies are capped at 4 MiB**, gRPC's default receive limit. Bodies were read before authentication with no limit.

- **Graph memory is now scoped per user on SQLite.** The SQLite graph memory store did not write or filter `user_id`, so when several people shared one agent, facts extracted from one person's conversations were recalled into another person's conversations, and ID-based operations (`Forget`, `Supersede`, `Consolidate`, `TouchMemories`, `Unrelate`, `GetLineage`, `Neighbors`, edge listing) acted on any user's rows. Every statement now writes and filters the caller's user ID (`types.UserIDFromContext`; an empty ID means `default-user`, so existing single-user data stays reachable). `Relate` and `Remember` reject links to another user's entities.
- **Graph entity names are unique per user.** `UNIQUE(agent_id, name)` on `graph_entities` becomes `UNIQUE(agent_id, user_id, name)` on both backends, so two people sharing an agent can each have an entity such as "mom". New migrations: SQLite `000009_graph_entities_per_user` (rebuilds `graph_entities`, keeping rows, edges, memory links, indexes and FTS triggers) and PostgreSQL `000024_graph_entities_per_user`. Rolling back fails while two users under one agent share an entity name.
- **PostgreSQL graph memory store matches the same contract.** It already relied on RLS; it now also carries explicit `user_id` predicates, checks entity ownership before creating edges or memory links (foreign-key checks bypass RLS), and creates referenced entities under a fresh ID when the requested ID belongs to another user.
- `Forget`, `Supersede`, `GetMemory` and `Consolidate` return the new sentinel `memory.ErrNotFound` (wrapped) for a memory that does not exist or belongs to another user, instead of succeeding without effect.
- SQLite now stores `graph_memories.expires_at` in the same text format as `datetime('now')`. A TTL set through `Memory.ExpiresAt` that had passed earlier the same day was treated as still active.

- **`/v1/weave:stream` (HTTP/SSE) now authenticates and identifies callers** — the SSE handler calls the service in-process and previously skipped the gRPC interceptors, so with `server.auth.enabled: true` SSE turns ran unauthenticated and, in every mode, without a user identity. `looms serve` now runs the same stream-interceptor chain on it, with headers mapped to metadata by the gateway's rules (`Authorization`, `Grpc-Metadata-*`). Behavior change: SSE requests now need the same credentials as gateway routes — `401` without a valid bearer token when auth is enabled, and `401` without `Grpc-Metadata-X-User-Id` on PostgreSQL storage with `require_user_id: true`; on SQLite storage they run as `default-user`. Sessions created earlier with no owner stay reachable when ownership enforcement is off. See `docs/reference/http-server.md#sse-authentication-and-identity`.

- **`/apps/` pages now require the same credentials as the API** — the browser-facing UI app pages skipped the gRPC interceptors, so with `server.auth.enabled` (or the new `server.device_pairing.required`) anyone who could reach the HTTP port could read every app, including ones built from personal data. They now run the authentication and user-identity interceptors as `GetUIApp`/`ListUIApps`. Unchanged when no authentication is configured. On PostgreSQL storage with `require_user_id: true`, these pages now also need the user-id header.

### Added

- **`run_script` tool** — agents that list `run_script` in `tools.builtin` can run a short Starlark calculation script and get its exact result (`pkg/sandbox/script`). Each script runs in a child `looms sandbox-run` process with an empty environment, no file, network, process, module, clock or random access, and limits of 10M steps, 5 s, 256 MB heap and 64 KiB output; a script that exhausts memory ends the child, not the server. Reference: `docs/reference/run-script.md`; design and Lua-vs-Starlark measurements: `docs/plans/sandboxed-script-execution.md`.

- **Monthly spending limit** — `server.spending.monthly_limit_usd` refuses new turns with `RESOURCE_EXHAUSTED` (HTTP 429; ErrorInfo reason `SPENDING_LIMIT_REACHED`) once the month's estimated LLM cost reaches it. Every provider `looms serve` builds is counted (`pkg/llm/spending`); the month's total is kept in `$LOOM_DATA_DIR/spending.json` even without a limit. A turn admitted under the limit finishes. `/v1/weave:stream` error frames now also carry `code` and, when present, `reason`. Reference: `docs/reference/spending-limit.md`.
- **Claude Opus 5.5 and Claude Haiku 5.5 in the model catalog**, with their prices ($4/$20 and $0.10/$0.50 per million tokens), so their costs are no longer estimated with fallback rates.

- **Shared UI apps** — a dynamic app can be shared with everyone on the server, who all see and change it (a household's grocery list): `CreateUIApp` takes `shared`, and the new `SetUIAppSharing` RPC (`POST /v1/apps/{name}:set-sharing`) shares or unshares an existing app. Agent tools cannot share: `create_ui_app` always makes the person's own app, so sharing needs a client call the person agreed to. `UIApp.shared` and `UIApp.shared_by` report it; only the person who shared an app can delete or unshare it; a shared app's name cannot be used by anyone's own app. `looms pair --user` now rejects user IDs with control characters, as the user-ID header already did. Reference: `docs/reference/mcp-apps.md#shared-apps`.
- **What an agent remembers about the caller** (`ListUserMemories`, `AddUserMemory`, `CorrectUserMemory`, `ForgetUserMemory`, `ForgetAllUserMemories` on `LoomService`; `GET/POST/DELETE /v1/agents/{agent_id}/memories[/{memory_id}]`, `POST …/{memory_id}:correct`) — lets a client show a person their own graph memories for an agent and add, correct (supersede), or forget them, or forget everything. Scoped to the caller's user ID; `FAILED_PRECONDITION` when graph memory is off for the agent. Reference: `docs/reference/user-memories.md`.
- `GraphMemoryStore.ListMemories(ctx, agentID, limit, offset)` lists the caller's active memories (not forgotten, expired or superseded), newest first, with a total count.
- `GraphMemoryStore.ForgetAll(ctx, agentID)` forgets all of the caller's memories under an agent and deletes the caller's entities and edges under it. Memory content is soft-deleted (`expires_at`), as with `Forget`.
- **Tappable checklists on UI apps** — new `checklist` component (`{id, label, detail?, checked?}` items; the component needs an `id`). Tapping a row toggles it and sends `ui/notifications/checklist-changed` to the host; the new `SetUIAppChecklistItem` RPC (`POST /v1/apps/{name}:set-checklist-item`) saves the tick in the app's spec. Apps created or updated from a spec now store the spec next to the HTML. New agent tool `get_ui_app` returns the current spec so an agent can update a page without losing ticks; it is offered with the other UI app tools. Reference: `docs/reference/mcp-apps.md#checklist-state`.

- **Agent-created UI apps persist across restarts** — `looms serve` stores dynamic apps in `$LOOM_DATA_DIR/apps/` (one JSON file each, mode 0600) and reloads them at startup; previously they were lost on restart. New `UIApp.updated_at`. Reference: `docs/reference/mcp-apps.md#persistence`.
- **UI app tools can be always offered** — an agent that lists `create_ui_app` (or another UI app tool) in its YAML tools now has all the UI app tools from the first turn; previously the YAML entry was ignored and the tools appeared only after UI keywords ("dashboard", "chart", ...). Other agents keep keyword-triggered disclosure.
- **Scripted provider: structured tool arguments** — `call_tool.arguments` accepts any YAML value; strings at any depth are templates, other scalars keep their type, so scripts can call tools that take objects such as `create_ui_app`.
- **`server.device_pairing.link_base`** — builds pairing links on an https URL (`<link_base>#code=...&server=...`) instead of the `loompair://` scheme, so a QR code can open the app through an associated domain, or a download page when the app is not installed. The code stays in the fragment, which browsers do not send to the web host.
- **`looms devices list --json`** — prints the paired devices as `loom.v1.ListDevicesResponse` JSON, so setup apps and scripts need not parse the table.
- **Device pairing** (`server.device_pairing`, `looms pair`, `looms devices`) — connect a phone or desktop app to a self-hosted server by scanning a QR code. `looms pair` issues a single-use code (default 10 min, max 1 h) and prints it as a QR code and link carrying the server address; the device redeems it at `POST /v1/pairing:redeem` for its own revocable bearer token (`loomd_…`), accepted on gRPC, gateway routes, and `/v1/weave:stream`. Codes and tokens are stored only as SHA-256 hashes; devices persist to `$LOOM_DATA_DIR/devices.json`; admin RPCs need the token in `$LOOM_DATA_DIR/device-admin.token`. Off by default; with `required: false` (default) unpaired clients keep working. New `DeviceService` in `proto/loom/v1/devices.proto`. The gateway is plain HTTP, so tokens are unencrypted on the network unless `public_url` points at an HTTPS proxy or VPN (the server warns). Single server process only (file store). Reference: `docs/reference/device-pairing.md`; design: `docs/architecture/device-pairing.md`.
- **Scripted LLM provider** (`llm.provider: scripted`, `llm.scripted_script_path`) — answers from a YAML script of regex-matched rules (text replies or tool calls, with templates over the user message and tool results) instead of a model, so `looms serve` runs end to end with no API key and deterministic replies for client development and tests. Streams through the existing loadtest mock provider. Server-level only. Reference: `docs/reference/llm-scripted.md`.
- **Client-executed tools** — a `StreamWeave` caller can declare tools it executes itself (`WeaveRequest.client_tools`; device APIs, browser, local files). The agent is offered them for that call only; a call is handed to the caller as `WeaveProgress.client_tool_call` and the turn holds until `SubmitClientToolResult` (`POST /v1/sessions/{session_id}/client-tool-results`) returns the result or `client_tool_timeout_seconds` elapses (`CLIENT_TOOL_TIMEOUT`). Results are accepted once and only from the stream's user. Timeout policy: `server.client_tools.default_timeout_seconds` (300) / `max_timeout_seconds` (900). Implemented in `looms serve`; the single-agent server rejects `client_tools` with `UNIMPLEMENTED`. Reference: `docs/reference/streaming.md#client-executed-tools`; design: `docs/architecture/client-tools.md`.

## [1.4.0] - 2026-08-12

### Breaking Changes

- **Skills and patterns move from push to pull (#278)** — the per-turn skill discovery-and-activation push and the pattern injection channel are removed. Skills load on demand via the new `manage_skills` builtin (`list`/`load`); patterns load via `load_pattern`. The findings channel and the `record_finding` tool are removed. `maxL1Tokens` no longer triggers compaction (compaction is budget-based; the field remains validated config).
- **Context machinery rewritten around compile-per-call (#288)** — rendering decisions (full/stub/offload/eviction/summary fold) are made at every LLM call. Large tool results offload at one 16 KiB threshold (16384 bytes) as a pure render condition of compilation; results render as a verbatim string or JSON, never Go map syntax. Advertised tools, error/SQL-result/shared-memory stores are per-session.
- **User-turn timestamps left the message body (#306)** — stored user `Content` no longer carries a `[Mon 2006-01-02 15:04 MST] ` prefix; clients see the user's words verbatim. Arrival stamps render into the compiled LLM view only (from the durable `Timestamp`), and graph-memory extraction anchors on `Timestamp` instead of scanning content.
- **`DeleteSession` semantics (#146)** — returns success for a session that exists only in the persistent store (previously `NotFound`), so clients can clean up sessions already evicted from memory.
- **Go 1.26 toolchain (#271)** — required for source builds.
- **10 new database migrations** (`000014`–`000023`: analytics views, RLS infrastructure and JWT-sub honoring, tool-outcome policy/admission columns, context compilation, human-request store) apply automatically on first start of the upgraded server. Back up databases before upgrading.

### Added

#### Context Compilation (#288, #297)
- Compile-per-call context: arrival appends messages in full; a single compilation algorithm decides at every LLM call what renders full, what renders as an offload/eviction stub, and when conversation folds into the summary. One 16 KiB threshold, proactive relief.
- Configurable offload-exempt tool set (`SetOffloadExemptTools`) so chosen tools' results never render as stubs. No tools are exempt by default.

#### Human-in-the-Loop: Approval Gates & Tool Admission (#269, #300)
- Declarative approval gates for pipeline/iterative workflows: `PipelineStage.hitl_gate` fires deterministically after stage output validates, with durable checkpoint/resume — no LLM decides whether to ask.
- Tool-admission hook library: config-built bindings (`gated-allowlist` / `denylist` / `audit` / `ask`) with dot-path matchers over call params are evaluated against every tool call before execution (unmatched calls pass through); most-restrictive verdict wins; a config that fails to compile aborts serve startup rather than running ungoverned (fail-closed).
- `ask` verdicts hold the turn on a human-request store (SQLite + PostgreSQL) with expiry; admission decisions are audited on tool outcomes.

#### Context & Skill Management (#278)
- `manage_skills` builtin (`list`/`load`): a load activates the skill for the session, registers its required tools for same-turn use, and delivers the skill body into the conversation. Bound skills render name+description once into the system prompt at session creation.
- Budget-based compaction using the LLM compressor when a prompt registry is configured (preserves decisions/approvals with scope, open commitments, reload pointers); heuristic summarizer remains the fallback.

#### OTLP Tracing (#255)
- `OTelTracer` exports spans to any OTLP/HTTP backend (Opik, Jaeger, Tempo); Loom span attributes translate to OTel `gen_ai.*` semantic conventions; `mode: otel` in observability config plus auto-select from `OTEL_EXPORTER_OTLP_TRACES_*` env vars; credential and PII redaction default-on for exported spans. OTLP/gRPC is not implemented.

#### Multimodal User Turns (#249)
- `ChatWithContentBlocks`: user turns carry text + image content blocks end-to-end (agent → provider), with the canonical text preserved on the stored turn.

#### Replay/Import Arrival Time (#308)
- `WeaveRequest.occurred_at`: replayed or imported conversations anchor every persisted row (user turn, assistant reply, tool rows) at their historical time. Gated by `server.allow_time_override` (default off); future-dated values rejected.

#### Session Artifact Metadata & ListSessions API (#146)
- Opt-in session `metadata.json` (config `artifacts.session_metadata_enabled`, env `LOOM_ARTIFACTS_SESSION_METADATA_ENABLED`; default **off**) colocating `agent_name`, `ended_at`, `metadata_status`, `artifact_count`, and allowlisted attribution context next to a session's artifacts. Disk I/O stays off the hot path and is zero-cost when disabled.
- `ListSessions` pagination (`limit`/`offset`; server default page size 50, max 500) plus `metadata_status` and `project_id` filters (filters require the flag and read `metadata.json` per session).
- New `Session` fields returned by `ListSessions`: `agent_name`, `ended_at`, `metadata_status`, `artifact_count`.

#### Other
- OpenAI client: `ExtraHeaders` config forwarded on requests (#265)
- Release pipeline publishes the homepage (`web/` → gh-pages) with a version-stamp check (#275)

### Fixed
- **Temporal grounding leak (#306)** — timestamps no longer leak into user-visible message bodies; per-turn grounding moves to the compiled view (see Breaking Changes)
- **Bedrock** — SDK tool mappings isolated per request (concurrent-session corruption) (#301); canonical provider identity (#302)
- **Workflow loading** — parallel workflow tasks load from the canonical schema (#298); canonical fork-join pattern type accepted (#307); all canonical pattern types load in the registry (#310)
- **Security** — SQL result table names sanitized at every use (#267); Snyk triage: Go 1.26 toolchain, artifact path containment, postMessage origin guard (#271); session metadata path containment provable to CodeQL (#277); tainted path values guarded directly with `filepath.IsLocal` (#309)
- **TUI** — no hardcoded default model shown at startup (#240); readline keys (ctrl+a/e/k/u/w) freed in chat input (#241)
- **Quickstart** — Bedrock API-key auth + non-exhaustive model picker (#239)
- **CI** — unit-test de-flake: SSE happy path, live web-search calls removed (#242)
- **Packaging** — Homebrew tap publishing repaired (broken since v1.0.1; publishers now dispatched explicitly) (#272); repo templates are the single source of truth for Homebrew formulas (#274)

### Changed
- `ListSessions` pagination is **opt-in**: a request that sets neither `limit` nor `offset` still returns every session (no silent truncation). Setting either parameter applies the server-side default page size and 500-row cap.
- buf remote plugin versions pinned to the committed codegen (#296)
- Dependency updates: Go toolchain 1.26, golang.org/x/crypto, excelize, and CI action bumps (Dependabot)

## [1.3.0] - 2026-06-01

### Added

#### Salience-Driven Graph Memory (#133, #137, #159)
- **GraphMemoryService** — 15 RPCs: entity/edge CRUD, `Remember`/`Recall`/`Forget`/`Supersede`/`Consolidate`, `ContextFor` composite queries, `GetGraphStats`
- Salience scoring with exponential time decay (`S = S_base * decay_rate^days`), boost operations, and per-memory-type token budget allocation
- Auto-extraction subprocess — LLM extracts entities/relationships/memories asynchronously; fires on user input and on a tool-execution cadence (default every 5 tool calls)
- `graph_memory` tool auto-injected when a graph memory store is configured; enabled by default (opt-out via `enable_extraction: false`)
- Event-date anchoring — extractor emits absolute ISO dates (`YYYY-MM-DD`) with confidence levels (`exact`/`approximate`/`ambiguous`); retrieval prepends `[YYYY-MM-DD]` to the context block ⚠️ internal Go layer only — see Limitations
- SQLite (migration `000002_graph_memory`) and PostgreSQL (migration `000010_graph_memory`) storage with FTS5 full-text search, soft deletes, and user-scoped RLS

#### LLM-Assisted Task Decomposition & Agent Kanban (#141)
- **TaskService** proto — Task/TaskBoard/TaskLane messages, `TaskStatus`/`TaskPriority`/`TaskCategory`/`TaskDependencyType`/`DecomposeStrategy` enums, 19 RPCs defined (⚠️ 11 of 19 served — see Limitations)
- `task_board` tool auto-injected into the agent loop (10 actions: decompose, ready, claim, update, close, create, list, show, add_dep, board)
- Task Manager business logic: DFS cycle detection, auto status propagation, auto block/unblock, WIP limits, compaction
- LLM-assisted Decomposer with backward/forward/parallel strategies, retry-on-invalid-JSON, and rollback on materialize failure
- Dependency-aware storage: SQLite (`000003_tasks`, `000008_task_idempotency`) with FTS5; PostgreSQL (`000011_tasks`, `000013_task_idempotency`) with RLS
- `OutputRetryPolicy` proto in `collaboration.proto` — `CONTINUE`/`FRESH`/`ESCALATE` retry-session modes with feedback templates and cooldown
- Live task context injected into the agent system prompt each turn; message-bus events for task lifecycle (created/claimed/released/completed/blocked/updated)

#### Skills System Expansion (#174, #182, #183, #184, #195)
- Declarative skill **bindings** (`SkillBinding`/`SkillBindingMode`) with exact-name, glob, and label-selector matching; replaces `enabled_skills`/`disabled_skills` (backward-compat shim retained)
- Hierarchical **PageIndex-style router** (`SkillIndexNode`/`SkillIndex`) for LLM-driven skill discovery with session caching (migration `000007`/`000012` skill_index)
- **Skill task emission** — author-defined decompositions (`SkillTaskTemplate`) with decomposer fallback; idempotent materialization via skill idempotency keys
- End-of-turn **task-board hygiene** auditor (`HygieneConfig`/`HygienePolicy`: `REQUIRE_FIX`/`AUTO_FIX`/`WARN_ONLY`)
- **SkillsImportService** gRPC (`BulkImportSkills`/`AddSkill`/`ClassifySkill`) with post-write router reload
- Import of Anthropic-style Agent Skill directories with cross-skill references and keyword synthesis
- `SkillRiskLevel` (`LOW`/`MEDIUM`/`HIGH`/`RESTRICTED`) recorded per skill; HIGH/RESTRICTED skills are blocked when `tools.permissions.require_approval=true` ⚠️ interactive approval flow not yet implemented

#### Self-Healing Error Recovery (#177)
- **3-tier recovery** in the conversation loop, enabled by default (opt-out via `behavior.enable_self_healing: false`):
  - Output-token circuit breaker → trims broken turns, injects a recovery nudge, continues
  - Tool circuit breaker → disables the failing tool locally, returns a synthetic disabled result, continues with remaining tools
  - Token-budget pressure → aggressive trim (keep last 4 messages, clear summaries) when budget exceeds threshold after normal compression
- One-shot recovery per error type per conversation prevents infinite retry loops
- `RecoverableError` (type + recovery action + payload) surfaced to upper layers when self-healing is exhausted
- Trim operations respect `tool_use`/`tool_result` pair boundaries; recovery paths emit observability span events

#### Load Testing Infrastructure & Throughput Optimization (#139)
- Load-testing framework (2,484 lines): zero-token mock LLM provider + gRPC harness with configurable latency, error injection, and streaming
- Per-layer token-count caching reduces `AddMessage` from O(n) to O(1)
- Channel-based encoder pool replaces the mutex on `TokenCounter` (tunable via `LOOM_TOKEN_ENCODER_POOL_SIZE`)
- Read-lock fast path for session cache hits; `SegmentedMemory` built outside the lock
- Measured on Apple M4 Pro (14 cores): **2,200 req/s peak at 160 workers** (concurrency scaling), **11,023 req/s on fresh sessions vs 187 req/s on a shared session** (59x contention gap, by design); throughput flat ~2,400 req/s across 100–10,000 sessions
- 15 load/scalability scenarios (concurrency, multi-turn, memory pressure, session contention, fresh vs reused)
- `just load-test`, `just load-test-bench`, `just load-test-perf` targets

#### LongMemEval Benchmark & Judge Criteria Fix (#159)
- `loom-longmemeval` CLI harness — evaluates graph memory across LongMemEval question categories with ingest, multi-session, and context-stuffing modes (`download`/`run`/`score`/`info` subcommands, `--types`/`--isolate` flags)
- **LLM judge now honors `JudgeConfig.Criteria`** — routes to a generic criteria-driven prompt instead of a hardcoded SQL-evaluation template when `Evidence.Criteria` is set
- Vector embedding infrastructure: `Embedder` interface + OpenAI implementation; SQLite brute-force `VectorRecall` with cosine similarity (migration `000005_embedding_column`) ⚠️ PostgreSQL pgvector path stubbed — see Limitations
- Configurable `extraction_timeout_seconds` (default 30s) and tool-result filtering for graph memory extraction

#### Span Exporter & Workflow Session Continuity (#132)
- **SpanExporter** interface (`ExportSpans`/`ForceFlush`/`Shutdown`) for exporting traces to external backends; `WithSpanExporter` and `WithDefaultResourceAttributes` options on `EmbeddedTracer`
- Workflow session continuity — `ScheduledSessionMode` (`NEW`/`RESUME`); RESUME mode uses a stable `workflow_id` for deterministic agent sessions across scheduled runs
- `ContextWithTraceID` for explicit trace-ID propagation; workflow-id resolution across all orchestration executors

#### Agent Presets & Workflow Templates
- `AgentPreset` enum (8 presets) and `WorkflowTemplate` enum (7 templates) with defaults registries
- RPCs: `ListAgentPresets`, `ListWorkflowTemplates`, `CreateWorkflowFromTemplate` (idempotent by agent name) wired into `MultiAgentServer`

#### Local-Ollama Profile & Tool-Surface Controls (#193)
- Per-role LLMs (`compressor_llm`, `classifier_llm`) now wired in the static-YAML server loader, not only the gRPC path; finding extraction routed through the compressor LLM when set
- `llm.concurrency_limit` config knob (default 2) replaces the hardcoded limit — set to 1 for single-slot local Ollama
- `tools.none` config knob — agent sees only the tools its YAML declares (suppresses the auto-registered minimal/communication/orchestration tool surface)
- `examples/config/local-ollama.yaml` and an Ollama researcher reference agent

#### Teradata Patterns (#142)
- Expanded Teradata patterns from 34 to **84** YAML files across new ML/analytics categories (AI functions, association, BYOM, data prep, geospatial, hypothesis testing, ML, model evaluation, text, timeseries, vector search)
- 4 new Teradata skills: data prep, ML pipeline, SQL analytics, vector RAG

#### Bedrock Bearer-Token Authentication
- Optional bearer-token auth for Amazon Bedrock (IAM Identity Center / SSO) that bypasses SigV4, supplied via CLI/env/keyring as `bedrock_bearer_token` (runtime only, not persisted to config files)
- Note: an earlier iteration of this feature was reverted (a34a5cb) and has since been re-introduced and wired end-to-end (`pkg/llm/bedrock` → factory → config)

### Fixed

- **Compression boundary** (#162) — `adjustCompressionBoundary` maps tool-call groups and iterates until stable so compression never splits an `assistant(tool_calls)` message from its `tool_result` pair
- **Pipeline partial results** (#162) — pipeline executor emits stage output and metadata (thinking, cost, duration, tokens) incrementally after each stage instead of only at the end
- **LLM catalog wiring** (#156) — catalog (73 models) is now authoritative for output cap and context window across all providers via a `Source` chain
- **Ollama healthcheck & timeout** (#188) — fixed healthcheck handling; default timeout is now provider-driven instead of a hardcoded 60s (see Changed)
- **MCP `loom_weave` routing** (#145) — agent routing surfaced to MCP clients; agent id/name preamble prepended to tool responses
- **Graph memory hot-reload** (#137) — corrected hot-reload wiring and default config; entities track roles/user markers to reduce entity confusion
- **Visualization styleguide** (#144, #157) — fails fast on an unavailable styleguide endpoint (returns an error instead of a silent fallback) and uses the zap logger for the fallback path
- **Skills slash commands** (#195) — unicode base-splitting so multi-byte delimiters parse correctly
- **Quickstart shell detection** (#179) — reads the `SHELL` environment variable instead of guessing from the script runtime
- **Chocolatey package icon** — restored the `iconUrl` (removed in v1.0.x when it pointed at a non-existent file) using a committed `packaging/icon.png` served via a tag-pinned jsDelivr CDN URL; the version manager keeps the pinned tag in sync on every bump
- **Chocolatey package install** — the pattern library is now bundled inside the package instead of downloaded at install time, removing an unvalidated remote download flagged by Chocolatey moderation (the patterns archive was fetched via `Url64bit` with no `Checksum64`). Also corrected the bundled binary SHA256 checksums, which did not match the published v1.3.0 Windows artifacts and would have failed verification at install
- **Test hermeticity** — `pkg/shuttle/builtin` web-search tests now clear ambient search-provider API keys (`TAVILY_API_KEY`, etc.) via `TestMain`, so results no longer depend on the developer's environment
- **`.gitattributes`** (#194) — enforces LF line endings to eliminate false diffs on Windows clones with `autocrlf=true`

### Changed

#### Breaking Changes
- **IntentCategory enum removed** (#137) — patterns use freeform `intents` strings; the enum constants are gone (YAML fallback preserved). Pattern YAML must declare `intents`.
- **Ollama default timeout** (#188) — zero/unset now means provider-driven instead of the previous 60s fallback. Configs relying on the implicit 60s behave differently; set an explicit timeout to preserve old behavior.
- **LLM catalog model IDs** (#156) — the legacy short-prefix map for `claude-opus-4`/`claude-sonnet-4`/`claude-haiku-4` was removed; catalog-exact model IDs are now required.
- **Graph memory extraction default-on** (#137) — when graph memory is enabled, the extraction subprocess runs by default, which increases LLM-call volume in existing deployments. Opt out with `enable_extraction: false`.
- **Skills config** — `enabled_skills`/`disabled_skills` deprecated in favor of `bindings[]` (compat shim retained); skills emit tasks by default — opt out with `emit_tasks: false` (per skill) or `tasks_enabled: false` (agent).
- **Auto-applied DB migrations** — SQLite `000005` (embedding) and `000006` (event_date) apply automatically on first start of the upgraded server. Back up databases before upgrading.

#### Dependency Updates
- google.golang.org/grpc: 1.79.3 → 1.81.1
- github.com/grpc-ecosystem/grpc-gateway/v2: 2.28.0 → 2.29.0
- github.com/anthropics/anthropic-sdk-go: 1.26.0 → 1.45.0
- github.com/aws/aws-sdk-go-v2/service/bedrockruntime: 1.50.2 → 1.52.0 (+ aws-sdk-go-v2 core/config/credentials)
- github.com/jackc/pgx/v5: 5.8.0 → 5.9.2
- charm.land/bubbletea/v2: 2.0.2 → 2.0.6; bubbles/v2 2.0.0 → 2.1.0; lipgloss/v2 2.0.2 → 2.0.3
- github.com/go-jose/go-jose/v4 (#138), go.opentelemetry.io/otel otlptracehttp (#147), and grouped go-minor-patch updates (#140, #155, #165, #176, #192)
- CI action bumps: codeql-action, codecov-action, golangci-lint-action, gosec, release-downloader, action-gh-release

### Testing

- **3739 test functions** across 393 test files (up from 3112/319 in v1.2.0)
- **Zero race conditions** — tests run with the `-race` detector (`-tags fts5`)
- Self-healing recovery: 12 tests including concurrent-access race coverage
- Span exporter: 16 tests (export errors, shutdown, resource attributes, concurrent export)
- Task system: 31 tests (store, decomposer, emitter, tool, manager integration, idempotency)
- Skills: binding/router/emitter/hygiene/import suites + unicode slash-command parsing
- Load testing: mock provider (21 tests) + harness/scalability/profiling suites; all consume zero LLM tokens

### Known Limitations

- **Graph memory event dates over gRPC** — `event_date`/`event_date_confidence` are not yet fields on the proto `GraphMemory` message and are not round-tripped by `convert.go`; event-date anchoring works via the internal Go layer only, not for gRPC clients.
- **Graph memory conversation-turn cadence** — `conversation_extraction_cadence` parses from config but is currently inert; only user-input and tool-execution triggers are active.
- **TaskService gRPC surface** — 11 of 19 RPCs are served. `UpdateTask`, `DeleteTask`, `ReleaseTask`, `TransitionTask`, `AddDependency`, `RemoveDependency`, `GetBlockedTasks`, `DecomposeTask`, and `GetTaskContext` are defined in proto but not yet implemented; decomposition is available via the `task_board` tool action.
- **Vector recall** — SQLite `VectorRecall` is brute-force O(n) (scales to ~100K memories/agent); the PostgreSQL pgvector path is stubbed and returns not-implemented.
- **Skill risk levels** — recorded and used for block-when-`require_approval`; an interactive HITL approval flow is not yet implemented (`tools.permissions` returns a not-implemented error unless `yolo=true` or the tool is allow-listed).

## [1.2.0] - 2026-03-19

### Added

#### Skills System (#117)
- **LLM-agnostic skills** — Activatable behaviors combining prompt injection, tool preferences, and trigger conditions (slash commands, keyword auto-detection, always-on)
- Proto-first: `skill.proto` defines Skill, SkillsConfig, SkillActivationMode
- `pkg/skills`: Library (discovery+caching), Orchestrator (activation+budget), Loader (YAML parsing), HotReloader (fsnotify), Format (token budgeting)
- Agent integration: skill matching/activation in conversation loop with token budget control
- MCP bridge: 5 skill management tools (list, get, create, activate, deactivate)
- 73+ tests with race detector, concurrent stress tests (33 goroutines)

#### Per-Role LLM Providers (#100)
- **Different LLM providers per operational role** (judge, orchestrator, classifier, compressor) with fallback to main agent LLM
- New proto fields: `judge_llm`, `orchestrator_llm`, `classifier_llm`, `compressor_llm` on AgentConfig
- Role-aware `SwitchModel` RPC (backward compatible when role=UNSPECIFIED)
- Real `GetHealth` with per-role LLM ping checks (replaces hardcoded stub)
- `ValidateProviders` startup preflight wired into server startup

#### Provider Pool & A/B Testing (#100)
- **Provider pool** with round-robin, weighted, and A/B test routing strategies
- **Prompt caching** support across providers
- **Rate limiting** with token bucket algorithm, burst capacity, and queue timeout

#### PostgreSQL Storage Backend (#85)
- **Native PostgreSQL as storage backend** with pgx/v5 driver, connection pooling, RLS, tsvector FTS, JSONB, and embedded SQL migrations
- `StorageBackend` abstraction enabling both SQLite (default) and PostgreSQL with zero breaking changes
- New RPCs: `GetStorageStatus` and `RunMigration`
- All concrete `*SessionStore`/`*SQLResultStore` references refactored to interfaces

#### Multi-Tenancy (#85)
- **User-scoped isolation** with Row Level Security and gRPC interceptors
- Auth via `X-User-ID` gRPC metadata header with configurable strict/lenient modes
- `AdminService` for tenant management and security hardening

#### Supabase Backend (#91)
- **Supabase execution backend** with session/transaction pooler modes and automatic prepared statement handling
- RLS context injection via JWT/role claims
- Loom internal storage can now run on Supabase PostgreSQL (verified with PostgreSQL 17.6)
- Viper env var fix: `SetEnvKeyReplacer(".", "_")` for nested config like `LOOM_STORAGE_POSTGRES_DSN`

#### MCP Server & Apps (#73)
- **MCP server** with stdio transport, app management, and `loom-mcp` bridge binary
- 12 gRPC RPCs implemented on MultiAgentServer: pattern management (LoadPatterns, ListPatterns, GetPattern), agent lifecycle (CreateAgentFromConfig, etc.), and observability
- 71 unit tests, all passing with `-race`

#### Context State & Model Catalog (#118)
- **ContextState** proto message — per-session context window reporting (active_pattern, tokens used/max, ROM, tools loaded)
- `WeaveRequest.reset_context` for atomic context window clearing
- **Model catalog overhaul** — 44 models across 7 providers, replacing outdated GPT-4-era entries
- Removed huggingface provider; added Bedrock Llama/Nova, Azure o3/o4-mini, Mistral Codestral/Pixtral
- New ModelInfo fields: `max_output_tokens`, `is_reasoning`, `show_in_dropdown`

#### Weaver Improvements (#121)
- **/agent-plan mode** — Structured requirement gathering with skills-based recommendations
- Skills catalog (`skills_catalog.yaml`) with decision tree for domain-specific recommendations
- Guided planning vs quick start on first interaction
- `/agent-plan` discoverable in sidebar, help dialog, and splash screen

#### TUI Enhancements (#109, #121)
- **Slash commands** — `/clear`, `/quit`, `/sessions`, `/model`, `/agents`, `/workflows`, `/sidebar`, `/apps`, `/mcp`, `/patterns`, `/help`
- **Pattern browser modal** with keyboard navigation, edit, and view support
- Weaver agent as first selectable item in Ctrl+W workflows dialog
- Agent info block and keyboard hints in sidebar
- `/help` dialog listing all slash commands

#### Dynamic Ollama Discovery (#70)
- **Runtime model discovery** via `/api/tags` replacing hardcoded model list
- Dynamic tool support detection via `/api/show` template inspection
- Updated recommendations: qwen3, mistral-small3.1, command-r families

#### Tool Schema Pruning (#100)
- Pruned tool Description() strings and InputSchema property descriptions
- Schema regression test with baseline+50 token ceilings

#### Tool Lifecycle Streaming (#117)
- **Tool-started/completed events** in WeaveProgress for real-time tool timeline UI
- Correlated start/end via ToolCallID with duration counters and success/failure states

### Fixed

#### LLM Provider Fixes
- **MCP tool schema handling** (#114) — All 6 providers now handle empty/missing `type` fields; infer "object"/"array" from properties/items; added anyOf/oneOf/allOf/not composite keyword support
- **StreamWeave error visibility** (#114) — Sends EXECUTION_STAGE_FAILED event before gRPC error so TUI displays errors instead of silent empty responses
- **Rate limiter starvation** (#89) — `NewRateLimiter()` now backfills zero-value config with defaults; previously caused spurious 429 errors
- **Anthropic model IDs** (#77) — Updated from deprecated claude-3-5-sonnet/claude-3-opus to current model IDs across all providers
- **Anthropic tool_use serialization** (#77) — Custom MarshalJSON ensures `"input": {}` is always present (API requires it)
- **Anthropic streaming tool inputs** (#77) — Fix `input_json_delta` SSE handling; previously caused nil-input tool calls and retry loops
- **Gemini thought_signature** (#78) — Capture from response and echo back verbatim at Part level (was incorrectly placed inside FunctionCall)
- **shell_execute pipe race** (#117) — `wg.Wait()` before `cmd.Wait()` per Go docs

#### Windows Fixes
- **PowerShell 5.1 compatibility** (#74) — UTF-8 BOM added to quickstart.ps1 (byte 0x93 misinterpreted as smart quote)
- **YAML path escapes** (#74) — Convert backslashes in observability `sqlite_path` to forward slashes
- **Auto-install prerequisites** (#74) — Go, Just, and Buf installed via winget when missing
- **Chocolatey package compliance** — Fixed packageSourceUrl 404, removed broken iconUrl, added pattern download checksum

#### Security
- **314 gosec issues resolved** (#84) — G115 integer overflow (SafeInt32/SafeUint helpers), G304 filepath.Clean, G104 error handling, G201 SQL sanitization, G110 decompression bomb protection, G302/G306 tightened permissions
- **30 gosec G118 alerts** (#113) — Moved `#nosec` annotations to correct `go func()` lines for gosec 2.24.7
- **Ollama SSRF prevention** (#109) — Validate endpoint URL before HTTP GET
- **Rate limiter overflow guard** (#109) — `BurstCapacity*2` checked for integer overflow

#### CI/CD
- **Nightly build fixed** (#81) — Missing generate-weaver step caused 40 consecutive failures
- **SARIF upload** — Added to nightly scan; removed duplicate config causing "1 configuration not found" warnings
- **CodeQL** — Workflow permission fixes

### Changed

#### Dependency Updates
- google.golang.org/grpc: 1.79.1 → 1.79.3
- charm.land/bubbletea/v2: upgrade to v2
- charm.land/bubbles/v2: 2.0.0-rc.1 → 2.0.0
- charm.land/lipgloss/v2: upgrade to v2
- github.com/xuri/excelize/v2: updated
- filippo.io/edwards25519: 1.1.0 → 1.1.1
- securego/gosec: 2.23.0 → 2.24.7
- softprops/action-gh-release: 2.5.0 → 2.6.1
- actions/download-artifact: 7.0.0 → 8.0.1
- actions/upload-artifact: 6 → 7
- actions/attest-build-provenance: updated
- github/codeql-action: 4.32.2 → 4.33.0
- 30+ Go module updates across 3 batch PRs (#80, #90, #124)

### Testing

- **3112+ test functions** across 319 test files (up from 2252/244 in v1.1.0)
- **Zero race conditions** — all tests run with `-race` detector
- **Skills system**: 73+ tests with concurrent stress tests (33 goroutines)
- **PostgreSQL storage**: 31+ tests covering config, RPCs, backend factory, migrations
- **Supabase backend**: 560-line unit test suite + 10-function E2E integration suite
- **MCP server**: 71 unit tests for 12 gRPC RPCs
- **Security scan**: gosec passes with 0 issues

---

## [1.1.0] - 2026-02-02

### Internal API Improvements

#### agent_management Tool API Restructure (PR #63)
The `agent_management` tool now uses structured JSON for better type safety and validation.

**Key Point**: Weaver prompt was updated in the same PR - the primary use case (weaver creating agents) works seamlessly with the new API.

**What Changed**:
- Old string-based actions (`"create"`, `"update"`) → new structured JSON actions
- New actions: `create_agent`, `create_workflow`, `update_agent`, `update_workflow`
- JSONSchema validation prevents runtime errors from invalid field names

**Who's Affected**:
- ✅ Weaver users: No impact (weaver prompt updated simultaneously)
- ✅ Standard workflows: No impact (built-in tools updated)
- ⚠️ Custom integrations: Need migration (if directly calling agent_management)

**Migration for Custom Tools**:
- Old actions return `INVALID_ACTION` error with clear migration instructions
- See `prompts/tools/agent_management.yaml` for new structured format
- JSONSchema available at `pkg/shuttle/builtin/agent_management_schema.go`

### Added

#### Memory & Communication
- **Unified conversation_memory tool** - Progressive disclosure with recall/search/clear actions
- **Workflow agent communication** (PR #61) - Auto-healing agent IDs, message delivery auto-injection
- **Session-memory tool** - List/summary/compact actions with FTS5 search
- **Broadcast bus auto-injection** - Workflow coordinators get automatic message buses

#### Observability & Instrumentation
- **Self-contained observability** (PR #47) - Removed Hawk dependency
  - MemoryStorage: Ring buffer with FIFO eviction (10k traces)
  - SQLiteStorage: Persistent storage with FTS5
  - HTTP export mode still available with `-tags hawk`
- **LLM instrumentation** (PR #57) - Comprehensive token/cost tracking for all LLM calls
- **GetWorkflowExecution & ListWorkflowExecutions RPCs** - Full workflow observability

#### Multi-Agent & Orchestration
- **spawn_agent tool** - Dynamic sub-agent creation with lifecycle management
- **Auto-workflow initialization** - Peer-to-peer pub-sub workflows
- **StreamWeave integration** - CLI and workflow examples
- **Agent ID migration** (PR #59) - Backward-compatible message tracking

#### CLI, TUI & UX
- **Phase 1 CLI commands** - Sessions and MCP management
- **Built-in Operator agent** - Agent discovery with keyword matching
- **Session search** (PR #60) - Improved session select UX with metadata
- **Pattern viewer dialog** - Full pattern editor in TUI sidebar
- **Rich session metadata** - Title, time ago, token count, cost display

#### Tool System
- **Structured JSON API** (PR #63) - agent_management with JSONSchema validation
- **Tool documentation system** (PR #53) - YAML descriptions work for all tools
- **Universal executor-level solution** - Oversized tool parameter handling
- **Progressive disclosure** - Character-based limits for tool output

#### Token-Based Memory (PR #58)
- **Dynamic L1 memory allocation** - Adapts to LLM context sizes (8K-200K)
- **Token-based limits** - `MaxL1Tokens` replaces `MaxL1Messages`
- **Profile multipliers** - data_intensive (0.6x), balanced (1.0x), conversational (1.5x)
- **Backward compatible** - Proto accepts `max_l1_messages` (converts: messages × 800 → tokens)

#### Release Infrastructure
- **GPG signing** - All checksums cryptographically signed starting with v1.1.0
- **SLSA provenance attestations** - Supply chain security with build provenance
- **Enhanced version-manager** - Now tracks 13 files (added docs/README.md, chocolateyinstall.ps1)

### Fixed

#### Critical Fixes
- **Database migration failure** (PR #51) - Fixed index creation order for v1.0.0/v1.0.1 upgrade
- **MCP server startup hang** (PR #50) - Persist enabled field fix
- **Workflow agent communication** (PR #61) - Auto-inject queue messages, persist dequeue_count
- **shell_execute contradictory sandbox checks** (PR #55) - Resolved permission conflicts
- **Message duplication in TUI** (PR #52) - Fixed for all agents
- **Session modal crash** (PR #60) - Handle zero-token sessions gracefully

#### Security & Quality
- **gosec security warnings** - Fixed G302, G104, G115 across codebase
- **File permissions** - Hardened to 0600/0644/0755
- **Integer overflow protection** - Proto int32 conversions safeguarded
- **CodeQL permissions** - Security workflow improvements

#### Tool & Agent Fixes
- **Agent name lookup** (PR #44) - Support both names and GUIDs
- **Tool documentation system** (PR #53) - YAML descriptions now enabled
- **Multi-agent broadcast** (PR #48) - Test failures fixed
- **Pattern selection in sidebar** (PR #52) - Selection state preserved

#### Package Management
- **Chocolatey antivirus false positives** - Package format fixes
- **winget file format** - Preservation improvements
- **Scoop formatjson** - Pre-commit formatting
- **Homebrew GPG signing** - Added for tap commits

#### Proto & Build
- **buf generate** - Downgraded openapiv2 plugin to v2.22.0 for stability
- **Proto generation** - Up-to-date checks
- **Linter issues** - artifacts CLI, version-manager fixed

### Changed

#### Dependency Updates
- anthropic-sdk-go: 1.19.0 → 1.20.0
- lib/pq: 1.10.9 → 1.11.1
- grpc-gateway/v2: 2.27.4 → 2.27.7
- AWS SDK v2/bedrockruntime: 1.47.2 → 1.48.0
- actions/setup-go: 5 → 6
- Total: 30+ dependency updates across 3 batch PRs (#64, #46, and others)

### Documentation

#### Major Restructuring (PR #54, #56)
- Reorganized examples/ directory:
  - `examples/config/` - Server configurations
  - `examples/backends/` - Backend integrations
  - `examples/patterns/` - Pattern libraries
  - `examples/reference/` - Reference examples
- Complete rewrite of examples/README.md
- Organization rename: "Teradata-TIO" → "teradata-labs" (110 files)

#### Pattern Libraries
- agent-standard.yaml - Standard agent creation
- workflow-multi-agent.yaml - Multi-agent coordination
- workflow-debate.yaml - Debate orchestration
- workflow-pipeline.yaml - Pipeline orchestration
- workflow-parallel.yaml - Parallel orchestration

#### Reference Documentation
- TD.rom v2 improvements (PR #56) - Focus on 5 critical error patterns
- Agent templates: code-reviewer.yaml (NEW)
- Workflow namespacing documentation
- Comprehensive tool descriptions (prompts/tools/*.yaml)

### Testing

- **2252+ test functions** across 244 test files
- **Zero race conditions** (all tests run with `-race` detector)
- **50+ new test functions** for new features
- **Security scan**: gosec passes with no critical issues
- **Linting**: golangci-lint passes on all modified code

---

## [1.0.2] - 2026-01-15

### Fixed
- Various bug fixes and improvements
- Package distribution fixes

## [1.0.1] - 2026-01-14

### Fixed
- Database migration issues
- Initial release stability improvements

## [1.0.0] - 2026-01-09

### Added
- Initial release of Loom
- Complete LLM agent runtime with tool system
- Multi-agent orchestration patterns
- Pattern-guided learning system
- Agent templates and workflow management
- TUI client and gRPC server
- Support for Anthropic, AWS Bedrock, Ollama, OpenAI backends

[1.2.0]: https://github.com/teradata-labs/loom/compare/v1.1.0...v1.2.0
[1.1.0]: https://github.com/teradata-labs/loom/compare/v1.0.2...v1.1.0
[1.0.2]: https://github.com/teradata-labs/loom/compare/v1.0.1...v1.0.2
[1.0.1]: https://github.com/teradata-labs/loom/compare/v1.0.0...v1.0.1
[1.0.0]: https://github.com/teradata-labs/loom/releases/tag/v1.0.0
