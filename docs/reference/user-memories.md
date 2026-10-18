---
title: "User Memories Reference"
weight: 46
---

# User Memories Reference

HTTP/gRPC API for what an agent remembers about the person calling it: the
caller's own [graph memories](../architecture/graph-memory.md) for one agent.
A client app uses it to show a person what the agent knows about them and to
let them add, correct, or delete it.

**Status**: ✅ Implemented (`LoomService`, `proto/loom/v1/user_memory.proto`;
SQLite and PostgreSQL graph memory stores).

---

## Quick Reference

| RPC | HTTP | Purpose |
|-----|------|---------|
| `ListUserMemories` | `GET /v1/agents/{agent_id}/memories` | The caller's active memories, newest first |
| `AddUserMemory` | `POST /v1/agents/{agent_id}/memories` | Remember a fact the caller states |
| `CorrectUserMemory` | `POST /v1/agents/{agent_id}/memories/{memory_id}:correct` | Replace a memory with corrected text |
| `ForgetUserMemory` | `DELETE /v1/agents/{agent_id}/memories/{memory_id}` | Forget one memory |
| `ForgetAllUserMemories` | `DELETE /v1/agents/{agent_id}/memories` | Forget everything about the caller |

`agent_id` is the agent's ID or its name.

---

## Table of Contents

