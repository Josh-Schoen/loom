# Scripted Provider Reference

A development and test LLM provider that answers from a YAML script instead of
a model. `looms serve` runs its full stack (gRPC, HTTP gateway, SSE, sessions,
tool execution, client-executed tools) with no API key and deterministic
replies. It does not understand language: a rule matches only what its regular
expression matches.

**Available since**: Unreleased (after v1.4.0)
**Provider name**: `scripted`
**Package**: `pkg/llm/scripted`

---

## Quick Reference

| Item | Value |
|------|-------|
| Config key | `llm.provider: scripted` |
| Script path | `llm.scripted_script_path` (required) |
| Model reported | script file name without extension |
| Streaming | yes (simulated chunks) |
| Tool calls | yes (scripted) |
| Cost | always `0`; token counts are estimates (characters / 4) |
| Scope | server-level `llm` only; per-agent `llm.provider: scripted` overrides are not supported |

---

## Configuration

```yaml
llm:
  provider: scripted
  scripted_script_path: /path/to/script.yaml
```

`looms serve` validates the script at startup and refuses to start on an
invalid one. It logs a warning that replies come from a script.

---

## Script Format

```yaml
rules:                      # tried in order; the first match answers
  - name: add-reminder      # optional; used in error messages
    when:
      user_message: '(?i)remind me (?:to )?(.+)'   # regex on the newest user message
      tool_offered: device_add_reminder            # tool must be advertised on the call
    call_tool:
      name: device_add_reminder
      arguments:                                   # values are templates; sent as strings
        title: '{{.Group 1}}'
        due: '{{dateAt 1 9}}'
  - name: reminder-result
    when:
      after_tool: device_add_reminder              # newest message is this tool's result
    reply: '{{if .ToolSucceeded}}Saved.{{else}}Not saved: {{.ToolResult}}{{end}}'
default_reply: 'No rule matched: {{.UserMessage}}'
timing:
  first_token_ms: 300       # delay before the first chunk (default 300)
  chunk_chars: 8            # characters per streamed chunk (default 8)
  chunk_delay_ms: 20        # delay between chunks (default 20)
```

### Rules

| Field | Type | Description |
|-------|------|-------------|
| `name` | string | Label for error messages. |
| `when.user_message` | regex (Go RE2) | Must match the newest user message. Capture groups feed `.Group n`. |
| `when.tool_offered` | string | A tool with this name must be advertised on the provider call. |
| `when.after_tool` | string | The newest message must be this tool's result. |
| `call_tool.name` | string | Tool to call. |
| `call_tool.arguments` | map of string templates | Tool arguments; every value is sent as a string. |
| `reply` | string template | Text reply. |

Each rule sets exactly one of `call_tool` and `reply`.

**Matching**:
- A rule **without** `after_tool` matches only when the newest message is from the user.
- A rule **with** `after_tool` matches only when the newest message is that tool's result.

So a tool-calling rule never fires again on its own result.

When no rule matches, `default_reply` answers. A script needs at least one rule
or a `default_reply`. Unknown fields are errors.

### Template Data

Templates use Go `text/template`.

| Name | Type | Description |
|------|------|-------------|
| `.UserMessage` | string | Newest user message. |
| `.Group n` | string | Capture group `n` of `when.user_message` (`0` is the whole match); `""` when absent. |
| `.ToolName` | string | Tool whose result is the newest message. |
| `.ToolResult` | string | That result's content as a model would read it. |
| `.ToolSucceeded` | bool | Whether that tool call succeeded. |
| `dateAt days hour` | function | RFC 3339 UTC timestamp `days` from today at `hour`:00. |

A template that fails at run time renders as
`[scripted provider template error: …]` in the reply.

---

## Errors

| Condition | Result |
|-----------|--------|
| `scripted_script_path` empty | startup error: `llm.scripted_script_path is required for the scripted provider` |
| File missing or unreadable | startup error: `scripted: read <path>: …` |
| Invalid YAML or unknown field | startup error: `scripted: parse script: …` |
| Rule with both or neither of `call_tool` / `reply` | startup error: `set exactly one of call_tool and reply` |
| Invalid regex or template | startup error naming the rule and field |

---

## Example: Client Tool Round Trip

With the script above:

```bash
curl -N -X POST http://localhost:5006/v1/weave:stream \
  -H "Content-Type: application/json" \
  -d '{"query":"Remind me to buy milk","client_tools":[{"name":"device_add_reminder","input_schema_json":"{\"type\":\"object\"}"}]}'
```

The stream carries a `client_tool_call` with
`"arguments_json":"{\"due\":\"<tomorrow>T09:00:00Z\",\"title\":\"buy milk\"}"`.
Posting `{"tool_call_id": "<id>", "success": true}` to
`/v1/sessions/<session_id>/client-tool-results` returns `{"accepted":true}`, and
the stream finishes with the `reminder-result` reply. See
[Client-Executed Tools](streaming.md#client-executed-tools).

---

## See Also

- [LLM Provider Support](llm-providers.md)
- [Streaming Reference](streaming.md)
