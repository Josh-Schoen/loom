# MCP Apps Reference

Declarative spec format for building interactive UI apps rendered as MCP resources.

**Status**: ✅ Implemented (v1.1.0)

## Table of Contents

- [Overview](#overview)
- [Spec Format](#spec-format)
- [Layouts](#layouts)
- [Component Types](#component-types)
  - [Display Components](#display-components)
  - [Layout Components](#layout-components)
  - [Complex Components](#complex-components)
- [Colors](#colors)
- [Limits](#limits)
- [APIs](#apis)
  - [gRPC RPCs](#grpc-rpcs)
  - [MCP Tools](#mcp-tools)
  - [Agent Tools](#agent-tools)
- [Ownership](#ownership)
- [Persistence](#persistence)
- [Checklist State](#checklist-state)
- [HTTP Access Control](#http-access-control)
- [Security Constraints](#security-constraints)

## Overview

MCP Apps are standalone HTML documents compiled from a declarative JSON spec. They can be created via:

- **gRPC**: `CreateUIApp` / `UpdateUIApp` / `DeleteUIApp` RPCs
- **MCP tools**: `loom_create_app` / `loom_update_app` / `loom_delete_app` (for Claude Code, Cursor, etc.)
- **Agent tools**: `create_ui_app` / `update_ui_app` / `delete_ui_app` / `get_ui_app` (lazily disclosed to all server-side agents)

Apps are served as MCP resources at `ui://loom/<name>` and via HTTP at `/apps/<name>`.

There are 4 built-in (embedded) apps, shared by every user, and up to 100 dynamic apps per user created at runtime. Dynamic apps belong to the user who created them (see [Ownership](#ownership)).

## Spec Format

```json
{
  "version": "1.0",
  "title": "Dashboard Title",
  "description": "Optional subtitle",
  "badge": "Live",
  "layout": "stack",
  "components": [
    {
      "type": "header",
      "props": { "title": "My Dashboard" }
    },
    {
      "type": "chart",
      "props": { "chartType": "bar", "labels": ["A", "B"], "datasets": [...] }
    }
  ]
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `version` | string | Yes | Must be `"1.0"` |
| `title` | string | No | HTML `<title>` and header fallback |
| `description` | string | No | Subtitle shown below the title |
| `badge` | string | No | Badge text in the header (default: `"MCP App"`) |
| `layout` | string | No | Top-level layout mode (default: `"stack"`) |
| `components` | array | Yes | Array of `UIComponent` objects (at least 1) |
| `data_type` | string | No | postMessage type for live data updates |

Each component has:

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `type` | string | Yes | Component type from the catalog below |
| `props` | object | No | Type-specific properties |
| `children` | array | No | Nested components (only for layout types: `section`, `tabs`) |
| `id` | string | No | ID for targeting with postMessage data updates |

## Layouts

The `layout` field controls how top-level components are arranged:

| Layout | Description |
|--------|-------------|
| `stack` | Vertical stack (default). Components fill the full width. |
| `grid-2` | Two-column grid. Responsive: collapses to single column on narrow screens. |
| `grid-3` | Three-column grid. Responsive: collapses to single column on narrow screens. |

## Component Types

14 built-in component types across 3 categories.

### Display Components

#### `stat-cards`

Row of KPI stat cards with label, value, optional color and sublabel.

**Props:**

| Prop | Type | Required | Description |
|------|------|----------|-------------|
| `items` | array | Yes | Array of `{label, value, color?, sublabel?}` |

Each item:
- `label` (string, required): Metric name
- `value` (string, required): Display value
- `color` (string): Named color or hex
- `sublabel` (string): Delta or context text

```json
{
  "type": "stat-cards",
  "props": {
    "items": [
      {"label": "Total Revenue", "value": "$4.54M", "color": "success"},
      {"label": "Active Users", "value": "2,100", "color": "accent"}
    ]
  }
}
```

---

#### `chart`

Chart.js chart supporting 8 chart types.

**Props:**

| Prop | Type | Required | Description |
|------|------|----------|-------------|
| `chartType` | string | Yes | `bar`, `line`, `pie`, `doughnut`, `radar`, `polarArea`, `scatter`, `bubble` |
| `title` | string | No | Chart title |
| `labels` | array | Yes | X-axis labels (array of strings) |
| `datasets` | array | Yes | Array of `{label, data, color?}` |
| `fill` | boolean | No | Fill area under line charts |
| `stacked` | boolean | No | Stack bar/line datasets |

Each dataset:
- `label` (string, required): Legend label
- `data` (array of numbers, required): Data values
- `color` (string): Named color or hex

```json
{
  "type": "chart",
  "props": {
    "chartType": "bar",
    "title": "Monthly Revenue",
    "labels": ["Jan", "Feb", "Mar"],
    "datasets": [
      {"label": "Revenue", "data": [320, 380, 410], "color": "accent"}
    ]
  }
}
```

---

#### `table`

Data table with columns, rows, optional sorting and max height.

**Props:**

| Prop | Type | Required | Description |
|------|------|----------|-------------|
| `title` | string | No | Table title |
| `columns` | array | Yes | Column headers (array of strings) |
| `rows` | array | Yes | 2D array of strings (each row is an array) |
| `sortable` | boolean | No | Enable click-to-sort on column headers |
| `maxHeight` | string | No | CSS max-height (e.g., `"400px"`) |

```json
{
  "type": "table",
  "props": {
    "title": "Top Customers",
    "columns": ["Customer", "Revenue"],
    "rows": [["Acme Corp", "$1.2M"], ["GlobalTech", "$890K"]],
    "sortable": true
  }
}
```

---

#### `key-value`

Key-value metadata pairs in grid or list layout.

**Props:**

| Prop | Type | Required | Description |
|------|------|----------|-------------|
| `title` | string | No | Section title |
| `items` | array | Yes | Array of `{key, value, color?}` |
| `layout` | string | No | `"grid"` (default) or `"list"` |

```json
{
  "type": "key-value",
  "props": {
    "title": "Database Info",
    "items": [
      {"key": "Host", "value": "prod-db-01"},
      {"key": "Status", "value": "Online", "color": "success"}
    ]
  }
}
```

---

#### `text`

Text block with optional styling.

**Props:**

| Prop | Type | Required | Description |
|------|------|----------|-------------|
| `content` | string | Yes | Text content |
| `style` | string | No | `"default"`, `"note"`, `"warning"`, or `"error"` |

```json
{
  "type": "text",
  "props": {
    "content": "Analysis complete. 3 anomalies detected.",
    "style": "warning"
  }
}
```

---

#### `code-block`

Monospace code display with optional title and language hint.

**Props:**

| Prop | Type | Required | Description |
|------|------|----------|-------------|
| `code` | string | Yes | Code content |
| `title` | string | No | Title above the block |
| `language` | string | No | Language hint (e.g., `"sql"`, `"json"`) |

```json
{
  "type": "code-block",
  "props": {
    "title": "Generated SQL",
    "language": "sql",
    "code": "SELECT customer_id, SUM(amount) FROM orders GROUP BY 1"
  }
}
```

---

#### `progress-bar`

Percentage progress bars with labels and colored fills.

**Props:**

| Prop | Type | Required | Description |
|------|------|----------|-------------|
| `title` | string | No | Section title |
| `items` | array | Yes | Array of `{label, value, color?}` |
| `thresholds` | object | No | Auto-color thresholds: `{warning: 60, error: 80}` |

Each item:
- `label` (string, required): Bar label
- `value` (number, required): 0-100 percentage
- `color` (string): Named color or hex

```json
{
  "type": "progress-bar",
  "props": {
    "title": "Storage Usage",
    "items": [
      {"label": "Disk", "value": 72, "color": "warning"},
      {"label": "Memory", "value": 45, "color": "success"}
    ]
  }
}
```

---

#### `badges`

Inline colored status badges/pills.

**Props:**

| Prop | Type | Required | Description |
|------|------|----------|-------------|
| `items` | array | Yes | Array of `{text, color}` |

```json
{
  "type": "badges",
  "props": {
    "items": [
      {"text": "Production", "color": "success"},
      {"text": "v2.1.0", "color": "accent"},
      {"text": "3 Warnings", "color": "warning"}
    ]
  }
}
```

---

#### `heatmap`

Color-coded grid with row labels, column labels, and numeric values.

**Props:**

| Prop | Type | Required | Description |
|------|------|----------|-------------|
| `title` | string | No | Heatmap title |
| `rowLabels` | array | Yes | Row labels (array of strings) |
| `columnLabels` | array | Yes | Column labels (array of strings) |
| `values` | array | Yes | 2D array of numbers (rows x columns) |
| `colorScale` | string | No | `"blue"` (default), `"green"`, or `"red"` |

```json
{
  "type": "heatmap",
  "props": {
    "title": "Query Latency (ms)",
    "rowLabels": ["Mon", "Tue", "Wed"],
    "columnLabels": ["Morning", "Afternoon", "Evening"],
    "values": [[120, 150, 90], [200, 180, 110], [95, 130, 85]]
  }
}
```

---

### Layout Components

#### `header`

App header with title, optional description and badge.

**Props:**

| Prop | Type | Required | Description |
|------|------|----------|-------------|
| `title` | string | Yes | Header title |
| `description` | string | No | Subtitle text |
| `badge` | string | No | Badge text |

```json
{
  "type": "header",
  "props": {
    "title": "Revenue Analysis",
    "description": "Q1 2026 Summary",
    "badge": "Live"
  }
}
```

---

#### `section`

Collapsible section grouping child components. **Supports `children`.**

**Props:**

| Prop | Type | Required | Description |
|------|------|----------|-------------|
| `title` | string | Yes | Section title |
| `subtitle` | string | No | Subtitle text |
| `collapsible` | boolean | No | Enable collapse/expand toggle |

```json
{
  "type": "section",
  "props": {"title": "Details", "collapsible": true},
  "children": [
    {"type": "text", "props": {"content": "Section content here"}},
    {"type": "key-value", "props": {"items": [{"key": "Status", "value": "OK"}]}}
  ]
}
```

---

#### `tabs`

Tab bar with child components per tab. **Supports `children`.**

Each child component maps 1:1 to a tab defined in `props.tabs`. The first child renders under the first tab, etc.

**Props:**

| Prop | Type | Required | Description |
|------|------|----------|-------------|
| `tabs` | array | Yes | Array of `{label}` objects |

```json
{
  "type": "tabs",
  "props": {
    "tabs": [{"label": "Overview"}, {"label": "Details"}]
  },
  "children": [
    {"type": "text", "props": {"content": "Overview tab content"}},
    {"type": "table", "props": {"columns": ["Col"], "rows": [["Data"]]}}
  ]
}
```

---

### Complex Components

#### `dag`

Directed acyclic graph rendered as SVG with nodes and edges.

**Props:**

| Prop | Type | Required | Description |
|------|------|----------|-------------|
| `title` | string | No | DAG title |
| `nodes` | array | Yes | Array of `{id, label, sublabel?, color?}` |
| `edges` | array | Yes | Array of `{from, to}` |

```json
{
  "type": "dag",
  "props": {
    "title": "Pipeline",
    "nodes": [
      {"id": "a", "label": "Extract"},
      {"id": "b", "label": "Transform"},
      {"id": "c", "label": "Load"}
    ],
    "edges": [
      {"from": "a", "to": "b"},
      {"from": "b", "to": "c"}
    ]
  }
}
```

---

#### `message-list`

Conversation message list with role-based styling.

**Props:**

| Prop | Type | Required | Description |
|------|------|----------|-------------|
| `messages` | array | Yes | Array of `{role, content, timestamp?}` |

Roles: `"user"`, `"assistant"`, `"system"`, `"tool"`

```json
{
  "type": "message-list",
  "props": {
    "messages": [
      {"role": "user", "content": "Show me revenue trends"},
      {"role": "assistant", "content": "Here's the analysis..."}
    ]
  }
}
```

---

#### `checklist`

Tappable list of items. Tapping a row toggles its `checked` value in the page and sends a `ui/notifications/checklist-changed` notification to the host (see [Checklist State](#checklist-state)). The component shows "N of M done" under the list.

**Props:**

| Prop | Type | Required | Description |
|------|------|----------|-------------|
| `title` | string | No | Checklist title |
| `items` | array | Yes | Array of `{id?, label, detail?, checked?}` |

**Constraints** (validation errors on create/update):

- The component needs an `id` (it is how a host addresses it).
- Each item needs `label` or `id`. An item's identifier is its `id`, or its `label` when it has no `id`; identifiers must be unique within the checklist.
- `checked` defaults to `false`.

```json
{
  "type": "checklist",
  "id": "groceries",
  "props": {
    "title": "Groceries",
    "items": [
      {"id": "peppers", "label": "Bell peppers", "detail": "2, any color"},
      {"id": "tortillas", "label": "Corn tortillas", "checked": true}
    ]
  }
}
```

---

## Colors

All components that accept a `color` prop use the Tokyonight Dark theme palette:

| Name | Use For | CSS Variable |
|------|---------|--------------|
| `accent` | Primary / informational | `--accent` (#7aa2f7) |
| `success` | Positive / healthy | `--success` (#9ece6a) |
| `warning` | Caution / degraded | `--warning` (#e0af68) |
| `error` | Failure / critical | `--error` (#f7768e) |
| `cyan` | Secondary highlight | `--cyan` (#7dcfff) |
| `magenta` | Tertiary highlight | `--magenta` (#bb9af7) |

You can also pass hex colors (e.g., `"#ff6b6b"`), but named colors are preferred for visual consistency.

## Limits

| Constraint | Value |
|------------|-------|
| Max components per spec | 50 |
| Max nesting depth | 10 |
| Max props size per component | 64 KB |
| Max total spec size | 512 KB |
| Max dynamic apps (per owner) | 100 |
| Max total dynamic app storage (per owner) | 50 MB |
| App name pattern | `^[a-z0-9][a-z0-9-]{0,62}$` |
| Reserved names | `component-types` |

## APIs

### gRPC RPCs

8 RPCs in `LoomService` (defined in `proto/loom/v1/loom.proto`):

| RPC | HTTP | Description |
|-----|------|-------------|
| `ListUIApps` | `GET /v1/apps` | List all registered apps |
| `GetUIApp` | `GET /v1/apps/{name}` | Get app metadata and compiled HTML |
| `CreateUIApp` | `POST /v1/apps` | Create a dynamic app from a spec |
| `UpdateUIApp` | `PUT /v1/apps/{name}` | Update an existing dynamic app |
| `DeleteUIApp` | `DELETE /v1/apps/{name}` | Delete a dynamic app |
| `ListComponentTypes` | `GET /v1/apps/component-types` | Discover available component types |
| `SetUIAppChecklistItem` | `POST /v1/apps/{name}:set-checklist-item` | Tick or untick one checklist item and save the app (see [Checklist State](#checklist-state)) |
| `SetUIAppSharing` | `POST /v1/apps/{name}:set-sharing` | Share one of the caller's apps with everyone, or stop sharing one the caller shared (`{"shared": true}` or `false`; see [Shared apps](#shared-apps)) |

### MCP Tools

Exposed by `loom-mcp` bridge to MCP clients (Claude Code, Cursor, etc.):

| Tool | Description |
|------|-------------|
| `loom_create_app` | Create a dynamic app from a declarative spec |
| `loom_update_app` | Update an existing dynamic app |
| `loom_delete_app` | Delete a dynamic app |
| `loom_list_component_types` | Discover available component types and prop schemas |

### Agent Tools

Registered to all server-side Loom agents in one of two ways:

- **Lazy disclosure** (default): the tools appear in the active tool set only when the user message contains UI-related keywords like "dashboard", "chart", "visualization", etc.
- **Always offered**: an agent whose YAML lists any of these tools under `tools.builtin` (or the flat `tools:` list) has all six from the first turn, whatever the wording. Use this for assistants that build pages on request ("plan my meals for the week"). The startup log shows `mode: declared` or `mode: lazy` per agent.

```yaml
spec:
  tools:
    - create_ui_app
    - update_ui_app
```

| Tool | Description |
|------|-------------|
| `create_ui_app` | Create a dynamic app from a declarative spec |
| `update_ui_app` | Update an existing dynamic app |
| `delete_ui_app` | Delete a dynamic app |
| `list_component_types` | Discover available component types and prop schemas |
| `get_ui_app` | Return an app's current spec (`{name, spec}`), including checklist ticks. Fails with `NOT_FOUND` for unknown apps or apps without a stored spec |
| `share_ui_app` | `{name, shared}`: share one of the person's apps with everyone (`shared`, default true) or stop sharing one they shared (`false`). Errors `NAME_IN_USE`, `NOT_SHARER` (see [Shared apps](#shared-apps)) |

`create_ui_app` also takes `shared` (boolean, default false) to create the app as a shared app.

Agents should call `list_component_types` first to discover the catalog, then use `create_ui_app` with a spec. Before `update_ui_app` on an app with a checklist, agents should call `get_ui_app` and keep each item's `checked` value; `update_ui_app` replaces the whole spec, so ticks missing from the new spec are lost.

## Ownership

Each dynamic app belongs to one user, its owner: the user ID of the request
that created it. For RPCs and `/apps/` pages that is the caller's identity
(set by the authentication, device-pairing, or user-ID interceptors); for the
agent tools it is the user who sent the message the agent is answering.
Requests without an identity act as `default-user` (`types.DefaultUserID`).

- `ListUIApps`, `GetUIApp`, `UpdateUIApp`, `DeleteUIApp`,
  `SetUIAppChecklistItem`, `/apps/`, and the agent tools see the embedded apps,
  the caller's own dynamic apps, and the [shared apps](#shared-apps). Another
  user's own app answers as not found (`NOT_FOUND` / HTTP 404), so its
  existence is not revealed (except through the shared-app name rules below).
- Names are unique per owner: two users can each have an app called
  `meal-plan`. Names of embedded apps are reserved for everyone.
- Limits (100 apps, 50 MB) apply per owner.
- The MCP resource view of the registry (`List`, `Read` on `loom-mcp` and
  embedded hosts) has no user identity and shows `default-user`'s apps.

### Shared apps

A dynamic app can be **shared**: everyone on the server sees it and can
change it (`UpdateUIApp`, `SetUIAppChecklistItem`, the agent tools), for
example a household's grocery list on a personal assistant server. Create
one with `CreateUIApp` `shared: true` (or `create_ui_app` `shared: true`), or
share an existing app with `SetUIAppSharing` / `share_ui_app`.

- `UIApp.shared` is true and `UIApp.shared_by` is the user ID of the person
  who shared it. Only that user can delete it (`DeleteUIApp`) or stop sharing
  it (`SetUIAppSharing` `shared: false`, which makes it their own app again);
  others get `FAILED_PRECONDITION`.
- A name means one app for every user: a shared app's name cannot be used by
  any user's own app. Creating an own app with a shared app's name, creating
  a shared app with a name someone uses for their own app, and sharing an app
  whose name another user also uses return `FAILED_PRECONDITION` (agent
  tools: `NAME_IN_USE`). The error for the last two reveals that some user
  has an app with that name.
- Updates keep an app shared and keep `shared_by`. Shared apps count against
  their own limits (100 apps, 50 MB), separate from each user's.
- Shared apps are stored under the reserved owner `"\x01shared"` (user IDs
  cannot contain control characters; `looms pair --user` rejects them too).

**Only as strong as the identity.** With no authentication configured, the
user ID comes from the `X-User-Id` header (`Grpc-Metadata-X-User-Id` on the
gateway), which any caller can set, so ownership separates users' apps but
does not protect them from each other. To enforce it, turn on
`server.auth.enabled` or `server.device_pairing.required`; a paired device
always acts as the user it was paired for (`looms pair --user`) and cannot
override it with the header.

## Persistence

`looms serve` stores each dynamic app as a JSON file: `default-user`'s in `$LOOM_DATA_DIR/apps/<name>.json` (where all apps were stored before apps had owners, so existing apps stay `default-user`'s), and every other owner's in `$LOOM_DATA_DIR/apps/users/<key>/<name>.json`, where `<key>` is the first 32 hex characters of the SHA-256 of the owner's user ID. Each file (mode 0600) holds the compiled HTML, the spec it was compiled from, display name, description, `updated_at`, and, outside the top directory, the owner's user ID. The server loads them at startup, so apps survive restarts. Create, update, and delete write the file before the change is visible; if the write fails, the operation fails and the previous version stays. Unreadable files, files whose `name` does not match the file name, files whose owner does not match their directory, and files beyond the registry limits are skipped (logged) and left in place. Embedded apps are not stored.

The spec is stored for apps created or updated from a spec (`CreateUIApp`, `UpdateUIApp`, `create_ui_app`, `update_ui_app`). Files written before specs were stored have none; such apps load and serve as before, but `get_ui_app` and `SetUIAppChecklistItem` fail for them until the next update.

`UIApp.updated_at` (`updatedAt` on the gateway) is the last create or update of a dynamic app; unset for embedded apps.

## Checklist State

A page shows checklist ticks immediately; saving them is the host's job. On each toggle the page posts this JSON-RPC notification to `window.parent` (only when it is framed; target origin is the origin of the host's first response, or `*` before one):

```json
{
  "jsonrpc": "2.0",
  "method": "ui/notifications/checklist-changed",
  "params": {"component_id": "groceries", "item_id": "peppers", "checked": true}
}
```

A host that renders the page unframed (a native web view) can listen for the same message with a `message` event listener injected at document start.

The host saves the tick with `SetUIAppChecklistItem`:

```bash
curl -X POST http://localhost:5006/v1/apps/groceries:set-checklist-item \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"component_id": "groceries", "item_id": "peppers", "checked": true}'
```

The server loads the stored spec, finds the checklist by `id` at any depth (inside `section`/`tabs` children), sets the item's `checked`, recompiles, and saves (same persistence rules as `UpdateUIApp`). Calls are serialized server-wide, so concurrent ticks on one page all land. The response is `{"app": UIApp}` with the new `updated_at`; display name and description are unchanged.

| Code | Condition |
|------|-----------|
| `INVALID_ARGUMENT` | Invalid app name, or empty `component_id` / `item_id` |
| `NOT_FOUND` | Unknown app, no checklist with that `component_id`, or no item with that identifier |
| `FAILED_PRECONDITION` | The app has no stored spec, or the server has no spec-keeping app store or compiler |
| `INTERNAL` | Stored spec unreadable, recompile failed, or the save failed (the previous version stays) |

Ticks are stored in the spec, so they survive restarts and appear on every device of the app's owner.

## HTTP Access Control

`/apps/` and `/apps/<name>` run the same authentication and user-identity interceptors as the gateway's `ListUIApps` and `GetUIApp`. With no authentication configured they stay open as before. With `server.auth.enabled` or `server.device_pairing.required`, a request needs `Authorization: Bearer <token>` (`401` otherwise), so a plain browser cannot open them; clients fetch `GET /v1/apps/{name}` (HTML in `content`, base64) with their credentials instead.

## Security Constraints

The compiler validates and sanitizes all specs before compilation:

- **Prototype pollution prevention**: Keys `__proto__`, `constructor`, `prototype` are rejected at any depth
- **Script injection prevention**: Values starting with `javascript:`, `vbscript:`, `data:text/html` are rejected
- **CSS injection prevention**: Values containing `url(`, `expression(`, `@import` are rejected
- **HTML injection prevention**: `<`, `>`, `&` in spec JSON are escaped to JSON unicode sequences (`\u003c`, `\u003e`, `\u0026`) for safe embedding inside `<script>` tags
- **CSP**: Compiled HTML includes `Content-Security-Policy` meta tag with: `default-src 'none'`; `script-src 'unsafe-inline' https://cdn.jsdelivr.net`; `style-src 'unsafe-inline'`; `img-src data:`; `connect-src 'none'`; `form-action 'none'`
- **DOM safety**: Runtime uses `textContent` exclusively (no `innerHTML`), SVG uses strict element/attribute allowlists
- **Chart.js**: Loaded with SRI hash from `cdn.jsdelivr.net` (pinned to v4.4.7)
