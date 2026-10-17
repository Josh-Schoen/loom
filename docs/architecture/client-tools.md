# Client-Executed Tools

**Status:** 📋 Planned. The API is defined in `proto/loom/v1/loom.proto`
(`WeaveRequest.client_tools`, `WeaveRequest.client_tool_timeout_seconds`,
`WeaveProgress.client_tool_call`, `SubmitClientToolResult`). The server does
not implement it yet: `SubmitClientToolResult` returns `UNIMPLEMENTED`, and
`client_tools` on a request is ignored.
**Relates to:** HITL holds and heartbeats (`pkg/shuttle/hold_heartbeat.go`,
`docs/architecture/hitl-park-and-resume.md`)

**Target Audience**: Architects and developers working on loom clients or the
conversation loop.

## Table of Contents

- [Overview](#overview)
- [Design Goals](#design-goals)
- [System Context](#system-context)
- [Key Interaction](#key-interaction)
- [Data Structures](#data-structures)
- [Design Trade-offs](#design-trade-offs)
- [Concurrency Model](#concurrency-model)
- [Error Handling](#error-handling)
- [Security Considerations](#security-considerations)
- [Constraints and Limitations](#constraints-and-limitations)
- [Related Work](#related-work)

---

## Overview

Some capabilities exist only on the client talking to loom, not on the server:
device APIs on a phone (Reminders, Calendar), a logged-in browser session,
local files or apps on a desktop. Client-executed tools let a caller offer
those capabilities to the agent for one `StreamWeave` call. The agent calls
them like any other tool; the server hands each call back to the client over
the stream and resumes the turn when the client posts the result.

The existing mechanisms do not cover this:

- **`RegisterTool`** adds tool metadata to the server-wide tool index
  (`IndexedTool`, source `TOOL_SOURCE_CUSTOM`) for search. It has no executor,
  and the registration is global: every agent and session would be offered the
  tool even when the client that can run it is not connected.
- **`contact_human` / clarifications** return free text from a person. Using
  them to carry structured tool calls would overload a human channel with a
  machine protocol, and the streaming gateway does not forward the identifiers
  a client would need to answer.

---

## Design Goals

- **Client capabilities without server credentials.** The server never holds
  the client's device permissions, cookies, or local file access; it only sees
  the arguments it sent and the result it got back.
- **Availability tied to the connection.** A client tool exists only while the
  stream that can execute it is open.
- **Same model-facing contract as server tools.** The agent sees an ordinary
  tool with a JSON Schema input and an ordinary tool result.
- **Additive API.** Clients that do not declare client tools see no change.

**Non-goals**: server-side approval of client tools (the client owns it),
persisting client tools across calls, and running client tools when no client
is connected.

---

## System Context

```
┌──────────────────────────────┐            ┌─────────────────────────────────┐
│ Client (phone/desktop/web)   │            │ loom server                     │
│                              │            │                                 │
│  ┌────────────────────────┐  │ StreamWeave│  ┌───────────────────────────┐  │
│  │ Client tool registry   │──┼───────────▶│  │ Conversation loop         │  │
│  │ {ToolDefinition}       │  │ +client_   │  │                           │  │
│  └───────────┬────────────┘  │  tools     │  │  agent tools ∪ client     │  │
│              │               │            │  │  tools (this call only)   │  │
│              ▼               │            │  └─────────────┬─────────────┘  │
│  ┌────────────────────────┐  │ client_    │                │ call to a     │
│  │ Approval + execution   │◀─┼────────────┼────────────────┘ client tool   │
│  │ (device APIs, browser, │  │ tool_call  │  ┌───────────────────────────┐  │
│  │  local files)          │  │            │  │ Pending call table        │  │
│  └───────────┬────────────┘  │            │  │ {session, tool_call_id}   │  │
│              │               │ Submit     │  │  → waiting turn           │  │
│              └───────────────┼───────────▶│  └───────────────────────────┘  │
│                              │ ClientTool │                                 │
│                              │ Result     │──▶ [LLM provider]               │
└──────────────────────────────┘            └─────────────────────────────────┘
```

---

## Key Interaction

```
Client                     Server loop                 LLM
  │                            │                         │
  ├─ StreamWeave(q, [T]) ─────▶│                         │
  │                            ├─ prompt + tools ∪ T ───▶│
  │                            │◀──── tool_use T(args) ──┤
  │◀─ progress{client_tool_ ───┤                         │
  │    call: id, T, args}      │ register pending(id)    │
  │                            │ hold turn; heartbeats   │
  │◀─ progress (heartbeat) ────┤                         │
  │ approve + run T locally    │                         │
  ├─ SubmitClientToolResult ──▶│ resolve pending(id)     │
  │◀─ {accepted: true} ────────┤                         │
  │                            ├─ tool_result(T) ───────▶│
  │                            │◀──── final text ────────┤
  │◀─ progress … COMPLETED ────┤                         │
```

`SubmitClientToolResult` is a separate unary RPC because grpc-gateway exposes
`StreamWeave` as a server-streaming HTTP response; a REST/SSE client has no
way to send data back on it.

---

## Data Structures

- **`WeaveRequest.client_tools`** (`repeated ToolDefinition`): reuses the
  existing tool definition (name, description, `input_schema_json`). Names are
  unique within the list and must not collide with the agent's tools.
- **`WeaveProgress.client_tool_call`** (`ClientToolCall`): `tool_call_id`,
  `tool_name`, `arguments` (`Struct`), `session_id`, `timeout_seconds`.
  `session_id` is always set because the client needs it to answer, even when
  the request did not name a session.
- **`SubmitClientToolResultRequest`**: `session_id`, `tool_call_id`, and either
  `success = true` with `output` (`Value`) or `success = false` with
  `error_code` and `error_message`.
- **Pending call table** (server, planned): keyed by
  `(session_id, tool_call_id)`, value is the channel the held turn waits on.

**Invariants**

```
∀ (s, id): at most one SubmitClientToolResult for (s, id) is accepted
∀ call c: c is resolved by exactly one of {result, timeout, stream end}
∀ t ∈ client_tools(request): t is never executed by the server
```

---

## Design Trade-offs

### Decision 1: Declare tools per request

**Chosen**: `client_tools` on each `WeaveRequest`.

**Rationale**: availability matches the one stream that can execute the tool,
and each client declares only what it can do at that moment (a phone without
Calendar permission leaves the Calendar tool out).

**Alternatives considered**:
- *`RegisterTool`*: global and persistent; offers tools no connected client
  can run. Rejected.
- *Declare on `CreateSession`*: saves resending the list, but the session keeps
  advertising tools after the serving stream drops. Rejected for the first
  version; can be added later without breaking the per-request field.

### Decision 2: Separate result RPC instead of a bidirectional stream

**Chosen**: unary `SubmitClientToolResult`.

**Rationale**: the REST/SSE gateway (used by mobile and browser clients) cannot
carry client-to-server messages on a server stream. A unary call works on both
gRPC and REST.

**Alternative considered**: a new bidirectional `StreamWeave` variant. Works
for gRPC only; rejected.

### Decision 3: Reuse `ToolDefinition`

**Chosen**: reuse the existing message.

**Rationale**: it already carries what the model needs (name, description,
JSON Schema input), `RegisterTool` uses it, and clients can produce it without
a new type. The rich-metadata fields (use cases, conflicts) are optional and
can be left empty.

### Decision 4: Approval belongs to the client

**Rationale**: only the client knows what a tool does on its side (adding a
reminder vs. submitting a form in a logged-in browser) and only it can show
the user a confirmation. The server treats a declined call as a failed tool
result (`error_code = "DECLINED_BY_USER"` by convention).

---

## Concurrency Model

- Each held call blocks the turn goroutine on a per-call channel, as HITL holds
  do. Hold heartbeats keep the SSE stream alive while waiting.
- `SubmitClientToolResult` runs on a different goroutine (its own RPC). It
  removes the entry from the pending table under a mutex and then sends on the
  channel, so two concurrent submissions for one id resolve exactly once.
- Timeout and stream cancellation race with submission on the same table entry;
  whichever removes it first wins, and the loser gets `accepted = false`.
- Parallel tool calls in one model response may produce several pending client
  calls at once; each has its own entry.

---

## Error Handling

| Condition | Agent sees | Client sees |
|-----------|------------|-------------|
| Client reports failure | failed tool result with client's `error_code` | `accepted = true` |
| No result before timeout | failed tool result, `CLIENT_TOOL_TIMEOUT` | later submit → `accepted = false` |
| Stream ends while pending | failed tool result, `CLIENT_TOOL_TIMEOUT` | — |
| Unknown / answered id | — | `accepted = false`, `error` set |
| `client_tools` on unary `Weave` | — | `INVALID_ARGUMENT` |
| Name collides with agent tool | — | `INVALID_ARGUMENT` |

Each client tool call should be traced like a server tool call, with the wait
time recorded as a span attribute.

---

## Security Considerations

- **Trust boundary.** Arguments come from the model; the client validates them
  against the schema it declared before acting. Results come from the client;
  the server treats them as untrusted tool output.
- **Session binding.** A result is accepted only for a call pending in the
  named session, under the server's existing authentication. Guessing a
  `tool_call_id` from another session gives nothing.
- **No shadowing.** Rejecting name collisions stops a client from replacing a
  server tool's behavior.
- **High-risk clients.** Browser tools that act in a logged-in session are the
  highest risk case; per-call approval and site allow-lists belong in that
  client.

---

## Constraints and Limitations

- `StreamWeave` only; unary `Weave` cannot carry client tools.
- A turn holds server resources while waiting; `client_tool_timeout_seconds`
  bounds this and servers may cap it.
- Client tools are not available to sub-agents or workflows spawned during the
  call unless the implementation forwards them; the first version does not.
- If the stream drops, pending calls fail; the client cannot resume them on a
  new stream.

---

## Related Work

- **Client-side tools in LLM APIs.** The Anthropic Messages API and OpenAI
  function calling both return tool calls to the caller and expect results in
  the next request. This design applies the same split across loom's server:
  the server runs the loop, the client runs the tool.
- **HITL holds** (`docs/architecture/hitl-park-and-resume.md`): same
  hold-and-resume shape, with a machine instead of a person answering.
- **First consumer.** The loom iOS harness (`loom-mobile`) has an on-device
  tool registry whose invocation and result types match this shape
  (`call_id`, arguments object, `success` + `output` or `error.code`). Desktop
  and browser clients can use the same mechanism; nothing in the API is
  mobile-specific.
