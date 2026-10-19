# Device Pairing Reference

**Status**: ✅ Implemented in `looms serve` (file-backed device store, single
server process). 📋 Planned: PostgreSQL-backed store, browser page that shows
the pairing QR code.

Pair a phone or desktop app with a self-hosted server by QR code or link. The
device gets its own revocable token; nobody types an address or secret.
Design and trade-offs: [`docs/architecture/device-pairing.md`](../architecture/device-pairing.md).

## Table of Contents

- [Quick Start](#quick-start)
- [Configuration](#configuration)
- [CLI](#cli)
- [API](#api)
- [Pairing Link Format](#pairing-link-format)
- [Errors](#errors)
- [Files](#files)
- [Limitations](#limitations)

## Quick Start

```yaml
# looms.yaml
server:
  http_port: 5006
  device_pairing:
    enabled: true
```

```bash
looms serve --config looms.yaml     # terminal 1
looms pair                          # terminal 2, same user and LOOM_DATA_DIR
```

`looms pair` prints a QR code and a link. Scan the code with the phone (or
open the link on it) and the app connects.

## Configuration

All keys are under `server.device_pairing`.

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `enabled` | bool | `false` | Register `DeviceService` and accept device tokens. |
| `required` | bool | `false` | Reject requests that carry no credential (`UNAUTHENTICATED`). Enables session ownership enforcement. Keep `false` while unpaired clients (CLI, TUI) connect. |
| `public_url` | string | `""` | Gateway URL put in pairing links. Empty: `http://<first private IPv4>:<http_port>` (`https://` with `pinned_tls`), usable only on the same network. Must be `http` or `https` with a host; `https` when `pinned_tls` is on. |
| `pinned_tls` | bool | `false` | Serve the HTTP gateway over HTTPS with a self-signed certificate made once and kept in the data directory, and put its pin in pairing links (see [Pinned TLS](#pinned-tls)). |
| `link_base` | string | empty | An `https` URL (host, optional path; no query or fragment) to build pairing links on instead of `link_scheme`, e.g. `https://setup.example.com/pair`. Use it with a client that claims that domain (iOS associated domains), so one QR code opens the app if installed and a download page if not. |
| `link_scheme` | string | `loompair` | URL scheme of pairing links (RFC 3986 scheme syntax). Must match the scheme the client app registers. The default avoids `loom`, which other apps may already claim on a phone. |
| `code_ttl_seconds` | int | `600` | Code lifetime and the maximum a request may ask for. Range 60–3600. |

Validation (`looms serve` refuses to start): TTL outside 60–3600, invalid
scheme, malformed `public_url`, `http_port: 0` with no `public_url`, or
`pinned_tls` with an `http://` `public_url`.

Startup warning: when neither `pinned_tls` is on nor `public_url` is
`https://`, the server logs that device tokens travel unencrypted.

## Pinned TLS

With `pinned_tls: true` the HTTP gateway (REST, SSE, `/apps/`) serves only
HTTPS. The certificate is ECDSA P-256, signed with SHA-256, valid for 20
years, made at the first start and kept in `gateway-tls.crt` and
`gateway-tls.key`. It lists `localhost`, `127.0.0.1`, `::1`, the host name
(and `<name>.local`) and the machine's addresses at that time; pinning
clients ignore these.

Every pairing link then carries `pin`, the SHA-256 of the certificate's DER
bytes as unpadded base64url (43 characters), and `CreatePairingCodeResponse`
returns it as `cert_pin`. A client trusts exactly the certificate with that
pin for the link's server, with no certificate authority and no host name
check:

- Traffic between paired devices and the server is encrypted on any
  network, and a machine on the same network cannot pose as the server.
- A `public_url` must reach the gateway's TLS unchanged, for example
  Tailscale forwarding TCP (`tailscale serve --tcp=443
  tcp://127.0.0.1:<http_port>`). A proxy that terminates TLS with another
  certificate breaks the pin.
- Deleting either file makes a new certificate at the next start; every
  device must then be paired again. If only one of the two files exists,
  the server refuses to start.
- gRPC (`port`) is unchanged; `looms pair` and other local tools use it.

Local clients that do not pin can trust `gateway-tls.crt` as a root and
connect to `https://127.0.0.1:<http_port>`.

## CLI

All commands read `$LOOM_DATA_DIR/device-admin.token` and connect to the gRPC
port in plaintext.

| Command | Description |
|---------|-------------|
| `looms pair` | Issue a code; print a QR code, the link, the server URL, and the expiry. |
| `looms pair --link-only` | Print only the link. |
| `looms pair --ttl 5m` | Shorter lifetime (capped at `code_ttl_seconds`). |
| `looms pair --user alice` | Device acts as `alice` (default: the server's default user). Pages the assistant makes for `alice` are hers alone; see `mcp-apps.md#ownership`. |
| `looms devices list` | Paired devices with status (`active` / `revoked`). |
| `looms devices list --json` | The same as JSON: `loom.v1.ListDevicesResponse` in protojson (lowerCamelCase, every field present; `revokedAt` is `null` for active devices), for scripts and setup apps. |
| `looms devices revoke <device-id>` | Disable a device token permanently. |

Common flag: `--server` (default `localhost:60051`).

## API

`proto/loom/v1/devices.proto`, service `loom.v1.DeviceService`.

| RPC | HTTP route | Credential |
|-----|------------|------------|
| `CreatePairingCode` | `POST /loom.v1.DeviceService/CreatePairingCode` | admin token |
| `RedeemPairingCode` | `POST /v1/pairing:redeem` | none (the code) |
| `ListDevices` | `POST /loom.v1.DeviceService/ListDevices` | admin token |
| `RevokeDevice` | `POST /loom.v1.DeviceService/RevokeDevice` | admin token |

Credentials go in `Authorization: Bearer <token>` (HTTP) or `authorization`
metadata (gRPC).

### RedeemPairingCode

Request:

| Field | Type | Description |
|-------|------|-------------|
| `code` | string | Code from the pairing link (case-insensitive). Required. |
| `device_name` | string | Shown in `ListDevices`; truncated to 100 characters. |
| `platform` | string | e.g. `ios`; truncated to 32 characters. |

Response: `device_id`, `device_token` (returned only here), `user_id`.

```bash
curl -X POST http://192.168.1.20:5006/v1/pairing:redeem \
  -H 'Content-Type: application/json' \
  -d '{"code":"DSE5KEC3ZQZD2SEQSAKQ5QF5GCL4BRVY","device_name":"Phone","platform":"ios"}'
# {"deviceId":"dev_c4f44b6ed2ae5c23","deviceToken":"loomd_…","userId":"default-user"}
```

After pairing, send the token on every request, including SSE:

```bash
curl -N -X POST http://192.168.1.20:5006/v1/weave:stream \
  -H "Authorization: Bearer $DEVICE_TOKEN" -d '{"query":"hello"}'
```

### CreatePairingCode

Request: `user_id` (optional), `ttl_seconds` (optional; `0` = default,
negative = `INVALID_ARGUMENT`). Response: `code`, `expires_at`, `server_url`,
`pairing_link`.

## Pairing Link Format

```
<link_scheme>://pair?code=<code>&server=<url-encoded server_url>
```

Example: `loompair://pair?code=DSE5KEC3ZQZD2SEQSAKQ5QF5GCL4BRVY&server=http%3A%2F%2F192.168.1.20%3A5006`

With `pinned_tls`, `&pin=<cert_pin>` is added and the server URL is `https`:
`loompair://pair?code=DSE5KEC3ZQZD2SEQSAKQ5QF5GCL4BRVY&pin=47DEQpj8HBSa-_TImW-5JCeuQeRkm5NMpJWZG3hSuFU&server=https%3A%2F%2F192.168.1.20%3A5006`

With `link_base` set, the same values go in the fragment:

```
<link_base>#code=<code>&server=<url-encoded server_url>
```

Example: `https://setup.example.com/pair#code=DSE5KEC3ZQZD2SEQSAKQ5QF5GCL4BRVY&server=http%3A%2F%2F192.168.1.20%3A5006`

Browsers do not send the fragment to the web host, so a page at `link_base`
never receives the code; a page that wants to hand the link to the app reads
it from `location.hash` in the browser.

Clients should accept only `http`/`https` server URLs and treat the code as a
secret until redeemed. A client given a `pin` should send the code only to a
server presenting the certificate with that pin, and refuse links whose pin
is malformed or whose server is not `https`.

## Errors

| Situation | Code | HTTP |
|-----------|------|------|
| Unknown, used, or expired pairing code | `UNAUTHENTICATED` | 401 |
| Missing `code` | `INVALID_ARGUMENT` | 400 |
| Admin RPC without the admin token | `PERMISSION_DENIED` | 403 |
| Unknown or revoked device token | `UNAUTHENTICATED` | 401 |
| No credential with `required: true` | `UNAUTHENTICATED` | 401 |
| `RevokeDevice` with an unknown id | `NOT_FOUND` | 404 |
| No address for the link (no private IPv4 and no `public_url`) | `FAILED_PRECONDITION` | 400 |
| Pairing disabled | `UNIMPLEMENTED` | 501 |

## Files

| File | Mode | Contents |
|------|------|----------|
| `$LOOM_DATA_DIR/device-admin.token` | 0600 | Admin token (created at first start with pairing enabled). |
| `$LOOM_DATA_DIR/devices.json` | 0600 | Paired devices with SHA-256 token hashes. |
| `$LOOM_DATA_DIR/gateway-tls.crt` | 0600 | Pinned TLS certificate (PEM), with `pinned_tls`. |
| `$LOOM_DATA_DIR/gateway-tls.key` | 0600 | Its private key (PEM), with `pinned_tls`. |

Deleting `device-admin.token` and restarting creates a new admin token.
Deleting `devices.json` unpairs every device.

## Limitations

- One server process per device store (JSON file).
- Unredeemed codes do not survive a restart.
- Revocation does not close a stream that is already open.
- Without `pinned_tls` the gateway serves plain HTTP; use `pinned_tls`, an
  HTTPS reverse proxy, or a VPN.
- With `pinned_tls`, replacing the certificate means pairing every device
  again; there is no rotation that keeps existing pins.