- [Scope and Identity](#scope-and-identity)
- [Where Memories Come From](#where-memories-come-from)
- [UserMemory](#usermemory)
- [ListUserMemories](#listusermemories)
- [AddUserMemory](#addusermemory)
- [CorrectUserMemory](#correctusermemory)
- [ForgetUserMemory](#forgetusermemory)
- [ForgetAllUserMemories](#forgetallusermemories)
- [Errors](#errors)
- [Limitations](#limitations)
- [See Also](#see-also)

---

## Scope and Identity

Every request reads and changes only the caller's memories. The caller is the
user ID set by the identity interceptors: the paired device's user
([device pairing](device-pairing.md)), the authenticated user, or the
`Grpc-Metadata-X-User-Id` header (when authentication is off). Without one it is `default-user`. Another user's memory
answers exactly like a missing one (`NOT_FOUND`).

Isolation is only as strong as identity: when neither `server.auth.enabled`
nor `server.device_pairing.required` is set, any caller can choose a user ID
with the header.

Memories are stored under the agent's name, so the same agent reached by ID
or by name has the same memories.

## Where Memories Come From

With graph memory enabled for the agent (`spec.memory.graph_memory`, on by
default; see [agent configuration](agent-configuration.md)):

- **Extraction**: after each user message, the agent's compressor LLM
  (`spec.compressor_llm`, or the agent's LLM) picks out facts from the recent
  conversation and stores them (`source: "auto_extracted"`).
- **This API**: `AddUserMemory` and `CorrectUserMemory` store `source: "user"`
  with salience 0.8 (extracted facts default to 0.5), so they rank ahead in
  recall.
- **Recall**: before each turn the agent searches the caller's memories for
  the new message and adds the relevant ones to the conversation as a system
  message. Forgotten and superseded memories are never recalled.

## UserMemory

| Field | Type | Description |
|-------|------|-------------|
| `id` | string | Memory ID |
| `content` | string | The fact, one sentence |
| `summary` | string | Short form (under ~50 characters); may be empty |
| `memory_type` | string | `fact`, `preference`, `decision`, `experience`, `failure`, or `observation` |
| `source` | string | `auto_extracted`, `user`, or another source label |
| `created_at` | int64 | Unix seconds (JSON: string) |
| `event_date` | string | `YYYY-MM-DD` the fact refers to; empty when it has none |

## ListUserMemories

```proto
rpc ListUserMemories(ListUserMemoriesRequest) returns (ListUserMemoriesResponse)
```

**Request**: `agent_id` (string, required); `limit` (int32, default 100,
values above 500 are capped at 500); `offset` (int32, default 0, must not be
negative).

**Response**: `memories` (newest first); `total_count` (active memories the
caller has for this agent).

```bash
curl -s http://localhost:5006/v1/agents/assistant/memories \
  -H "Authorization: Bearer $DEVICE_TOKEN"
```

```json
{
  "memories": [
    {
      "id": "mem_…",
      "content": "Allergic to tree nuts",
      "memoryType": "fact",
      "source": "user",
      "createdAt": "1792329498"
    }
  ],
  "totalCount": 1
}
```

## AddUserMemory

```proto
rpc AddUserMemory(AddUserMemoryRequest) returns (UserMemory)
```

**Request**: `agent_id` (required); `content` (1-500 characters after trimming
spaces).

```bash
curl -s -X POST http://localhost:5006/v1/agents/assistant/memories \
  -H "Authorization: Bearer $DEVICE_TOKEN" \
  -d '{"content": "Allergic to peanuts"}'
```

## CorrectUserMemory

```proto
rpc CorrectUserMemory(CorrectUserMemoryRequest) returns (UserMemory)
```

**Request**: `agent_id`, `memory_id`, `content` (1-500 characters).

Stores a new memory with the corrected text and supersedes the old one
(graph memory `Supersede`): the old memory is no longer listed or recalled.
The new memory keeps the old one's type and entity links, has a new `id`, and
is returned.

```bash
curl -s -X POST "http://localhost:5006/v1/agents/assistant/memories/$MEMORY_ID:correct" \
  -H "Authorization: Bearer $DEVICE_TOKEN" \
  -d '{"content": "Allergic to tree nuts, not peanuts"}'
```

## ForgetUserMemory

```proto
rpc ForgetUserMemory(ForgetUserMemoryRequest) returns (ForgetUserMemoryResponse)
```

**Request**: `agent_id`, `memory_id`. Forgetting a memory that is already
forgotten or superseded returns `NOT_FOUND`.

```bash
curl -s -X DELETE "http://localhost:5006/v1/agents/assistant/memories/$MEMORY_ID" \
  -H "Authorization: Bearer $DEVICE_TOKEN"
```

## ForgetAllUserMemories

```proto
rpc ForgetAllUserMemories(ForgetAllUserMemoriesRequest) returns (ForgetAllUserMemoriesResponse)
```

**Request**: `agent_id`. Forgets every active memory the caller has for the
agent and deletes the caller's entities and relationships for it (the people,
places, and things extraction recorded). **Response**: `forgotten_count`.

```bash
curl -s -X DELETE http://localhost:5006/v1/agents/assistant/memories \
  -H "Authorization: Bearer $DEVICE_TOKEN"
```

```json
{"forgottenCount": 3}
```

## Errors

| gRPC code | HTTP | When |
|-----------|------|------|
| `INVALID_ARGUMENT` | 400 | `agent_id` or `memory_id` missing; `content` empty or over 500 characters; negative `offset` |
| `FAILED_PRECONDITION` | 400 | Graph memory is disabled for the agent, or the server has no graph memory store |
| `NOT_FOUND` | 404 | Unknown agent; memory missing, forgotten, superseded, or another user's |
| `INTERNAL` | 500 | Storage error |

## Limitations

- Forgetting does not change conversation history. Facts still in the
  conversation the caller is having can be extracted again from its recent
  messages; clients should start a new conversation after
  `ForgetAllUserMemories`.
- Memories are not edited in place: a correction is a new memory that
  supersedes the old one (the old one stays in the store as history and is
  never recalled).
- Entities (and their properties) are not exposed by this API; use
  `GraphMemoryService` (gRPC only) for the full graph.

## See Also

- [Graph memory architecture](../architecture/graph-memory.md)
- [Agent configuration: `memory.graph_memory`](agent-configuration.md)
- [Device pairing](device-pairing.md)
- [HTTP server](http-server.md)
