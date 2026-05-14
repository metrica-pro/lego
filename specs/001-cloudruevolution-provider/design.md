# Spec 001 — Design: lego DNS-01 provider `cloudruevolution`

> Status: implemented.
> Branch: `feat/cloudru-evolution-dns-provider`.
> Upstream sibling: [`providers/dns/cloudru`](../../providers/dns/cloudru/) (legacy `console.cloud.ru/api/clouddns`).

## Context

Cert-manager in the new Cloud.ru mk8s cluster must validate Let's Encrypt
certificates through DNS-01. The legacy `cloudru` provider points at the
deprecated `console.cloud.ru/api/clouddns` API and is not usable on
Cloud.ru Evolution. Production domains `metricapro.ru` and `summasoft.ru`
are delegated to Cloud.ru Evolution DNS — automation needs a lego provider
that speaks `https://dns.api.cloud.ru/v1/publicZones` and
`/v1/publicRecordsSole`.

## Architecture

- Go package: `cloudruevolution` (lowercase, one word — lego convention).
- Env namespace: `CLOUDRU_EVOLUTION_*`.
- Provider Code in TOML: `cloudruevolution` (registered via `make generate`).

```
providers/dns/cloudruevolution/
├── cloudruevolution.go              # Config, DNSProvider, Present/CleanUp, helpers
├── cloudruevolution.toml            # metadata → auto-generated docs/registry
├── cloudruevolution_helpers_test.go # relativeName, filterOut
├── cloudruevolution_test.go         # env + config + mocked upsert end-to-end
└── internal/
    ├── client.go        # HTTP client core, do(), 401 retry, error mapping
    ├── client_test.go
    ├── identity.go      # IAM token cache (sync.RWMutex, 60s prefetch)
    ├── identity_test.go
    ├── openapi.yaml     # vendored Evolution DNS OpenAPI v3 reference
    ├── records.go       # records CRUD + *AndWait helpers
    ├── records_test.go
    ├── types.go         # JSON DTOs (Token, PublicZone, PublicRecord, Operation, APIError)
    ├── waiter.go        # WaitForOperation via internal/wait.For
    ├── zones.go         # ListZones, FindZoneByDomain, cache
    └── zones_test.go
```

## API contracts (verified live 2026-05-14)

### Authentication

```
POST https://iam.api.cloud.ru/api/v1/auth/token
Content-Type: application/json
Body: {"keyId": "...", "secret": "..."}
→ {access_token, token_type:"Bearer", expires_in:3600, refresh_token, ...}
```

**Not form-encoded** — the ТЗ documented form-encoded, but the live API
rejects it with HTTP 400. The form-encoded variant works only on the
legacy OpenID endpoint `auth.iam.cloud.ru/auth/system/openid/token`, which
issues an equivalent Bearer for `aud=["iam"]`. The DNS API accepts the
JSON-flow token directly — no token exchange required.

### DNS endpoints (camelCase, gRPC-gateway shape)

| Operation | Method | Path |
|---|---|---|
| List zones | GET | `/v1/publicZones?projectId={pid}` |
| Get zone | GET | `/v1/publicZones/{publicZoneId}` |
| List records | GET | `/v1/publicRecordsSole?publicZoneId={zid}` |
| Get record | GET | `/v1/publicRecordsSole/{recordId}` |
| Create record | POST | `/v1/publicRecordsSole` |
| Update record | PATCH | `/v1/publicRecordsSole/{recordId}` |
| Delete record | DELETE | `/v1/publicRecordsSole/{recordId}` |
| Poll operation | GET | `/v1/operations/{operationId}` (poll until `done=true`) |

### Validation surface (regex from the API's own 400 responses)

- `domain` — FQDN with trailing dot.
- record `name` — **relative to the zone**, no trailing dot. Apex = `""`.
- `type` — lowercase (`txt`, `a`, `aaaa`, `cname`, ...).
- `ttl` — integer.

### Asynchronous operations

