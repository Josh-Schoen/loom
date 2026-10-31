---
title: "ChatGPT Plan Reference"
weight: 36
---

# ChatGPT Plan Reference

The `chatgpt` provider answers with a ChatGPT user's plan (OpenAI's "Sign in with ChatGPT", plan usage) instead of an API key. Requests count against the user's plan usage limits; they are not billed per token.

**Version**: v1.4.0

**Status**: ⚠️ Partial. The sign-in, token refresh, request format, stream parsing and error mapping are implemented and tested against fake servers (`pkg/llm/chatgpt`). They have not been run against a live ChatGPT account. Plan usage is an OpenAI preview, and the details below come from OpenAI's Sign in with ChatGPT developer pages as of October 2026.

---

## Table of Contents

1. [Quick Reference](#quick-reference)
2. [Who May Use It](#who-may-use-it)
3. [Configuration](#configuration)
4. [CLI Commands](#cli-commands)
5. [Sign-In Flow](#sign-in-flow)
6. [Requests](#requests)
7. [Error Codes](#error-codes)
8. [Limitations](#limitations)
9. [Go API](#go-api)
10. [See Also](#see-also)

---

## Quick Reference

| Item | Value |
|------|-------|
| Provider name | `chatgpt` |
| Package | `pkg/llm/chatgpt` |
| Sign in | `looms chatgpt login` (system browser, loopback redirect) |
| Credentials file | `llm.chatgpt_credentials_file`, default `$LOOM_DATA_DIR/chatgpt.json` (mode 0600) |
| Model | `llm.chatgpt_model`: a slug from `looms chatgpt models` (required) |
| Bound user | `llm.chatgpt_user_id`: requests for any other loom user fail with `chatgpt.ErrOtherUser` |
| Endpoint | `POST https://api.openai.com/v1/responses`, `Authorization: Bearer <access token>` |
| Streaming | Always (plan usage requires `stream: true`) |
| Tool calling | Function tools |
| Cost | `Usage.CostUSD` is always 0; `CostsNothing()` returns true |
| Rate limiter | None (see [Limitations](#limitations)) |

---

## Who May Use It

These rules are OpenAI's (Sign in with ChatGPT terms, 2026-09-29), not loom's:

- **Access**: open-source tools and personal projects that run locally can register themselves on the first sign-in. Commercial or remotely hosted apps need OpenAI's approval through the Sign in with ChatGPT interest form.
- **One user per plan**: plan usage is only for requests made by the user who signed in. Pooling, sharing, or using one user's plan for another user's requests is prohibited. loom enforces part of this: a `chatgpt.Client` refuses any request whose context user ID (`types.UserIDFromContext`) differs from `llm.chatgpt_user_id`.
- **Runtime**: requests must come from a runtime the user controls, and tokens must be stored locally under the user's control. Background requests need the user's express consent.
- **Eligible plans**: the plans OpenAI lists (Plus and Pro; OpenAI's help pages also mention Go). Each user can cap an app's share of their weekly usage in ChatGPT settings.

---

## Configuration

### llm.provider

Set to `chatgpt`. Per-agent `llm.provider: chatgpt` also works; the agent's `llm.model` overrides `llm.chatgpt_model`.

### llm.chatgpt_model

**Type**: `string`
**Default**: `""`
**Required**: Yes, when `llm.provider` is `chatgpt` (config validation fails without it)

A model slug from `looms chatgpt models` (the plan's models with visibility `list`).

### llm.chatgpt_credentials_file

**Type**: `string`
**Default**: `""` (uses `$LOOM_DATA_DIR/chatgpt.json`)
**Required**: No

Where `looms chatgpt login` saves the credentials and the provider reads and refreshes them. One process may use a file. All providers in a `looms` process share one `TokenSource` per file, so refreshes are serialized.

### llm.chatgpt_user_id

**Type**: `string`
**Default**: `""`
**Required**: No

The loom user ID the plan belongs to. When set, only requests carrying that user ID are sent. When empty, only requests carrying no user ID are sent (a server without authentication, used by one person).

### llm.chatgpt_reasoning_effort

**Type**: `string`
**Default**: `""` (field left out)
**Required**: No

Sent as `reasoning.effort`, for example `low`, `medium` or `high`.

**Example**:

```yaml
llm:
  provider: chatgpt
  chatgpt_model: <slug from 'looms chatgpt models'>
  chatgpt_user_id: sam
```

---

## CLI Commands

| Command | Description |
|---------|-------------|
| `looms chatgpt login [--port N] [--no-browser] [--app-name NAME]` | Signs in. Opens the system browser, unless `--no-browser` is given, in which case the address is only printed. Waits up to 10 minutes for the redirect, then saves the credentials. `--app-name` (default `Loom`) is shown on the first sign-in. |
| `looms chatgpt status` | Shows the signed-in user, whether plan usage is granted, and when the access token expires. |
| `looms chatgpt models` | Lists `slug<TAB>display name` for the plan's models. |
| `looms chatgpt logout` | Revokes the refresh token at OpenAI and deletes the credentials file. If revoking fails, the file is still deleted and a warning is printed. |

The sign-in redirect is `http://127.0.0.1:<port>/auth/callback`, so the browser has to be on the same computer as `looms`. For a remote host, sign in on a local computer and copy the credentials file to the host (OpenAI's "self-hosted VMs" guidance); after that, the host refreshes by itself.

---

## Sign-In Flow

The OAuth 2.0 authorization code flow with PKCE (`S256`), with endpoints read from `https://auth.openai.com/.well-known/openid-configuration`:

1. **First sign-in**: `client_id=dynamic_agent_client`, `agent_name_hint=<app name>`. On every sign-in: `ext_agent_host_id` (a `urn:uuid:` kept in the credentials), `response_type=code`, `redirect_uri`, `state`, `nonce`, `code_challenge`, `scope=openid profile email offline_access resource.invoke chatgpt.tokens.use.direct`, `resource=https://api.openai.com/v1`.
2. **Callback**: the redirect returns `code`, `state` and the issued `client_id`, which is saved and used from then on. Later sign-ins send the issued ID and the same host ID.
3. **Code exchange**: `grant_type=authorization_code` with `client_id`, `code`, `code_verifier`, `redirect_uri` and `resource`. No client secret.
4. **ID token checks**: an RS256 signature against the issuer's JWKS, plus issuer, audience (the issued client ID), expiry and nonce. The granted scope must include `chatgpt.tokens.use.direct`, or the sign-in fails with `ErrSignInAgain`.
5. **Refresh**: `grant_type=refresh_token` with `client_id`, `refresh_token` and `resource`, and no `scope`. Access tokens last 1 hour and are refreshed 2 minutes early. The refresh token lasts 30 days and is replaced on every refresh. The new one is saved before it is used, because reusing a spent refresh token ends the grant.

---

## Requests

Each call sends a Responses API request with:

- `model`, `stream: true`, `store: false`
- `instructions`: all system messages, joined (plan usage rejects system-role items)
- `input`: the whole conversation (no `previous_response_id`). User text and images become `input_text` and `input_image`. Assistant text becomes `output_text`. Tool calls and results become `function_call` and `function_call_output`; a call without its result, or a result without its call, is dropped.
- `tools`: function tools (`type: function`, `name`, `description`, `parameters`). Names are sanitized with `llm.SanitizeToolName` and restored in responses.
- `reasoning.effort` when configured

These fields are never sent, because the preview rejects them: `temperature`, `top_p`, `max_output_tokens`, `metadata`, `user`, `truncation` and others.

From the stream, loom reads:

| Event | Use |
|-------|-----|
| `response.output_text.delta` | Text, passed to the token callback |
| `response.reasoning_summary_text.delta` | `LLMResponse.Thinking` |
| `response.output_item.done` | Tool calls (used when `response.completed` lists no output) |
| `response.completed` | The answer: output items, usage, status |
| `response.incomplete` | The answer so far, with `StopReason` `max_tokens` (or the reason given) |
| `response.failed`, `error` | A `*chatgpt.PlanError`, even though HTTP was 200 |

A stream that ends without `response.completed` or `response.incomplete` is an error.

Usage is mapped as follows. `InputTokens` excludes `input_tokens_details.cached_tokens`, which become `CacheReadInputTokens`.

---

## Error Codes

| Go error (`errors.Is`) | Cause | What to do |
|------------------------|-------|------------|
| `chatgpt.ErrUsageLimit` | `subscription_sharing_usage_limit_exceeded` (HTTP 429, or `response.failed` mid-stream): the plan's limit or the user's per-app cap is reached | Wait for the limit to reset, or raise the app's limit in ChatGPT settings. Not retried. |
| `chatgpt.ErrNotEligible` | `subscription_sharing_user_not_eligible` (HTTP 403): the plan or region can't share usage | Use another provider |
| `chatgpt.ErrSignInAgain` | The refresh token expired, was reused or was revoked (`invalid_grant`, `refresh_token_reused`, `refresh_token_expired`, …); `subscription_sharing_invalid_user`; `chatpass_v2_scope_not_authorized`; HTTP 401 after one refresh | `looms chatgpt login` |
| `chatgpt.ErrNotSignedIn` | No credentials file | `looms chatgpt login` |
| `chatgpt.ErrOtherUser` | The request is for a different loom user than `llm.chatgpt_user_id` | Nothing is sent. Use another provider for that user. |
| `*chatgpt.PlanError` with `Retryable()` true | `subscription_sharing_usage_unavailable`, HTTP 502/503/504 | Retry later |
| `*chatgpt.PlanError` (other) | e.g. `subscription_sharing_unsupported_capability` (HTTP 400, `Param` names the field) | Fix the request |

An HTTP 401 makes the client refresh the access token and retry once.

---

## Limitations

- **Not run against a live account.** Request and response shapes follow OpenAI's documentation; only fake servers were used in tests.
- **Tool shape unverified**: some reports say plan usage wants function tools grouped in a `namespace` tool. loom sends plain function tools.
- **Hosted tools unsupported** by plan usage: file search, code interpreter, image generation, hosted MCP. Audio is unsupported too.
- **No rate limiter**: plan usage has no per-minute token limits to pace, and loom's rate limiter retries any error containing "429", which would repeat requests refused for the plan's usage limit.
- **One user per process and credentials file.** Several users would need a provider per user, which loom does not route yet.
- **Not in `pkg/llm/factory`**: `SwitchModel` can't switch to `chatgpt` at runtime, and `looms workflow` doesn't offer it as a default provider.
- **No device-code flow**: Sign in with ChatGPT has none. The browser must reach `127.0.0.1` on the `looms` host.

---

## Go API

```go
import "github.com/teradata-labs/loom/pkg/llm/chatgpt"

// Sign in (once).
creds, err := (&chatgpt.Auth{}).SignIn(ctx, chatgpt.SignInOptions{
    AppName:     "My App",
    OpenBrowser: openInSystemBrowser,
})
store := chatgpt.Store{Path: "/path/chatgpt.json"}
err = store.Save(creds)

// Answer with the plan.
ts := &chatgpt.TokenSource{Store: store, Auth: &chatgpt.Auth{}}
models, err := chatgpt.ListModels(ctx, ts, "", nil)
client, err := chatgpt.NewClient(chatgpt.Config{Model: models[0].Slug, Tokens: ts, UserID: "sam"})
resp, err := client.Chat(types.ContextWithUserID(ctx, "sam"), messages, tools)
```

**Thread safety**: `Client`, `Auth` and `TokenSource` are safe for concurrent use. Two processes must not share one credentials file.

---

## See Also

- [LLM Providers](llm-providers.md)
- [OpenAI](llm-openai.md): the API-key provider (Chat Completions)
