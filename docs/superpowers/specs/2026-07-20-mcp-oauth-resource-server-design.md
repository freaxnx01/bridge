# MCP OAuth Resource Server — Design

**Issue:** [#205](https://github.com/freaxnx01/bridge/issues/205)
**Date:** 2026-07-20
**Status:** Approved

## Problem

`bridge mcp serve` authenticates with a single static bearer token
(`internal/mcp/auth.go:StaticBearerVerifier`). That works for Claude Code CLI and
for Claude Desktop via the `mcp-remote` stdio bridge, because both run locally and
can send a custom `Authorization` header.

It does not work for Claude **custom connectors** (claude.ai web, the Connectors
UI, mobile). Connectors are dialled from Anthropic's servers, so the endpoint must
be publicly reachable, and the connector UI cannot send a static bearer token —
only OAuth. A connector can therefore only reach this server today with
`--no-auth`, i.e. an unauthenticated internet-facing endpoint whose `read_file`
tool reads private repos using the operator's tokens. `validateNoAuthHost`
(`cmd/bridge/mcp.go:90`) already refuses that combination by design.

OAuth is the prerequisite for reaching this server from any non-local Claude client.

## Research finding that shaped the design

The issue originally proposed using the homelab **authentik** instance as the
authorization server. **That does not work.** Verified empirically against the live
discovery documents for the `flowhub`, `vikunja`, `vaultwarden`, and
`paperless-ngx` providers:

| Requirement (Claude) | authentik advertises |
|---|---|
| RFC 7591 `registration_endpoint` | **absent** |
| Public clients (`token_endpoint_auth_method: none`) | only `client_secret_post`, `client_secret_basic` |
| PKCE `S256` | supported |

Claude performs Dynamic Client Registration and registers as a public client. It
assumes it is not pre-registered. authentik supports neither, on any provider — it
is an instance-wide limitation, not per-provider configuration.

Bridge therefore acts as the authorization server itself, and delegates only the
**human login** to authentik.

## Decisions

| Decision | Choice | Rationale |
|---|---|---|
| Authorization server | Bridge itself | authentik cannot do DCR or public clients |
| Human login | Delegated to authentik via OIDC | Reuses the existing account and MFA; no second credential to manage |
| Users | Exactly one | No user store, roles, or per-user scoping needed |
| Token format | **Opaque random tokens**, stored hashed | Bridge is sole issuer *and* verifier, so JWTs buy nothing. Removes signing keys, algorithm confusion, and all crypto verification code. Makes revocation trivial |
| OAuth library | None — hand-rolled flow on SDK wire types | Repo guardrail: keep the dependency tree small. Approach A deletes the risky (crypto) part; what remains is flow logic that tests exhaustively |
| ID token validation | Skipped; `userinfo` used instead | Bridge receives authentik's tokens directly from the token endpoint over TLS, so OIDC Core §3.1.3.7 does not require signature validation. Keeps the design free of any JWT dependency |
| State | File-backed JSON | Survives restarts, so a deploy doesn't force re-authorisation. No storage dependency |
| Scope-gating writes | **Dropped (YAGNI)** | With one user who always receives every scope, it adds branching without removing capability from any real actor. `--read-only` is the genuine kill switch |

## Architecture

New package `internal/oauth`, owning the authorization-server role, the authentik
OIDC-client leg, and the token store. `internal/mcp` is unchanged except that
`cmd/bridge/mcp.go` selects which `auth.TokenVerifier` to use.

Bridge plays three roles at once:

| Role | Toward | Responsibility |
|---|---|---|
| Authorization Server | Claude | DCR, `/authorize`, `/token`, PKCE |
| OIDC Client (confidential) | authentik | The human login |
| Resource Server | Claude | Validates its own opaque tokens |

### Components

1. **`oauth.Store`** — file-backed JSON, mutex-guarded, atomic write (temp file +
   rename in the same directory), expired records pruned on write.
2. **`oauth.Server`** — the HTTP handlers.
3. **`oauth.Verifier()`** — returns an `auth.TokenVerifier`, mirroring
   `StaticBearerVerifier`'s shape so `buildMCPHandler` needs no structural change.
4. **Protected-resource metadata** — the SDK's `auth.ProtectedResourceMetadataHandler`.

Reused from the SDK rather than reimplemented: `oauthex.ClientRegistrationMetadata`,
`oauthex.ClientRegistrationResponse`, `oauthex.AuthServerMeta`,
`oauthex.GetAuthServerMeta` (for discovering authentik), and
`auth.RequireBearerTokenOptions{ResourceMetadataURL}`. `Scopes` is left empty,
consistent with dropping scope-gating.

### Request routing

`buildMCPHandler` currently returns the Streamable HTTP handler directly, mounted at
the root. OAuth adds sibling paths, so in `--auth=oauth` mode the handler becomes an
`http.ServeMux`:

| Pattern | Handler |
|---|---|
| `/.well-known/oauth-protected-resource` | SDK metadata handler (unauthenticated) |
| `/.well-known/oauth-authorization-server` | AS metadata (unauthenticated) |
| `/oauth/` | `oauth.Server` — register, authorize, callback, token (unauthenticated by spec) |
| `/` | Bearer-guarded MCP transport, as today |

The OAuth endpoints must sit **outside** the bearer middleware — a client cannot
present a token it does not yet have. Only `/` is wrapped. In `--auth=static` mode
no mux is introduced and the handler is byte-for-byte what it is today.

## Endpoints

| Endpoint | Purpose |
|---|---|
| `GET /.well-known/oauth-protected-resource` | RFC 9728 — names bridge as the AS for this resource |
| `GET /.well-known/oauth-authorization-server` | RFC 8414 — advertises `registration_endpoint`, `code_challenge_methods_supported: ["S256"]`, `token_endpoint_auth_methods_supported: ["none"]` |
| `POST /oauth/register` | DCR; mints a `client_id`, no secret |
| `GET /oauth/authorize` | Validates client and `redirect_uri`, redirects to authentik |
| `GET /oauth/callback` | authentik returns here; exchange code, fetch `userinfo`, check `sub` |
| `POST /oauth/token` | `authorization_code` (with PKCE verifier) and `refresh_token` grants |

## Flow

1. Claude calls the MCP endpoint → **401** with `WWW-Authenticate` carrying the
   resource-metadata URL.
2. Claude fetches resource metadata → discovers bridge is the AS → fetches AS metadata.
3. Claude self-registers via DCR → receives a `client_id`.
4. Claude opens the browser at `/oauth/authorize` with an S256 `code_challenge`.
5. Bridge redirects to authentik; the user logs in with their existing account and MFA.
6. authentik redirects to `/oauth/callback`; bridge exchanges the code server-side,
   calls `userinfo`, and rejects any `sub` other than the configured user.
7. Bridge redirects to Claude's `redirect_uri` with its own single-use code.
8. Claude POSTs `/oauth/token` with the PKCE verifier → opaque access + refresh token.
9. MCP calls carry `Bearer <opaque>`; the verifier hashes and looks it up, returning
   `TokenInfo{UserID: sub, Expiration}`.

## Security invariants

These are requirements, each with a corresponding test:

- `redirect_uri` is matched **exactly** against the registered value. No prefix,
  wildcard, or normalisation matching. This is the most-exploited OAuth flaw.
- Authorization codes are single-use, expire after 60 seconds, and are bound to
  `client_id`, `redirect_uri`, and `code_challenge`.
- PKCE `S256` is required. `plain` is rejected even though authentik offers it.
- Access and refresh tokens are stored **SHA-256 hashed**. A leaked state file
  yields nothing usable.
- Refresh tokens rotate; replaying a consumed refresh token revokes the entire
  chain and is logged loudly, because it indicates theft.
- `TokenInfo.UserID` carries the authentik `sub`, which the SDK transport uses to
  bind sessions and prevent hijacking.
- Tokens, codes, verifiers, and the authentik client secret are never logged at any
  level. Auth decisions log `sub`, `client_id`, and outcome only.

## State

Path: `${BRIDGE_MCP_STATE_DIR:-~/.local/state/bridge-mcp}/oauth.json`, directory
`0700`, file `0600`.

```json
{
  "clients": { "<client_id>":     { "redirect_uris": ["..."], "client_name": "...", "created_at": "..." } },
  "codes":   { "<sha256(code)>":  { "client_id": "...", "redirect_uri": "...", "code_challenge": "...", "sub": "...", "expires_at": "..." } },
  "tokens":  { "<sha256(token)>": { "kind": "access|refresh", "client_id": "...", "sub": "...", "expires_at": "...", "chain_id": "...", "consumed": false } }
}
```

Access tokens live 1 hour; refresh tokens 30 days.

## Configuration

Environment variables, read from the existing `~/.config/bridge-mcp/env`. The
`--auth` flag selects the mode and defaults to `static`.

| Variable | Meaning |
|---|---|
| `--auth=static\|oauth` | Auth mode. Default `static` — existing behaviour is unchanged |
| `BRIDGE_MCP_ISSUER` | Bridge's public URL; used for metadata and callback construction |
| `BRIDGE_OIDC_ISSUER` | authentik provider issuer URL |
| `BRIDGE_OIDC_CLIENT_ID` | Bridge's confidential client in authentik |
| `BRIDGE_OIDC_CLIENT_SECRET` | Its secret |
| `BRIDGE_OIDC_ALLOWED_SUB` | The single authentik subject permitted to log in |
| `BRIDGE_MCP_STATE_DIR` | State directory override |

With `--auth=oauth`, startup fails fast listing every missing value, mirroring how
`buildMCPHandler` already errors on an absent token. The issuer must be `https`
except on loopback.

## Failure modes

| Scenario | Behaviour |
|---|---|
| authentik unavailable | New logins fail; existing tokens keep working, since the verifier only reads local state |
| State file corrupt | Refuse to start. Continuing with empty state would silently drop every session and accept fresh registrations |
| State file deleted | Everything invalid; Claude re-registers and the user re-logs in. Recoverable without manual repair |
| Clock skew | Not applicable — bridge is sole issuer and verifier, so no cross-party time comparison occurs |
| Two instances running | `flock` on the state directory; the second instance exits with a clear error rather than clobbering state |
| Refresh token replay | Chain revoked, re-login required, logged loudly |
| authentik `sub` changes | Login rejected with a log line naming the mismatch, so the cause is obvious rather than an unexplained 403 |

### DCR abuse

The registration endpoint is, per spec, unauthenticated and internet-facing.
Anyone may POST to it. Registration alone grants no access — it is worthless
without a successful authentik login — but it is an unbounded write to the state
file, i.e. a disk-fill denial of service.

Mitigations: cap registered clients at 100, evict the oldest unused registration on
overflow, TTL-expire registrations never used to complete a flow after 24 hours,
and keep Traefik's rate limit in front.

## Testing

Stdlib `testing`, table-driven, hand-rolled fakes, green under `-race`. No new test
dependencies.

A **fake authentik** `httptest.Server` implements discovery, token, and userinfo,
letting the whole flow run in-process without network access.

Highest-value tables:

- **`redirect_uri` exact match** — near-misses that must all be rejected: prefix
  extension, extra path segment, trailing-slash difference, scheme downgrade to
  `http`, different port, added query string, embedded userinfo, host case variation.
- **PKCE** — correct verifier accepts; wrong verifier rejects; verifier from a
  different flow rejects; `plain` rejected; missing `code_challenge` rejected.

Further cases: code single-use and expiry; `client_id`/`redirect_uri` mismatch at
token time; refresh rotation and reuse-revokes-chain; unknown, expired, and revoked
access tokens at the verifier; `sub` mismatch at callback; unknown or replayed state.

Store: file mode `0600` and directory `0700` asserted; atomic write survives a
simulated mid-write failure; corrupt JSON produces a startup error rather than empty
state; pruning removes only expired records; concurrent access clean under `-race`;
a second instance is blocked by `flock`.

Metadata: AS metadata advertises `S256` only and
`token_endpoint_auth_methods_supported: ["none"]` — the properties that make
Claude's DCR work at all.

Integration: one end-to-end test walking 401 → resource metadata → AS metadata →
DCR → authorize → fake-authentik login → callback → token exchange → authenticated
`tools/list`.

Regression: `--auth=static` behaviour is unchanged and the existing
`StaticBearerVerifier` tests keep passing untouched.

## Out of scope

- **Deployment.** Activating this requires dropping `internal-secured` from the
  dispatcher route so Anthropic's servers can reach the endpoint, and adding a
  Cloudflare DNS record. The rate limit stays. That is a separate, deliberate step.
- **Multi-user support**, roles, and per-user token scoping.
- **Fixing [#204](https://github.com/freaxnx01/bridge/issues/204)** (Forgejo client
  falls back to codeberg.org and leaks the token). It is a prerequisite for exposing
  this server to the internet, but a separate change.

## Acceptance criteria

- [ ] `--auth=oauth` rejects unauthenticated requests with a 401 carrying a
      `WWW-Authenticate` header pointing at the resource-metadata URL.
- [ ] `/.well-known/oauth-protected-resource` and
      `/.well-known/oauth-authorization-server` return valid metadata, the latter
      advertising DCR, `S256`, and public clients.
- [ ] A client can complete DCR, the authorization-code flow with PKCE, and receive
      a working access token.
- [ ] Only `BRIDGE_OIDC_ALLOWED_SUB` can complete a login; any other `sub` is rejected.
- [ ] Every security invariant above has a passing test, including the full
      `redirect_uri` and PKCE rejection tables.
- [ ] Refresh rotation works and reuse revokes the chain.
- [ ] `--auth=static` remains the default and is unchanged.
- [ ] A real Claude connector completes the flow end-to-end against a publicly
      reachable instance — verified, not assumed.