POST/PATCH/DELETE return `Operation{id, resourceId, done, error}` —
`resourceId` is the freshly created/affected record id and is usable
immediately, but the change is only durably applied after `done=true`. The
client polls every `OperationPollInterval` (default 2s) until
`OperationTimeout` (default **5min** — chosen with safety margin over the
10–60 s settle range observed in empirical testing). The waiter honours
`ctx.Done()`; on context cancellation the call returns promptly with the
last-known operation envelope. On wait timeout from a Create the partial
Operation (carrying `resourceId`) is included in the error so callers can
schedule a cleanup of a stranded record. The waiter short-circuits as soon
as `op.Error` becomes non-nil — it does not wait for `done==true` before
surfacing terminal failures.

### Error envelope (gRPC status semantics)

| HTTP | gRPC code | Meaning | Provider behaviour |
|---|---|---|---|
| 409 | 6 (AlreadyExists) | duplicate POST | `Present` swallows: idempotent success |
| 400 | 9 (FailedPrecondition) | record/zone gone | `CleanUp` swallows: success |
| 400 | 3 (InvalidArgument) | validation failed | surfaced as is |
| 4xx/5xx other | — | unexpected | wrapped in `errutils.UnexpectedStatusCodeError` |

## Multi-value TXT (wildcard scenario)

Cloud.ru stores a TXT label as **one rrset with an array of values**.
A wildcard cert (`*.example.com` + `example.com`) makes lego issue two
Present calls for the same `_acme-challenge.example.com` label — the
provider must PATCH-merge the new value into the existing rrset, not
attempt a second POST (which returns 409). On CleanUp the corresponding
value is removed; the rrset is DELETEd only when the array empties.

Cloud.ru exposes no optimistic-concurrency primitive (no ETag / If-Match)
so concurrent writers would risk losing updates on a read-modify-write
path. Two mitigations:

1. `Sequential()` asks lego to drain each DNS-01 challenge before launching
   the next on the same provider instance (default 60s — configurable via
   `CLOUDRU_EVOLUTION_SEQUENCE_INTERVAL`).
2. `upsertTXT` performs **bounded re-read+retry** on a conflict response
   from PATCH (5 attempts).

## Token caching strategy

Single `identity` per Client, guarded by `sync.RWMutex`:

- RLock fast path when token is present and within `refreshThresholdFor`
  (default 60 s, but scaled to `expires_in/2` for tokens shorter than 2min
  so a misconfigured IAM returning `expires_in=30` does not provoke a
  refresh on every call).
- Lock taken only for the brief moment of swapping the cached token; the
  IAM HTTP roundtrip itself runs without holding any lock so readers are
  never blocked behind a slow IAM.
- `invalidate()` is called from the HTTP layer on 401 → next request
  rebuilds with a fresh token; a single retry is attempted. The 401 body
  is logged at debug level (truncated to 256 bytes) so operators can see
  IAM-side reasons like "key revoked".

## Files used unchanged from lego v5 (cited at exact lines)

- `challenge/provider.go:12,28` — `Provider`, `ProviderTimeout`.
- `challenge/dns01/dns_challenge.go:175,206` — `ChallengeInfo`,
  `GetChallengeInfo`.
- `challenge/dns01/client.go:42` — `FindZoneByFqdn`.
- `platform/env/env.go:16,127,133,149` — `Get`, `GetOrDefaultInt`,
  `GetOrDefaultSecond`, `GetOrFile` (auto `_FILE` suffix).
- `internal/wait/wait.go:14` — `wait.For`.
- `internal/errutils/client.go:120` —
  `NewUnexpectedResponseStatusCodeError`.
- `internal/useragent/useragent.go:27` — `SetHeader`.
- `providers/dns/internal/clientdebug` — HTTP client wrapper for debug.

## Out of scope

- PR into upstream `go-acme/lego` (separate task after fork settles).
- `providers/dns/cloudru` legacy provider — untouched.
- Private zones (`/v1/privateZones`) — not needed for DNS-01.
- `cert-manager-lego-webhook` integration — separate component.
- Code generation from OpenAPI — current iteration writes the client by
  hand for tight control; `internal/openapi.yaml` is kept as reference.
