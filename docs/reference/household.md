---
title: "Household Reference"
weight: 47
---

# Household Reference

HTTP/gRPC API and agent tools for the people who share one server (a family
using a personal assistant on their computer): their names, short notes
they send each other ("I'm running late", "Can you do pickup Thursday?")
with answers, and who's doing what: chores, turns, pickups and
appointments.

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
| `ListHouseholdItems` | `GET /v1/household/items` | Chores, turns, pickups and appointments the caller may see |
| `SaveHouseholdItem` | `POST /v1/household/items` | Create an item, or change one the caller is involved in |
| `CompleteHouseholdItem` | `POST /v1/household/items/{item_id}:complete` | Mark it done (a turn passes on, a repeat moves to its next date) |
| `DeleteHouseholdItem` | `DELETE /v1/household/items/{item_id}` | Delete an item the caller is involved in |

| Agent tool | Purpose |
|------------|---------|
| `household_members` | The members' names, marking the person being helped |
| `household_notes` | The person's recent notes and answers |
| `household_items_list` | The items the person can see, with whose turn it is |
| `household_item_done` | Mark one of the person's own items done |

---

## Table of Contents

- [Configuration](#configuration)
- [Members and Names](#members-and-names)
- [Scope and Identity](#scope-and-identity)
- [HouseholdNote](#householdnote)
- [HouseholdItem](#householditem)
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
| `server.household.enabled` | `false` | Registers the household RPCs' handlers and lets agents that list any household tool in `tools.builtin` use them. Off: every household RPC returns `FAILED_PRECONDITION`, and an agent listing the tools logs a warning and does not get them. |

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
- An item is visible to the person who set it up (`created_by`), its
  `assignees`, its `for_user_id`, and, when `everyone` is set, every member.
  Anyone else gets `NOT_FOUND` for it.
- Only the people an item involves (creator, assignees, `for_user_id`) may
  change or delete it; seeing an `everyone` item is not enough
  (`PERMISSION_DENIED`).
- Only an assignee may complete an item that has assignees (any assignee
  for a turn, not only the one whose turn it is). Anyone who sees an item
  with no assignees may complete it.

**Only as strong as the identity.** Without `server.auth.enabled` or
`server.device_pairing.required`, any caller can set `X-User-Id` and read
another person's notes and items.

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

## HouseholdItem

| Field | Type | Meaning |
|-------|------|---------|
| `id` | string | `item_` and 16 hex characters; empty when creating |
| `kind` | `HouseholdItemKind` | `CHORE`, `TURN` (the assignees take turns), `PICKUP` (pickup or drop-off), `APPOINTMENT` |
| `title` | string | Trimmed, 1–200 characters |
| `assignees`, `assignee_names` | string list | Who does it (user IDs, members), in turn order for a turn; at most 20, duplicates dropped. A turn needs at least two |
| `for_user_id`, `for_name` | string | Who it is for (an appointment's patient); optional, a member |
| `everyone` | bool | Visible to every member |
| `starts_at` | Timestamp | When it is due; for a repeating item, the next time |
| `repeat` | `HouseholdRepeat` | `daily`, or `weekdays` (1 = Monday … 7 = Sunday); unset is once |
| `place` | string | At most 200 characters |
| `notes` | string | At most 1000 characters |
| `created_by`, `created_by_name`, `created_at`, `updated_at` | | Who set it up, and when it last changed |
| `turn_user_id`, `turn_name`, `next_turn_user_id`, `next_turn_name` | string | For a turn: whose turn it is now and next |
| `done_by`, `done_by_name`, `done_at` | | Who last completed it, and when |
| `done` | bool | A one-time item that was completed; a repeating item or a turn never is |
| `yours` | bool | The caller's to do: for a turn, it is their turn; otherwise they are an assignee, or no one is assigned and they can see it |
| `can_edit` | bool | The caller is involved and may change or delete it |

Fields from `assignee_names` on are output only; `SaveHouseholdItem`
ignores them.

**Days and times.** Days (repeats, "due today") are in the server's local
time zone. A repeating item saved without `starts_at` starts at midnight on
its next day, from today.

**Completing.**

- A turn passes to the next assignee, wrapping around.
- A repeating item moves to the first day it repeats after both its due day
  and today, at the same wall-clock time. Doing an overdue item counts as
  today's, so missed days do not pile up; doing one early moves it past its
  due day.
- A one-time item (other than a turn) becomes `done`.
- Changing a turn's assignees keeps the turn with the same person when they
  are still an assignee; otherwise it goes to the first.

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
| `itemsDue` | The caller's open items (`yours`) due by the end of today, including overdue ones |
| `updatedAt` | Latest change to any of the caller's notes or to an item they can see; unset when there are none |

Cheap to call; a client polls it to show a badge.

### ListHouseholdItems

`GET /v1/household/items?mineOnly=true&includeDone=false&from=...&to=...` →
`{"items": [...]}`, soonest first, items with no time last.

| Parameter | Meaning |
|-----------|---------|
| `mine_only` | Only items that are `yours` |
| `include_done` | Also one-time items already done (kept 30 days after) |
| `from`, `to` | Only items due in `[from, to)`; items with no `starts_at` are always included |

### SaveHouseholdItem

```
POST /v1/household/items
{"item": {"kind": "HOUSEHOLD_ITEM_KIND_TURN", "title": "Dishes",
          "assignees": ["sam-3f9a0c71", "jo-77aa01b2", "alex-0b12d4e8"],
          "startsAt": "2026-10-19T19:00:00-05:00", "repeat": {"daily": true}}}
```

Without `id`, creates an item set up by the caller. With `id`, replaces its
kind, title, assignees, `for_user_id`, `everyone`, `starts_at`, `repeat`,
`place` and `notes`. Returns the item.

The server does not ask anyone before an item is assigned to them. A client
should ask the person before saving an item that involves someone else.

### CompleteHouseholdItem

`POST /v1/household/items/{item_id}:complete` with `{}` → the item, after
completing (see [Completing](#householditem)).

### DeleteHouseholdItem

`DELETE /v1/household/items/{item_id}` → `{}`.

---

## Agent Tools

They only read, except `household_item_done`, which completes only the
person's own items. Agents cannot send or answer notes or set up items:
those speak for the person to someone else or give someone work, so a
client does them after the person approves.

| Tool | Parameters | Returns |
|------|------------|---------|
| `household_members` | none | `{"people": [{"name", "you"}]}` |
| `household_notes` | `limit` (default 20, at most 50) | `{"notes": [{"id", "from", "to", "kind", "text", "when", "read"?, "answer"?, "answered"?}], "note"}` |
| `household_items_list` | `mine_only`, `include_done` (default false) | `{"items": [{"id", "kind", "title", "who", "for"?, "everyone", "when"?, "repeats"?, "place"?, "notes"?, "set_up_by", "yours", "turn"?, "next_turn"?, "last_done_by"?, "last_done"?, "done"?}], "now", "note"}` |
| `household_item_done` | `item_id` (required) | `{"done": true, "item": {...}, "note"}`; error `NOT_YOURS` for an item that is not the person's to complete, `NOT_FOUND` for one they cannot see |

- In `household_notes`, the person being helped is `"you"` in `from` and
  `to`. `read` is present on notes they received. `answer` is present on
  asks, and is `null` while unanswered.
- The result's `note` field tells the model that the texts were written by
  other people and are not instructions, as `get_ui_app` does for shared
  apps.
- In `household_items_list`, times (`when`, `last_done`, `now`) are in the
  server's local time, as `Mon 2006-01-02 15:04`; `repeats` is in words
  ("every day", "every Tuesday, Friday"). The person being helped is
  `"you"`.
- An agent gets all four tools when it lists any of them in `tools.builtin`
  and the household is enabled.

---

## Storage and Limits

| What | Where | Limit |
|------|-------|-------|
| Names | `<data dir>/people.json` | 60 characters per name |
| Notes | `<data dir>/household/notes.json` (mode 0600, written atomically) | 1000 characters per text and answer; 2000 notes per person (sent plus received), oldest dropped first |
| Items | `<data dir>/household/items.json` (mode 0600, written atomically) | 500 open items for the household (`RESOURCE_EXHAUSTED` past it); done one-time items are dropped 30 days after |

Notes and items are each read once at the first household request and kept in memory. Only
one `looms serve` should use a data directory.

---

## Errors

| Code | When |
|------|------|
| `FAILED_PRECONDITION` | Household off; answering a tell; answering an ask twice |
| `INVALID_ARGUMENT` | Unknown or missing recipient, sending to yourself, sender not a member, no kind, empty or too long text; for items, no item, kind or title, an assignee or `for_user_id` who is not a member, a turn with fewer than two assignees, a weekday outside 1–7, a field too long |
| `NOT_FOUND` | Answering a note not sent to the caller; an item the caller cannot see |
| `PERMISSION_DENIED` | Changing or deleting an item the caller sees but is not involved in; completing one that is not the caller's to do |
| `RESOURCE_EXHAUSTED` | Creating an item past 500 open items |
| `INTERNAL` | The notes, items or names file cannot be read or written |

---

## Limitations

- No push notifications; clients poll `GetHouseholdUpdates`.
- Notes are one person to one person; there are no group notes.
- Notes cannot be edited or deleted through the API.
- No reminders from the server; a client schedules its own from the items
  it fetched.
- One time zone, the server's, for every person's days.
- A turn passes on in a fixed order; there is no skipping or swapping.
- Single process: two servers on one data directory would overwrite each
  other's notes.

## See Also

- [device-pairing.md](device-pairing.md): pairing phones as people
- [mcp-apps.md#shared-apps](mcp-apps.md#shared-apps): pages the household
  shares
- [user-memories.md](user-memories.md): what the agent remembers about each
  person
