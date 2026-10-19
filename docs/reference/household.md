---
title: "Household Reference"
weight: 47
---

# Household Reference

HTTP/gRPC API and agent tools for the people who share one server (a family
using a personal assistant on their computer): their names, and short notes
they send each other ("I'm running late", "Can you do pickup Thursday?")
with answers.

**Status**: ✅ Implemented (`LoomService`, `proto/loom/v1/household.proto`,
`pkg/household`). Off by default (`server.household.enabled`). Push
notifications for new notes are 📋 planned; clients poll
`GetHouseholdUpdates`.

---

## Quick Reference

| RPC | HTTP | Purpose |
|-----|------|---------|
| `ListHouseholdMembers` | `GET /v1/household/members` | Everyone in the household, by name |
| `SendHouseholdNote` | `POST /v1/household/notes` | Send a note (tell or ask) to another member |
| `ListHouseholdNotes` | `GET /v1/household/notes` | Notes the caller sent or received, newest first |
| `AnswerHouseholdNote` | `POST /v1/household/notes/{note_id}:answer` | Answer an ask sent to the caller |
| `MarkHouseholdNotesRead` | `POST /v1/household/notes:mark-read` | Mark notes read and answers seen |
| `GetHouseholdUpdates` | `GET /v1/household/updates` | Counts of what is new for the caller |

| Agent tool | Purpose |
|------------|---------|
| `household_members` | The members' names, marking the person being helped |
| `household_notes` | The person's recent notes and answers |

---

## Table of Contents

