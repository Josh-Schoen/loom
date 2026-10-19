# Device Pairing

**Status:** ✅ Implemented in `looms serve`: `pkg/devices` (codes, tokens,
file store), `pkg/server/device_service.go` (`DeviceService` and its
interceptor), `cmd/looms/cmd_pair.go` (`looms pair`, `looms devices`).
📋 Planned: a PostgreSQL-backed device store; a browser page that shows the
pairing code. Configuration, CLI, and API:
[`docs/reference/device-pairing.md`](../reference/device-pairing.md).
**Relates to:** Supabase-JWT authentication (`pkg/server/auth_interceptor.go`),
SSE authentication (`docs/reference/http-server.md#sse-authentication-and-identity`).

**Target Audience**: Architects and developers working on loom clients or
server authentication.

## Table of Contents

- [Overview](#overview)
- [Design Goals](#design-goals)
- [System Context](#system-context)
- [Key Interaction](#key-interaction)
- [Components](#components)
- [Data Structures and Invariants](#data-structures-and-invariants)
- [Authentication Chain](#authentication-chain)
- [Design Trade-offs](#design-trade-offs)
- [Security Considerations](#security-considerations)
- [Concurrency Model](#concurrency-model)
- [Constraints and Limitations](#constraints-and-limitations)
- [Related Work](#related-work)

## Overview

A self-hosted loom server has had two ways to know who is calling: a trusted
`x-user-id` header (no authentication) or Supabase-issued JWTs (needs a
Supabase project). Neither suits a person who runs `looms serve` at home and
wants their phone to talk to it: the first is unauthenticated, the second
brings an external identity provider.

Device pairing gives each client device its own server-issued credential. The
operator issues a short-lived single-use code on the server machine; the
device redeems it for a long-lived bearer token. The code travels as a QR
code or link that also carries the server address, so the person setting up
the phone types nothing.

## Design Goals

- **No typing on the device.** One scan carries both where to connect and the
  right to connect.
- **Per-device credentials.** A lost phone is revoked without affecting other
  devices or the operator.
- **No external dependency.** Works with SQLite storage and no identity
  provider.
- **Additive.** With pairing off, behavior is unchanged; with pairing on and
  `required: false`, unpaired clients (CLI, TUI) keep working, and Supabase
  JWTs keep working in either mode.
- **Secrets never at rest in plain form on the server** (except the admin
  token, which must be presented by local tooling).

**Non-goals:** user accounts or login; transport encryption (the gateway
serves HTTP; encryption comes from a reverse proxy or VPN in front of it);
multi-instance deployments sharing one device list.

## System Context

```
┌──────────────────────── server machine ───────────────────────┐
│                                                                │
│  [operator] ── looms pair ──▶ ┌──────────────────────────┐     │
│        ▲      (admin token,   │       looms serve         │     │
│        │       gRPC :60051)   │  ┌─────────────────────┐  │     │
│        │                      │  │   DeviceService     │  │     │
│   QR code + link ◀────────────│  │  codes (memory)     │  │     │
│                               │  │  devices (file)     │  │     │
│                               │  └─────────────────────┘  │     │
│                               │  HTTP gateway :5006       │     │
│                               └────────────▲─────────────┘     │
└────────────────────────────────────────────┼───────────────────┘
                                             │ /v1/pairing:redeem,
                                             │ then every request with
                                             │ Authorization: Bearer loomd_…
                                    [phone / desktop app]
```

## Key Interaction

```
Operator CLI          DeviceService             Device
     │                      │                      │
     ├─ CreatePairingCode ─▶│                      │
     │   (admin token)      │ store hash(code),    │
     │                      │ expiry, user         │
     │◀─ code, link ────────┤                      │
     │                      │                      │
     ├─ show QR ─ - - - - - - - - - - - - - - - - ▶│ scan
     │                      │                      │
     │                      │◀─ RedeemPairingCode ─┤ (code only)
     │                      │ delete hash(code)    │
     │                      │ create device,       │
     │                      │ persist hash(token)  │
     │                      ├─ device token ──────▶│ keep in secure storage
     │                      │                      │
     │                      │◀─ any RPC, Bearer ───┤
     │                      │ hash → device → user │
```

Redeem and the single-use check happen under one mutex, so of two concurrent
redeems of the same code exactly one succeeds.

## Components

### Code and token manager (`pkg/devices`)

**Responsibility**: issue codes, redeem them, authenticate tokens, revoke
devices, persist devices.

**Interface**: `CreateCode`, `Redeem`, `Authenticate`, `List`, `Revoke`;
`LoadOrCreateAdminToken` for the admin credential.

**Implementation**: codes are 160 random bits, base32 (no padding, so they
survive case changes and URL encoding); tokens are 256 random bits,
base64url, with a `loomd_` prefix. Only SHA-256 hashes are kept. Devices are
written to a JSON file atomically (temp file, `chmod 0600`, rename).
`LastSeenAt` is persisted at most once a minute per device.

### Device service and interceptor (`pkg/server/device_service.go`)

**Responsibility**: the gRPC service, and the first authenticator in the
interceptor chain.

**Implementation**: one function decides, by method name and bearer prefix,
whether a request is the redeem call, an admin call, a device-token call, or
something for the next authenticator. It marks requests it has verified, so
later authenticators do not re-validate the same bearer as a JWT.

A device token is accepted only for an allowlist of RPCs a phone needs: the
conversation (Weave, StreamWeave, sessions, history, client tool results,
clarification answers), the caller's own UI apps and user memories, and
reading agents and health. Every other RPC, on every service, needs the admin
token: managing agents, MCP servers, tools, storage and skills changes the
server for all users or starts programs on the host, so a household member's
phone must not reach it.

### CLI (`cmd/looms/cmd_pair.go`)

**Responsibility**: issue codes and manage devices from the server machine.

**Implementation**: reads the admin token from the data directory, so only a
user who can read the server's files can pair devices. Draws the QR code with
explicit ANSI black and white so the code has the correct polarity on light
and dark terminal themes.

## Data Structures and Invariants

```
pending code (memory)              device (devices.json)
{ hash(code) → user_id,            { id, name, platform, user_id,
               expires_at }          token_hash, created_at,
                                     last_seen_at, revoked_at? }
```

- **I1, single use**: a code hash is deleted before its device is created; a
  code is never redeemed twice.
- **I2, bounded lifetime**: a code is accepted only while
  `now < expires_at`, and `expires_at − created ≤ code_ttl_seconds ≤ 3600 s`.
- **I3, no plaintext secrets**: neither codes nor device tokens are stored;
  only their SHA-256 hashes.
- **I4, revocation is final**: `revoked_at` is never cleared; a revoked
  token never authenticates again.

Lookup is O(1) by token hash. Expired codes are pruned when new codes are
issued.

## Authentication Chain

```
request ─▶ [device pairing] ─▶ [Supabase JWT] ─▶ [legacy x-user-id] ─▶ handler
              │                      │                   │
              │ redeem: pass, marked │ marked: skip      │ user set or marked:
              │ admin RPC: admin tok │ else validate JWT │ skip
              │ loomd_…: set user,   │                   │ else header/default
              │   allowlisted RPCs   │                   │
              │ admin token: pass    │                   │
              │ other bearer: reject │                   │
              │   if required, unless│                   │
              │   JWT auth follows   │                   │
              │ none: reject if      │                   │
              │       required       │                   │
```

The same chain runs for gRPC and for the in-process SSE endpoint
(`/v1/weave:stream`), so a device token works for streaming turns too.

When pairing is required and Supabase-JWT authentication is off, nothing
after the device authenticator verifies a bearer, and the legacy interceptor
takes the identity from the `x-user-id` header. A bearer that is neither a
device token nor the admin token is therefore rejected in that case; passing
it on would let any caller choose whose data it acts on.

## Design Trade-offs

### Decision 1: Server-issued tokens instead of an identity provider

**Chosen**: random bearer tokens issued and checked by the server.

**Alternatives considered**:
- *Supabase JWT only*: already supported, but needs an external project and
  an account per person; too much for a single household server.
- *Signed JWTs issued by loom*: stateless verification, but revocation then
  needs a deny list anyway; with a per-server device list, opaque tokens are
  simpler and revocable by construction.
- *Mutual TLS client certificates*: strong, but certificate provisioning on
  phones is not something a non-technical person can do.

### Decision 2: Single-use code exchanged for a token, not the token in the QR

**Chosen**: the QR carries a short-lived code; the long-lived token is issued
on redeem.

**Rationale**: QR codes get photographed, screenshotted, and left on screens.
A code that expires in minutes and works once limits that exposure; the
long-lived token only ever travels from server to device over the redeem
response.

### Decision 3: Admin token file instead of loopback trust

**Chosen**: admin RPCs require a token stored in the data directory with
mode 0600.

**Alternative rejected**: trusting callers from 127.0.0.1. The HTTP gateway
forwards every request to gRPC over a loopback connection, so all gateway
traffic, including traffic from the network, appears to come from loopback.
File permissions are a boundary the operating system enforces.

### Decision 4: JSON file store

**Chosen**: a JSON file in the data directory.

**Rationale**: pairing targets single-machine, self-hosted servers, including
SQLite deployments. The file keeps the feature independent of the storage
backend. Cost: no sharing across server instances (see Constraints).

### Decision 5: `required` defaults to false

**Rationale**: turning pairing on must not lock out existing clients that send
no credential. Operators who expose the server beyond a trusted network set
`required: true` once every client is paired.

## Security Considerations

**Threat model**: an attacker on the same network as the server, or one who
sees a pairing QR code, or one who obtains a phone.

| Threat | Mitigation |
|--------|------------|
| Guessing a pairing code | 160-bit codes; online guessing is infeasible within the TTL |
| Photographed QR code | Single use, expires in ≤ 1 h (default 10 min); a redeemed code is useless |
| Stolen or lost device | `looms devices revoke`; other devices unaffected |
| Server file disclosure | Device tokens stored only as SHA-256 hashes; codes not stored |
| Network eavesdropping | **Not mitigated by loom**: the gateway is plain HTTP. Tokens on a LAN are readable by others on it. The server logs a warning unless `public_url` is `https://`; use an HTTPS reverse proxy or a VPN |
| Timing attacks on the admin token | Constant-time comparison |
| A paired phone (or its owner) calling admin RPCs | Device tokens are limited to an allowlist of RPCs; the rest need the admin token |
| An unverified bearer with an `x-user-id` header | Rejected when pairing is required, unless a JWT authenticator follows |
| Oversized request bodies before authentication | HTTP bodies are capped at 4 MiB (gRPC's default receive limit) |
| Unpaired callers when `required: false` | They follow the existing (unauthenticated) legacy path, unchanged from before pairing |

**Trust boundaries**: the data directory (admin token, device store) and the
TLS boundary in front of the gateway, when present.

## Concurrency Model

All `Manager` state (pending codes, devices, hash index) is guarded by one
`sync.Mutex`; persistence happens inside the lock, so file contents always
match memory order. Hold times are short (a hash, a map lookup, and at most
one small file write). Tests run with `-race`, including concurrent redeems of
one code.

## Constraints and Limitations

- **Single process**: the device store is a file; multiple `looms serve`
  instances would each have their own device list.
- **Codes do not survive restarts**: unredeemed codes are in memory.
- **Revocation and open streams**: revoking does not close a stream that is
  already open; the device's next request fails.
- **Self-reported metadata**: device name and platform come from the device.
- **Address detection**: with no `public_url`, the link uses the first
  private IPv4 address found; on machines with several networks that may not
  be the one the phone is on.

## Related Work

- **OAuth 2.0 Device Authorization Grant (RFC 8628)**: also separates a
  short-lived user-visible code from the long-lived token. Pairing inverts the
  roles (the server shows the code; the device presents it) because the device
  has a camera and the server has no browser session to approve in.
- **Home Assistant and Plex companion apps**: pair phones with self-hosted
  servers by scanning a code shown on the server side.
- **Signal linked devices**: link a new device by scanning a QR code that
  carries a one-time provisioning secret.
