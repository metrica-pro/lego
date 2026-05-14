# Spec 001 — Tasks: lego DNS-01 provider `cloudruevolution`

Each task is one atomic commit (≤200 line diff) on
`feat/cloudru-evolution-dns-provider`. Conventional Commits prefixes.

| # | Task | Status | Commit |
|---|---|---|---|
| T0 | Branch off `main` | ✅ | (branch creation) |
| T1 | Skeleton + types | ✅ | `feat(cloudruevolution): add provider skeleton and types` |
| T2 | Identity / OAuth2 token cache + tests | ✅ | `feat(cloudruevolution): add IAM token caching with refresh` |
| T3 | HTTP client + `do()` + 401 retry + error mapping + tests | ✅ | `feat(cloudruevolution): add HTTP client with 401 retry and error mapping` |
| T4 | Zones API + domain cache + tests | ✅ | `feat(cloudruevolution): add zone lookup with cache` |
| T5 | Records API + operation waiter + tests | ✅ | `feat(cloudruevolution): add records API and async operation waiter` |
| T6 | `Present` + `CleanUp` with multi-value TXT merge | ✅ | `feat(cloudruevolution): implement Present and CleanUp with multi-value TXT merge` |
| T7 | Provider tests (env loader, config, mocked upsert) | ✅ | `test(cloudruevolution): add unit and mocked end-to-end tests` |
| T8 | TOML + `make generate` (registry, docs, dnshelp) | ✅ | `feat(cloudruevolution): register provider and regenerate docs` |
| T9 | OpenAPI vendor + SDD spec dir | ⏳ this commit | `docs(cloudruevolution): add SDD spec and vendor OpenAPI` |
| T10 | Live integration test (build tag) | ⏳ | `test(cloudruevolution): add live integration test` |
| T11 | Open PR `feat/cloudru-evolution-dns-provider` | ⏳ | (GitHub) |

## Verification checklist

- [x] `go build ./...` — succeeds
- [x] `go vet ./providers/dns/cloudruevolution/...` — clean
- [x] `go test ./providers/dns/cloudruevolution/...` — all tests pass
- [x] `lego dnshelp -c cloudruevolution` — env vars and defaults render
- [x] `go generate ./...` — registry, docs, README updated
- [ ] `golangci-lint run providers/dns/cloudruevolution/...` — clean
- [ ] Live integration test against `summasoft.ru` once Beget NS
      propagation completes (`dig +short NS summasoft.ru @8.8.8.8` lists
      `evo-cns01.cloud.ru.` / `evo-cns02.cloud.ru.`)

## Open follow-ups (not blocking PR)

1. Upstream PR to `go-acme/lego` once the fork has soaked in production for
   a release cycle.
2. Possible codegen of the internal client from `internal/openapi.yaml`
   when Cloud.ru publishes a stable spec version.
3. Tariff/quota awareness — Cloud.ru rate-limits the IAM endpoint; the
   provider currently fetches a token at most twice per provider lifetime
   (initial + one refresh) so this is not pressing.
