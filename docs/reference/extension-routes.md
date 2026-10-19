---
title: "Extension Routes Reference"
weight: 48
---

# Extension Routes Reference

Serve a service that runs on the same machine as loom on loom's HTTP gateway,
behind loom's authentication. loom authenticates each request the way it
authenticates its own routes (device tokens, Supabase JWTs, the user ID
interceptor). It then forwards the request to the service with the caller's
user ID. Clients keep one address, one certificate, and one credential.

**Status**: ✅ Implemented (`pkg/server/extensions.go`, `server.extensions` in
`looms.yaml`). HTTP gateway only. gRPC clients can't reach extension routes.

---

## Configuration

```yaml
server:
  extensions:
    - name: household                     # lowercase letters, digits, hyphens; at most 32
      path_prefix: /v1/household/         # two segments or more, ending in /
      upstream: http://127.0.0.1:5017     # must be on this machine
      secret: "${HOUSEHOLD_SECRET}"       # sent as X-Loom-Extension-Secret; ${ENV} expanded
      allow_devices: true                 # paired devices may call it (default: false)
```

| Field | Required | Meaning |
|-------|----------|---------|
| `name` | yes | Identifies the route in logs and its method name, `/loom.extension/<name>` |
| `path_prefix` | yes | Requests whose path starts with it are forwarded, with the full path and query unchanged |
| `upstream` | yes | `http` or `https` base URL on `localhost` or a loopback address. Its path, if any, is joined before the request's |
| `secret` | no | Sent as `X-Loom-Extension-Secret`, so the service accepts only requests loom forwarded |
| `allow_devices` | no | Adds `/loom.extension/<name>` to what paired devices may call |

An invalid entry stops `looms serve` at startup. Rejected entries:
- a prefix that overlaps loom's own routes (`/health`, `/swagger-ui`,
  `/openapi.json`, `/apps`, `/v1/weave`, `/v1/pairing`) or another extension;
- an upstream that isn't on this machine.

Extension routes take precedence over gateway routes under the same prefix,
so choose a prefix no loom RPC uses.

## What the Service Receives

| Header | Value |
|--------|-------|
| `X-Loom-User-Id` | The authenticated caller's user ID (`default-user` when no identity is configured) |
| `X-Loom-Extension-Secret` | The route's `secret`, when set |

Removed before forwarding: `Authorization`, `Cookie`, `X-User-Id`, and every
`X-Loom-*` and `Grpc-Metadata-*` header the client sent. A client therefore
cannot claim another user's ID or supply the secret.

## Errors

| Status | When |
|--------|------|
| 401 | No credential, or an unknown one, when authentication is required |
| 403 | A paired device calling a route without `allow_devices` |
| 502 | The service can't be reached (its address isn't shown to the client) |

Otherwise the service's response is returned as it is.

## Security Notes

- **Only as strong as the identity.** Without `server.auth.enabled` or
  `server.device_pairing.required`, callers can set `X-User-Id` on loom's own
  routes, and `X-Loom-User-Id` carries that value.
- **The service must accept only requests from loom.** Listen on loopback
  only, and check `X-Loom-Extension-Secret` with a constant-time comparison.
- To let agents use the same service's tools for the right person, see
  `forward_caller_identity` in the MCP guide
  (`docs/guides/integration/mcp-readme.md`).

## See Also

- [device-pairing.md](device-pairing.md)
- [http-server.md](http-server.md)