- [Configuration](#configuration)
- [Members and Names](#members-and-names)
- [Scope and Identity](#scope-and-identity)
- [HouseholdNote](#householdnote)
- [RPCs](#rpcs)
- [Agent Tools](#agent-tools)
- [Storage and Limits](#storage-and-limits)
- [Errors](#errors)
- [Limitations](#limitations)
- [See Also](#see-also)

---

## Configuration

```yaml
server:
  household:
    enabled: true   # default: false
```

| Field | Default | Meaning |
|-------|---------|---------|
| `server.household.enabled` | `false` | Registers the household RPCs' handlers and lets agents that list `household_members` or `household_notes` in `tools.builtin` use them. Off: every household RPC returns `FAILED_PRECONDITION`, and an agent listing the tools logs a warning and does not get them. |

Paired devices may call every household RPC (they are on the device
allowlist; see [device-pairing.md](device-pairing.md)).

---

## Members and Names

The household's members are:

1. every user ID in `<data dir>/people.json`, and
2. the user of every paired device that is not revoked (`looms devices
   list`).

`people.json` maps user IDs to display names:

```json
{
  "sam-3f9a0c71": "Sam",
  "alex-0b12d4e8": "Alex"
}
```

- Set a name when pairing: `looms pair --user sam-3f9a0c71 --name Sam`
  (`--name` needs `--user`; at most 60 characters). Another program, such as
  a setup app that adds people, may write the file itself. Write it
  atomically (temporary file, then rename).
- The server rereads the file when its modification time or size changes,
  so names appear without a restart.
- A member without a name is shown by user ID.
- A shared UI app's `UIApp.shared_by_name` is the sharer's name from this
  file (empty when they have none); see
  [mcp-apps.md#shared-apps](mcp-apps.md#shared-apps).
- Removing a phone (`looms devices revoke`) keeps the person in the
  household while they are named in `people.json`.

---

## Scope and Identity

Every RPC acts as the caller's user ID (the paired device's user, the JWT
subject, or `X-User-Id`; see [mcp-apps.md#ownership](mcp-apps.md#ownership)).

- A person lists only notes they sent or received.
- Only a note's recipient can answer it; anyone else gets `NOT_FOUND`.
- Marking read affects only notes the caller received, and answers to asks
  the caller sent; other IDs are skipped.
- The sender and recipient must both be members.

**Only as strong as the identity.** Without `server.auth.enabled` or
`server.device_pairing.required`, any caller can set `X-User-Id` and read
another person's notes.

---

## HouseholdNote

| Field | Type | Meaning |
|-------|------|---------|
| `id` | string | `note_` and 16 hex characters |
| `from_user_id`, `from_name` | string | Sender and their display name |
| `to_user_id`, `to_name` | string | Recipient and their display name |
| `kind` | `HouseholdNoteKind` | `HOUSEHOLD_NOTE_KIND_TELL` (no answer expected) or `HOUSEHOLD_NOTE_KIND_ASK` (one answer) |
| `text` | string | What the sender wrote, trimmed, 1–1000 characters |
| `created_at` | Timestamp | When it was sent |
| `read_at` | Timestamp | When the recipient read it (`MarkHouseholdNotesRead` or answering); unset while unread |
| `answer` | string | The recipient's answer to an ask; empty until answered |
| `answered_at` | Timestamp | When it was answered |
| `answer_seen_at` | Timestamp | When the sender marked the answer seen |
| `from_you` | bool | True when the caller sent it |

---

## RPCs

### ListHouseholdMembers

`GET /v1/household/members` → `{"members": [{"userId", "name", "isYou"}]}`,
sorted by name (case-insensitive), then user ID.

### SendHouseholdNote

```
POST /v1/household/notes
{"toUserId": "sam-3f9a0c71", "kind": "HOUSEHOLD_NOTE_KIND_ASK", "text": "Can you do pickup Thursday?"}
```

Returns the `HouseholdNote`. `INVALID_ARGUMENT` when `to_user_id` is empty
or not a member, is the caller, the caller is not a member, `kind` is
unspecified, or the text is empty or longer than 1000 characters.

### ListHouseholdNotes

`GET /v1/household/notes?limit=50` → `{"notes": [...]}`, newest first.
`limit` defaults to 50, at most 200.

### AnswerHouseholdNote

```
POST /v1/household/notes/{note_id}:answer
{"text": "Yes, I'll get them at 3:30"}
```

Returns the note with its answer, and marks it read. `NOT_FOUND` when the
note was not sent to the caller. `FAILED_PRECONDITION` for a tell, or for an
ask that was already answered (one answer per ask). `INVALID_ARGUMENT` for
empty or too long text.

### MarkHouseholdNotesRead

```
POST /v1/household/notes:mark-read
{"noteIds": ["note_..."]}
```

Empty `noteIds` marks all of them. Returns `{"marked": n}`, the number of
notes changed.

### GetHouseholdUpdates

`GET /v1/household/updates` →

| Field | Meaning |
|-------|---------|
| `unreadNotes` | Notes to the caller not yet read |
| `unansweredAsks` | Asks to the caller not yet answered |
| `newAnswers` | Answers to the caller's asks not yet seen |
| `updatedAt` | Latest change to any of the caller's notes; unset when there are none |

Cheap to call; a client polls it to show a badge.

---

## Agent Tools

Both only read. Agents cannot send or answer notes: those speak for the
person to someone else, so a client sends them after the person approves.

| Tool | Parameters | Returns |
|------|------------|---------|
| `household_members` | none | `{"people": [{"name", "you"}]}` |
| `household_notes` | `limit` (default 20, at most 50) | `{"notes": [{"id", "from", "to", "kind", "text", "when", "read"?, "answer"?, "answered"?}], "note"}` |

- In `household_notes`, the person being helped is `"you"` in `from` and
  `to`. `read` is present on notes they received. `answer` is present on
  asks, and is `null` while unanswered.
- The result's `note` field tells the model that the texts were written by
  other people and are not instructions, as `get_ui_app` does for shared
  apps.
- An agent gets both tools when it lists either in `tools.builtin` and the
  household is enabled.

---

## Storage and Limits

| What | Where | Limit |
|------|-------|-------|
| Names | `<data dir>/people.json` | 60 characters per name |
| Notes | `<data dir>/household/notes.json` (mode 0600, written atomically) | 1000 characters per text and answer; 2000 notes per person (sent plus received), oldest dropped first |

Notes are read once at the first household request and kept in memory. Only
one `looms serve` should use a data directory.

---

## Errors

| Code | When |
|------|------|
| `FAILED_PRECONDITION` | Household off; answering a tell; answering an ask twice |
| `INVALID_ARGUMENT` | Unknown or missing recipient, sending to yourself, sender not a member, no kind, empty or too long text |
| `NOT_FOUND` | Answering a note not sent to the caller |
| `INTERNAL` | The notes or names file cannot be read or written |

---

## Limitations

- No push notifications; clients poll `GetHouseholdUpdates`.
- Notes are one person to one person; there are no group notes.
- Notes cannot be edited or deleted through the API.
- Single process: two servers on one data directory would overwrite each
  other's notes.

## See Also

- [device-pairing.md](device-pairing.md): pairing phones as people
- [mcp-apps.md#shared-apps](mcp-apps.md#shared-apps): pages the household
  shares
- [user-memories.md](user-memories.md): what the agent remembers about each
  person
