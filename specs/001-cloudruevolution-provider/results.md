# Spec 001 — Results

## Status: released

Released as part of **`metrica-pro/lego` v5.1.0** on 2026-05-14.

## What was delivered

- New DNS-01 provider `cloudruevolution` at `providers/dns/cloudruevolution/`.
- Targets the Cloud.ru Evolution DNS REST API (`https://dns.api.cloud.ru/v1/publicZones`,
  `/v1/publicRecordsSole`) — distinct from legacy `cloudru` provider.
- Full async-operation polling, multi-value TXT PATCH-merge for wildcard scenarios,
  IAM token caching with scaled refresh threshold.
- Docker image published to `ghcr.io/metrica-pro/lego:v5.1.0` (amd64 + arm64).

## Test results

```
go test ./providers/dns/cloudruevolution/... -v -cover
# PASS — coverage ≥ 80 % for internal/
```

Lint:
```
golangci-lint run providers/dns/cloudruevolution/...
# 0 issues
```

CLI smoke:
```
/tmp/lego dnshelp -c cloudruevolution
# Configuration for Cloud.ru Evolution DNS — OK
```

## Live integration test

Blocked on NS propagation for `summasoft.ru` (Beget → Cloud.ru Evolution order pending at
release time).

Run once propagation is confirmed:

```sh
dig +short NS summasoft.ru @8.8.8.8
# expected: evo-cns01.cloud.ru. evo-cns02.cloud.ru.

go test -tags integration ./providers/dns/cloudruevolution/... \
  -v -run TestLivePresentCleanUp
```

## Container image

| Tag | Digest |
|---|---|
| `v5.1.0` | published to ghcr.io/metrica-pro/lego |
| `5.1`, `5`, `latest` | published (same digest) |

Platforms: `linux/amd64`, `linux/arm64`.

## Commits (main)

```
feat(cloudruevolution): add provider skeleton and types
feat(cloudruevolution): add IAM token caching with refresh
feat(cloudruevolution): add HTTP client with 401 retry and error mapping
feat(cloudruevolution): add zone lookup with cache
feat(cloudruevolution): add records API and async operation waiter
feat(cloudruevolution): implement Present and CleanUp with multi-value TXT merge
test(cloudruevolution): add unit and mock e2e tests
feat(cloudruevolution): register provider and regenerate docs
docs(cloudruevolution): add SDD spec and vendor OpenAPI reference
test(cloudruevolution): add live integration test
fix(cloudruevolution): address production-readiness review findings
test(cloudruevolution): raise coverage and add smoke build tag
style(cloudruevolution): satisfy repo golangci-lint defaults
ci(release): add tag-driven GHCR image build for metrica-pro fork
```

## Open items

- Upstream PR to `go-acme/lego` — separate task, after NS live test passes.
- cert-manager integration test in Cloud.ru mk8s cluster.
- Live integration test for `summasoft.ru` — blocked on NS propagation.
