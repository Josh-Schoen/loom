# Apprentice: Turning Observed Work into Reusable Automation

**Status**: 📋 Planned — nothing in this document is implemented yet.
**Scope**: `loom` (engine) + `avmo-tera-cloud/tera-backend` (tenancy, consent, promotion) + `up-tera` (capture and review UI).
**Working name**: apprentice. Prior working name "track task" is rejected — see [Naming](#naming).

---

## Problem

A user (or an agent) works through a multi-step procedure — profile a table, fix the skew,
re-run the load, check the row counts. It works. Nothing captures it. The next person
re-derives it from scratch, and so does the same person next month.

Loom can already *author* automation from intent: the weaver turns "build me a research
workflow" into `workflows/research.yaml`. What it cannot do is author from **evidence** —
take work that actually happened and turn it into something reusable.

Two things are missing:

1. **Capture on request** — "watch what I'm doing here and make it reusable."
2. **Suggestion from observation** — opt-in, passive: notice that a procedure recurs across
   sessions, agents, and people, and offer to take it over.

The second is the more valuable half. Repetition is the evidence that a procedure is worth
automating, and it is also the evidence that makes generalization tractable — one run tells
you what happened, three runs tell you which parts were incidental.

---

## Naming

The feature is **apprentice**; the thing it emits is a **candidate**; the act of accepting one
is **deputizing** it.

| Term | Meaning |
|---|---|
| **apprentice** | The meta-agent that watches and proposes. Sits alongside `weaver` and `guide`. |
| **candidate** | A proposed artifact — a skill, workflow, agent, or trigger — with provenance and a confidence score. Not yet active. |
| **deputize** | Accepting a candidate: it is written through the existing create paths and becomes live. |
| **watch mode** | Explicit, scoped capture the user starts and stops. |
| **observe mode** | Opt-in passive mining of stored traces for recurring procedures. |

The division against the weaver is the reason this belongs in loom:
**the weaver authors from intent; the apprentice authors from evidence.** Both emit the same
artifact types through the same restricted write path
([`pkg/shuttle/builtin/agent_management.go:188`](../pkg/shuttle/builtin/agent_management.go:188)).

### Names rejected, with reasons

| Name | Why not |
|---|---|
| **track task** | Collides with three existing things that are all different from this: `pkg/task`, cloud's `cloud_tasks`, and up-tera's `features/tasks`. The task board is this feature's *input*, not its subject — the collision would actively mislead. |
| **draft** | Correct in the weaving sense (a draft is the written notation for reproducing a woven structure), but already taken in the same domain with a different meaning: `draft_skill_name` / `draft_skill_content` on sessions (`proto/loomcloud/v1/session.proto:197`) and `AdminDraftSkillCache` mean "an unsaved skill under test in an admin session." Also one character from `pkg/drift`. |
| **shadow** | 15 existing hits across the two repos, and the wrong tone for a feature whose adoption depends on people consenting to be recorded. |
| **duet** | Implies real-time collaboration. This is capture-then-reuse; the timing is wrong. Also a live product name elsewhere. |
| **deputy** | Names the end state rather than the mechanism, and reads oddly for the watching half. Retained as the *verb* for promotion, which is exactly what it describes. |

`apprentice`, `candidate`, and `deputize` have zero hits across `loom` and `tera-backend`.

---

## What already exists (verified — reuse, do not rebuild)

This feature is mostly assembly. The novel code is segmentation, generalization, and
recurrence scoring; everything the distiller consumes and everything it produces already exists.

### loom

| Capability | Where | Note |
|---|---|---|
| Skill type with machine-authorship fields | [`pkg/skills/types.go`](../pkg/skills/types.go) | `Confidence`, `Status`, `LastValidatedMs`, `RiskLevel` are already on `Skill` |
| Skill/agent/workflow write path, restricted to meta-agents | [`pkg/shuttle/builtin/agent_management_skill.go`](../pkg/shuttle/builtin/agent_management_skill.go), [`agent_management.go:188`](../pkg/shuttle/builtin/agent_management.go:188) | Gate currently allows `weaver` and `guide`; `apprentice` must be added |
| **Skill → tasks**, the exact inverse of this feature | [`pkg/skills/tasks/emitter.go`](../pkg/skills/tasks/emitter.go) | Materializes `SkillTaskTemplate.Steps` onto the board; falls back to `task.Decomposer` |
| Task model rich enough to hold a captured step | [`proto/loom/v1/task.proto:234`](../proto/loom/v1/task.proto:234) | `objective`, `approach`, `acceptance_criteria`, `notes`, `estimated_effort`, `parent_id`, typed `TaskDependency` DAG |
| Task provenance back to the emitting skill | [`pkg/skills/tasks/emitter.go`](../pkg/skills/tasks/emitter.go) | `skill:<name>\|sess:<sessionID>\|step:<index>` |
| Board-honesty enforcement | [`pkg/skills/hygiene/doc.go`](../pkg/skills/hygiene/doc.go) | Catches orphan `IN_PROGRESS` / unstarted tasks before a turn returns — a reason the board is trustworthy as a trace |
| Workflow YAML + executors | [`pkg/orchestration/workflow_config.go`](../pkg/orchestration/workflow_config.go), [`workflow_examples/patterns/`](../workflow_examples/patterns) | Pipeline, parallel, fork-join, hierarchical |
| Skill auto-activation triggers | `SkillTrigger` in [`pkg/skills/types.go`](../pkg/skills/types.go) | `keywords`, `intent_categories`, `mode`, `min_confidence` — TRIGGER candidates need no new machinery here |
| Workflow scheduling | [`proto/loom/v1/loom.proto:366`](../proto/loom/v1/loom.proto:366) | `ScheduleWorkflow`, `UpdateScheduledWorkflow`, `TriggerScheduledWorkflow` |
| Validation | [`pkg/skills/hygiene/`](../pkg/skills/hygiene), `pkg/evals` | |
| Raw step detail | `tool_executions` in [`pkg/storage/postgres/migrations/000001_initial_schema.up.sql`](../pkg/storage/postgres/migrations/000001_initial_schema.up.sql), spans in [`pkg/observability/types.go:74`](../pkg/observability/types.go:74) | |

`pkg/skills/importer/` is **not** reusable as a distiller — it converts Anthropic-style
`SKILL.md` directories into loom YAML and is explicitly non-LLM and purely transformational.
Its `render` and `classify` stages are reusable for emitting the final YAML.

### avmo-tera-cloud / tera-backend

| Capability | Where |
|---|---|
| Git-backed marketplace: contribute → PR → sync → install | `proto/loomcloud/v1/skill.proto`, `pkg/gitskills/`, `docs/HLD_skill_marketplace.md` |
| Per-tenant contributable org repos | `skill_org_repos`; config at `loom-cloud.example.yaml:245` |
| Skill validation service | `pkg/skillvalidation/`, `ValidateSkill` RPC |
| Workflow CRUD + scheduling | `proto/loomcloud/v1/workflow.proto` — `CreateWorkflow`, `SetWorkflowSchedule` |
| Session export, message history | `ExportSession`, `session_messages` |
| Cross-agent activity | `workflow_tool_activity`, `trace_spans` |

The curated skills repo `Teradata-PE/aai-tera-agentic-skills` already runs
`skill-validation.yml` plus single- and multi-turn skill evals in CI. Contributions, however,
target *org* repos — arbitrary per-tenant GitHub slugs with no guaranteed CI. **Validation must
therefore run before the PR is opened, not in the destination repo.**

---

## Prior art

This is not a new idea. It is a 50-year-old research field called **programming by
demonstration** (PBD), and the canonical text is literally titled *Watch What I Do: Programming
by Demonstration* (Cypher et al., MIT Press 1993). The lineage runs Pygmalion (1975) → macro
recorders → Eager (1991) → CoScripter/Vegemite → RPA (UiPath, Blue Prism) → LLM agents. **None
of it achieved broad adoption**, and Lau's retrospective is blunt that the barrier was usability
rather than algorithmic capability.

Every documented failure falls into two buckets:

1. **Generalizing from one example.** Turning a demonstration into a program requires inferring
   intent, and automatic generalization routinely gets it wrong. The literature's mitigations are
   multi-shot demonstration or hand-configuration. Eager waited for **two complete iterations**
   (three for trivial patterns) before offering to automate anything — it inferred from
   *repetition*, not from a single demo.
2. **Brittleness of the capture substrate.** GUI, DOM, and pixel-level capture breaks whenever
   the interface changes. This killed CoScripter and Vegemite, is the standing critique of RPA,
   and remains a stated limitation of ALLOY (2025), which is confined to web environments.

**Where the apprentice differs, and it is the whole bet:** it captures at the *semantic* layer —
tool calls with structured inputs, task objectives, dependency edges — not pixels or DOM. Tool
schemas are versioned contracts; GUIs are not. That sidesteps bucket 2 entirely, which is the
bucket that killed most of the lineage. On bucket 1, the plan already independently landed on
Eager's answer: require recurrence (N≥2) before proposing in observe mode. Loom also holds a
*persistent* trace corpus, so it can see "repetitions spread out over time" — something Eager
explicitly could not, having only a session history.

**Concurrent competition.** xAI shipped **Grok Bot** on 11 Aug 2026: demonstrate a task once via
screen recording, it stores and replays the sequence, refines from user corrections, and each bot
gets its own cloud computer. It is the same product idea sitting squarely in bucket 2 — GUI-level
capture, cross-app. That is a fair validation that the problem is worth solving, and a reason to
be explicit that our differentiator is substrate, not concept.

**What we do not escape.** Three limitations recur from Eager (1991) through ALLOY (2025) and
should be treated as boundaries rather than bugs:

- **No conditionals, loops, or error recovery.** Both systems model a demonstration as a single
  linear example. Anything richer needs authoring, not capture.
- **Granularity is an open empirical question.** ALLOY reports task-level abstraction misses
  procedural constraints users consider essential, while action-level overfits to the interface.
  We sit on the task-level side, so we inherit the former risk.
- **Capture records *how*, not *why*.** ALLOY names this explicitly. Loom is better placed here
  than any predecessor — the first user message carries intent, and `Task.objective` /
  `acceptance_criteria` carry rationale when boards are on — but it is not solved for free.

Two design details worth borrowing outright:

- **Split abstraction from instantiation** (ALLOY's Identifier and Filter agents), rather than
  generalizing in one pass.
- **Do not build a live step display.** Only 2 of ALLOY's 12 participants noticed the workflow
  updating during their demonstration; nearly everyone reviewed afterwards. This independently
  confirms deprioritizing the TUI live step list in favour of the review dialog.

---

## Architecture

### Trace: tool calls and messages are the spine

> **Revised after the P1a spike.** This section originally made the task board the spine. Real
> data says otherwise: a local corpus of 284 sessions held 4,651 messages and 2,047 tool
> executions and **zero** tasks or boards, because `TaskBoardConfig` defaults to
> `Enabled: false` ([`pkg/agent/registry.go:785`](../pkg/agent/registry.go:785)). The board is
> excellent structure when it exists, but it cannot be the spine of a feature that has to work
> on the traces people actually produce.

```
tool_executions + messages (spine: what was attempted, in what order, with what intent)
  + task board, when enabled (enrichment: objective, acceptance criteria, dependency edges)
  + spans (detail: timing, nesting, errors)
      │
      ▼
  normalized Trace ─► segment ─► abstract ─► instantiate ─► emit ─► validate ─► candidate
```

- **segment** — trace → one or more episodes with a clear goal. Drop genuine noise: repeated
  identical searches, status-file churn, environment probing. **Do not drop failed steps** — a
  tool call's *input* is the evidence of intent, independent of its outcome. See finding 4.
- **abstract** — replace task-specific literals (this database, this date range, this path) with
  named placeholders, preserving structure.
- **instantiate** — bind placeholders to typed parameters with defaults and descriptions.
- **emit** — produce a candidate of the appropriate kind (below).
- **validate** — hygiene audit, `ValidateSkill`, and for workflows a dry-run parse. Nothing
  reaches a user unvalidated.

Abstraction is split from instantiation deliberately, mirroring ALLOY's Identifier/Filter
separation. Generalization is the step that has sunk this idea repeatedly, and doing it in one
shot from a single example is the documented failure mode — see [Prior art](#prior-art).

### Candidate kinds and the decision rule

Ambiguity resolves toward the cheaper artifact — a skill is easier to discard than an agent.

| Kind | Emit when | Lands as |
|---|---|---|
| **SKILL** | Repeatable procedure, single agent, judgment required at steps | `SkillTaskTemplate.Steps` + prompt instructions → skill YAML |
| **WORKFLOW** | Fixed step order, fan-out/parallelism, or more than one agent | workflow YAML (`pipeline`, `parallel`, `fork_join`) |
| **AGENT** | The same tool set and domain recur across many episodes with no existing agent fitting | agent YAML, via the weaver's existing path |
| **TRIGGER** | An existing or candidate artifact recurs on a cadence, or after a detectable event | `SkillTrigger` keywords/intent + mode, or `ScheduleWorkflow` / `SetWorkflowSchedule` |

TRIGGER is the cheapest and most under-served: the auto-activation and scheduling machinery
already exists in both repos and nothing currently proposes values for it.

### The output type is not free-form YAML

For SKILL candidates the target is `SkillTaskTemplate.Steps` — the same structure the emitter
consumes in the forward direction. This is what makes P0 self-checking.

---

## P0: the round-trip oracle

Before any cloud or UI work, prove the distiller offline in loom, CLI-only:

1. Take a shipped skill with an authored `TaskTemplate` (e.g. [`skills/teradata-sql-analytics.yaml`](../skills/teradata-sql-analytics.yaml)).
2. Run it. The emitter materializes its steps onto a task board.
3. Distill the completed board back into a `SkillTaskTemplate`.
4. Diff recovered against original.

Structural fidelity — step count, order, dependency edges, parameter positions — is measurable
without an LLM judge. If the round trip does not survive a skill we wrote ourselves, it will
not survive real work, and the feature can be abandoned for the cost of one package.

Exit criteria for P0: round-trip on ≥3 authored skills, plus one hand-scored real session.

---

## Surfaces: the TUI is the first client, not an afterthought

Loom's TUI is a gRPC client of `looms` ([`internal/app/app.go:46`](../internal/app/app.go:46) —
`NewFromClient(c *client.Client)`), and the task manager is constructed server-side in
[`cmd/looms/cmd_serve.go:1016`](../cmd/looms/cmd_serve.go:1016). The TUI and up-tera are
therefore **peers over the same engine**, which has one design consequence:

> The apprentice must be exposed as `ApprenticeService` in `proto/loom/v1/apprentice.proto`
> and served from `pkg/server` — not as a Go package plus CLI only. Otherwise the TUI cannot
> drive it, and tera-backend has to invent an API that loom should have defined.

This shrinks the cloud work: tera-backend becomes a multi-tenant wrapper over loom's RPCs, the
same pattern it already uses for everything else, rather than a new API surface.

Most of what the TUI needs already exists:

| Need | Existing mechanism |
|---|---|
| Command entry (`/apprentice watch`, browse candidates) | `uicmd.Command` + `Handler`, registered like `new_session` / `toggle_yolo` / `browse_apps` ([`commands.go:308`](../internal/tui/components/dialogs/commands/commands.go:308)); `arguments.go` supports scoped args |
| Candidate browser | New dialog beside the existing `workflows`, `pattern`, `agents`, `artifactbrowser`, `sessions` dialogs in [`internal/tui/components/dialogs/`](../internal/tui/components/dialogs) |
| Live progress while watching | `ProgressListener` + `ProgressMultiplexer` + [`TUIProgressListener`](../pkg/metaagent/tui_listener.go) already stream meta-agent progress as Bubbletea messages; `ConsoleListener` covers the CLI path |
| **Parameter confirmation during generalization** | `QuestionAskedMsg` / `QuestionAnsweredMsg` + the `clarification` dialog — the channel the weaver already uses for clarifications |
| Per-session watch on/off | `toggle_yolo` is the precedent for a persistent session-mode toggle |

Genuinely missing: there is **no task board view** — [`internal/tui/page`](../internal/tui/page)
contains only `chat`. A live step list is new UI; it belongs in the existing chat sidebar rather
than a new page, and it is lower priority for P1 than the candidate review dialog.

**Why this matters for sequencing**: the full loop — watch, distill, review, deputize — can run
in loom alone, against a local `looms` server, with no cloud and no up-tera. The UX gets
discovered in the TUI where iteration is cheap, and P2/P3 inherit settled interaction design
instead of inventing it. P0 stays CLI-only on purpose, since it needs no server at all.

---

## Ownership

| Concern | Repo |
|---|---|
| Trace model (proto first), segmenter, generalizer, emitter, candidate scoring | **loom** — `pkg/apprentice`, `proto/loom/v1/apprentice.proto` |
| `ApprenticeService` RPCs + `pkg/server` implementation | **loom** — consumed by both the TUI and tera-backend |
| `apprentice` meta-agent + ROM; add to the `agent_management` allowlist | **loom** |
| Recurrence detection over a local trace corpus | **loom** |
| TUI: watch command, candidate browser dialog, progress rendering, parameter clarification | **loom** — `internal/tui` |
| `loom apprentice watch / list / show / deputize` CLI | **loom** — server-free dogfood loop |
| `ApprenticeService` RPCs, RLS tables, quotas, audit | **tera-backend** |
| Consent, redaction, retention | **tera-backend** |
| Deputize → existing create paths (`ContributeSkillToOrg`, `CreateWorkflow`, `SetWorkflowSchedule`) | **tera-backend** — **no second skill store** |
| Cross-user recurrence at org scale | **tera-backend**, later **loom-knowledge** |
| Watch affordance, live step list, candidate review/edit, suggestion inbox (web) | **up-tera** — after the TUI settles the interaction design |
| Semantic non-chat activity events (notebook, SQL editor, file browser) | **up-tera**, phase 5 |

### Why not loom-knowledge (yet)

`loom-knowledge` is the wrong host today: its README lists Loom integration as not started, so
hosting the distiller would invert the intended dependency direction; its ingest path is
deliberately LLM-free with reproducible deterministic ranking, which the distiller is not; and
its output type is entities and insights, not executable YAML.

It becomes the right home for one specific part later: **org-scale recurrence and retrieval**.
`ContextService.ResolveContext` with use-case-scoped profiles is a good fit for "how has this
been done here before, by whom, with what confidence," and its deterministic ranking suits
recurrence scoring. That is phase 5 and it is blocked on Loom integration landing there.

---

## Phases

| Phase | Repo | Contents |
|---|---|---|
| **P0** ✅ | loom | Round-trip oracle. Offline distiller over a completed task board → `SkillTaskTemplate`. `pkg/apprentice`, no proto/server/TUI, no LLM. |
| **P1a** ✅ | loom | Prior-art review and a read-only spike over a real session corpus, before committing to P1. Produced findings 3–5 and reversed the trace-substrate decision. |
| **P1** | loom | `apprentice.proto` trace/candidate model + `ApprenticeService`, `apprentice` meta-agent, trace assembly from `tool_executions` + `messages`, abstract/instantiate split, SKILL + WORKFLOW emission, sanitization pass, validation gate. Every candidate `PROPOSED` with low confidence. |
| **P1.5** | loom | TUI: watch command, candidate review dialog, progress rendering, parameter clarification. Complete loop with no cloud dependency — this is where the UX is settled. |
| **P2** | tera-backend | Multi-tenant wrapper over loom's `ApprenticeService`, `apprentice_candidates` (RLS), consent gate, redaction, `Deputize` wired to existing create paths. |
| **P3** | up-tera | Web watch affordance, live step list, candidate review/edit, inheriting P1.5's interaction design. |
| **P4** | both | Observe mode: batch recurrence mining over stored traces; suggestion inbox; AGENT and TRIGGER candidates. |
| **P5** | loom-knowledge, up-tera | Org-scale recurrence via `ContextService`; non-chat activity events as an additional trace source. |

Watch mode ships before observe mode, but the trace model must be designed for both from P1 —
observe mode is only a different event source and a recurrence pass over the same structures.

---

## Testing

The feature is LLM-driven at exactly one step (generalization) and deterministic everywhere
else. The testing strategy follows that seam: everything deterministic gets ordinary Go tests
with `-tags fts5 -race`, the LLM step gets recorded fixtures for correctness and an eval suite
for quality. **Do not expect `just test` to catch a worse prompt** — that is what the eval tier
is for, and conflating the two is how quality regressions ship unnoticed.

### Tier 1 — Deterministic units (the bulk of coverage)

| Component | Test shape |
|---|---|
| Trace normalization | Table-driven: fixture task board + `tool_executions` + `messages` → expected `Trace`. Golden files. |
| Segmenter boundaries | Table-driven over fixture traces → expected episode splits. Retries, dead ends, and clarification exchanges must be dropped; assert they are. |
| **Kind decision rule** | Pure function over trace features → `SKILL \| WORKFLOW \| AGENT \| TRIGGER`. Exhaustive table. Keeping this a pure function rather than an LLM judgment exists partly so it is testable — do not let it drift into the prompt. |
| Candidate → YAML emission | Golden files, matching the existing convention for generated output. |
| Procedure fingerprint | Same procedure → same fingerprint; reordered-but-equivalent → same; materially different → different. Gates the "a discarded candidate never gets re-proposed" requirement, which is otherwise a nag bug. |
| Recurrence scoring | Fixture corpus of N traces → expected match groups at a given threshold. |

### Tier 2 — The round-trip oracle (P0 exit criteria, then permanent regression suite)

```
shipped skill with authored TaskTemplate
  → emitter (pkg/skills/tasks) → task board
  → distiller → recovered SkillTaskTemplate
  → structural diff against the original
```

Assertions are structural and need no LLM: step count, step order, dependency edges, parameter
positions. Drive it table-driven over **every shipped skill that has an authored
`TaskTemplate`**, so the corpus grows for free as skills land. Today that is
`weaver-presets`, `weaver-templates`, and `weaver-from-scratch` under `embedded/skills/`.

This is not a one-off proof for P0 — it stays in CI permanently as the cheapest signal that a
distiller change broke fidelity.

A second property matters just as much for deputizing: **re-emission must be a fixpoint.**
Emitting a recovered template has to reproduce the same board, so
`distill(emit(distill(emit(T))))` stops changing. A template that drifted on every generation
would corrupt the skill a little more each time it ran.

Exit criteria for P0: clean round trip on ≥3 authored skills plus one hand-scored real session.

### What P0 and the P1a spike found

Five findings, all evidence-backed, several of which changed the plan above.

**From the P1a spike over a real 284-session corpus:**

3. **The task board is not the trace spine, because in practice it is empty.** 284 sessions,
   4,651 messages, 2,047 tool executions, and zero tasks or boards —
   `TaskBoardConfig{Enabled: false}` is the default
   ([`pkg/agent/registry.go:785`](../pkg/agent/registry.go:785)). Enabling boards is a config
   change, but no deployment that hasn't made it produces board-based traces, so
   `tool_executions` + `messages` must be the substrate and the board an enrichment.

4. **The best procedure in the corpus is in a trace where every one of its steps failed** — and
   the plan's original segmenter spec would have deleted it. One session asked for complete schema
   metadata for a Teradata database and issued nine well-formed catalog queries (database
   metadata, tables, row counts, columns, primary keys, foreign keys, indexes, table constraints,
   column constraints). All nine failed for a purely environmental reason: the MCP client was
   unavailable, then the circuit breaker opened. The remaining ~29 steps were genuine flailing —
   repeated identical searches, status-file writes, probing for `bteq` / `isql` / `teradatasql`.
   So the signal was 100% failed and the noise was mostly successful. **Intent lives in tool call
   inputs, not outcomes.** Any "only distill successful sessions" filter — the obvious first
   heuristic — throws away the single most valuable candidate available.

5. **The kind-decision rule needs tightening.** Those nine catalog queries are mutually
   independent, and the rule as written ("fan-out → workflow") therefore classifies them as a
   nine-branch parallel workflow. That is wrong: they are nine independent queries one agent runs
   in a turn. Fan-out must mean *multiple agents or genuine orchestration need*, not merely
   independent steps.

**From building P0:**

1. **Authored step order survives only where step indices do.** With the emitter's
   `SkillIdempotencyKey` present, recovery is exact. Without it — the real-work case — order can
   only be inferred from the dependency DAG, and that is genuinely ambiguous wherever the graph
   fans out. Two of the three shipped templates fan out (`weaver-templates` steps 4 and 5 both
   depend on 3; `weaver-from-scratch` steps 1 and 2 both depend on 0), so a board simply holds no
   record of which branch ran first. The distiller therefore reports unconstrained ordering as a
   warning rather than presenting a guess as evidence, and the oracle asserts *a valid*
   topological order in that mode instead of the authored one. Anything downstream that needs
   true ordering must get it from a richer trace source, not the board.

2. **`SkillTaskTemplate.RootTitle` is documented but not implemented.** Its comment says it
   "names the parent task created to group emitted children", but `emitTemplate` never creates
   that parent and never sets `ParentID`. All three shipped templates set `root_title` and all
   three lose it on a round trip. `TestRoundTrip_UnrecoverableTemplateFields` pins the gap so it
   reads as known rather than as a distiller bug; if the emitter gains root-task support that
   test will fail and the distiller should learn to recover it.

### Tier 3 — The LLM step

Two separate mechanisms, deliberately not mixed:

- **Recorded fixtures for CI.** Capture real LLM responses for the generalization step once and
  replay them. Deterministic, fast, race-safe. This tests *the code path*, not the model — a
  passing suite says the plumbing is intact, nothing more.
- **Eval suite for quality.** [`pkg/evals`](../pkg/evals) already provides `runner.go`,
  `golden.go`, `metrics.go`, and `judges/`. Build a hand-labeled corpus of
  (trace → expected candidate) pairs and score: did it pick the right kind, did it parameterize
  the right literals (precision/recall over parameters), did it drop the incidental steps.
  Runs on a schedule and before prompt changes — not on every commit.

For generated *skills* specifically, the curated repo `Teradata-PE/aai-tera-agentic-skills`
already runs `skill-validation.yml` plus single- and multi-turn skill evals. Decide whether to
call those pre-PR or port the checks into `pkg/skillvalidation` (see risk 5).

### Tier 4 — Adversarial input (highest-risk untested surface)

**A captured trace is untrusted input, and its text becomes a skill prompt.** Tool output, table
comments, query results, and user messages all flow into the candidate that a later session
loads with tool access. This is the most dangerous path in the feature and it must be tested as
such, not reviewed as such:

- Prompt injection carried in captured tool output attempting to write instructions into the
  generated skill's `prompt.instructions` or add tools to `tools.required`.
- Path traversal via candidate name into the skill write path. The importer already guards the
  analogous case ([`pkg/skills/importer/parse.go`](../pkg/skills/importer/parse.go)) — reuse the
  guard and test it here too.
- Oversized candidates (token-budget exhaustion), cyclic dependencies in emitted workflow DAGs,
  candidates naming nonexistent tools or agents.
- A candidate whose `risk_level` would let it bypass the approval gate.

Every one of these is a table-driven test with a hostile fixture, and each must fail closed.

### Tier 5 — Concurrency

Watch mode observes a live session while the agent is mutating the same task board, and shares
the `ProgressMultiplexer` with other listeners. Per project convention this is zero-tolerance:

```bash
go test -tags fts5 -race -count=50 ./pkg/apprentice
```

Cover: concurrent watch + task mutation, watch started/stopped mid-turn, two watchers on one
session, and multiplexer fan-out under load. `just race-check` for the extended run.

### Tier 6 — Privacy as assertions, not policy

Redaction and the cross-user boundary must be tests, not review checklist items:

- Fixtures seeded with known secrets and PII → assert absent from the persisted candidate.
- A candidate derived from user A's traces, surfaced to user B, contains **structure only** — no
  table names, query text, or file contents from A. Assert on the payload, not the intent.

### Tier 7 — Service, cloud, and TUI

- `ApprenticeService` RPC-level tests in `pkg/server`, following existing service test patterns.
- **RLS** (P2): a user cannot read another tenant's candidates. Uses tera-backend's existing
  Postgres test utilities.
- **Deputize wiring** (P2): candidate → `ContributeSkillToOrg` produces a valid PR payload
  against a mocked GitHub client; candidate → `CreateWorkflow` / `SetWorkflowSchedule` likewise.
- **TUI** (P1.5): Bubbletea message-flow tests following
  [`internal/tui/adapter/events_test.go`](../internal/tui/adapter/events_test.go) — watch started
  → progress messages → clarification asked → answered → candidate ready. Deterministic, no LLM.

### Coverage targets

Following the project's existing tiering: `pkg/apprentice` ≥ 60% (new code, mostly
deterministic, no excuse for less), validation and redaction paths ≥ 80% (critical path),
round-trip oracle at 100% of shipped skills carrying an authored `TaskTemplate`.

---

## Risks and open questions

1. **Generalization quality is the feature.** One trace generalizes badly. Watch mode should
   require the user to confirm parameters; observe mode should require N≥2 similar episodes
   before proposing. Never auto-deputize.
2. **Passive cross-user mining is a different consent class** from "watch this session." Needs
   org-level opt-in plus per-user opt-out, and a hard rule: a suggestion derived from other
   people's sessions may carry the *structure* of a procedure but never its payload. No table
   names, no query text, no file contents crossing a user boundary.
3. **Redaction happens before persist, not before display.** Captured traces contain prompts,
   SQL with real object names, and possibly PII.
4. **A captured trace is untrusted input, and it becomes a prompt.** Tool output, table
   comments, query results, and user messages flow from a trace into a candidate's
   `prompt.instructions` and `tools.required`, which a later session then loads with tool
   access — potentially for a different user. A hostile table comment or query result is an
   injection vector into generated automation. This is the most severe risk in the feature.
   Mitigation is a sanitization pass plus the adversarial tests in Tier 4, and it must be built
   in P1, not deferred to P2 with the rest of the security work.
5. **Cost.** An always-on observer LLM roughly doubles per-session tokens. Watch mode is
   explicit; observe mode must be batch/offline over stored traces, never inline per turn.
6. **Validation cannot live in the destination repo** — org repos are arbitrary GitHub slugs.
   Decide whether to reuse the eval workflows from `aai-tera-agentic-skills` pre-PR or port the
   checks into `pkg/skillvalidation`.
7. **Untracked steps.** The board is the spine but is incomplete by construction. Confirm the
   fill-in pass can attribute orphan tool calls to the right step, or mark the candidate
   low-confidence when it cannot.
8. **Candidate lifecycle** — proposed `PROPOSED | VALIDATED | DEPUTIZED | DISCARDED`. A
   discarded candidate must suppress re-proposal of the same procedure, or observe mode becomes
   a nag. Needs a stable procedure fingerprint (tested in Tier 1).
9. **Open**: does deputizing a SKILL candidate go straight to an org PR, or land in the user's
   private skill set first with promotion as a second, separate act? The marketplace supports
   both; the safer default is private-first.
