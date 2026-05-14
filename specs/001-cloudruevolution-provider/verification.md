# Spec 001 — Verification

## Unit tests

```bash
cd /Users/imac/app/lego
go test ./providers/dns/cloudruevolution/... -v -cover
```

Expected: all tests pass. Coverage focus on `internal/` (token cache,
HTTP client, records CRUD, operation waiter, zone cache).

## Lint

```bash
golangci-lint run providers/dns/cloudruevolution/...
```

Expected: zero findings under the repo-level `.golangci.yml`
(`default: all` minus the standard disable list).

## Registry / docs regeneration

```bash
go generate ./...
git diff --stat providers/dns/zz_gen_dns_providers.go cmd/zz_gen_cmd_dnshelp.go docs/content/dns/zz_gen_cloudruevolution.md README.md
```

Expected: `case "cloudruevolution":` present in
`zz_gen_dns_providers.go`; README provider list contains
`cloudruevolution`.

## CLI smoke test

```bash
go build -o /tmp/lego .
/tmp/lego dnshelp -c cloudruevolution
```

Expected output (abridged):

```
Configuration for Cloud.ru Evolution DNS.
Code:   'cloudruevolution'
Since:  'v5.x'

Credentials:
  - "CLOUDRU_EVOLUTION_KEY_ID":      IAM API key ID
  - "CLOUDRU_EVOLUTION_PROJECT_ID":  Cloud.ru project UUID
  - "CLOUDRU_EVOLUTION_SECRET":      IAM API key secret
...
```

## Live integration test (`//go:build integration`)

Prerequisites:

1. NS propagation for `summasoft.ru` to Cloud.ru is complete:

   ```bash
   dig +short NS summasoft.ru @8.8.8.8
   # expected:
   # evo-cns01.cloud.ru.
   # evo-cns02.cloud.ru.
   ```

2. Credentials supplied via env (loaded from Infisical):

   ```bash
   eval $(grep INFISICAL_DEV /Users/imac/app/infrastructure/.env | sed 's/^/export /')
   TOKEN=$(infisical login --method=universal-auth \
     --client-id="$INFISICAL_DEV_AGENT_CLIENT_ID" \
     --client-secret="$INFISICAL_DEV_AGENT_CLIENT_SECRET" \
     --domain="https://secrets.app.metrica.pro" --plain)

   export CLOUDRU_EVOLUTION_KEY_ID=$(infisical secrets get CLOUDRU_KEY_ID \
     --projectId="015e2dec-dcd9-4653-93ad-ec7b64dc7b04" --env=prod \
     --path="/cloudru" --domain="https://secrets.app.metrica.pro" \
     --token="$TOKEN" --plain)
   export CLOUDRU_EVOLUTION_SECRET=$(infisical secrets get CLOUDRU_KEY_SECRET \
     --projectId="015e2dec-dcd9-4653-93ad-ec7b64dc7b04" --env=prod \
     --path="/cloudru" --domain="https://secrets.app.metrica.pro" \
     --token="$TOKEN" --plain)
   export CLOUDRU_EVOLUTION_PROJECT_ID=ac23918f-303d-4beb-b440-55d42e81a0be
   export CLOUDRU_EVOLUTION_DOMAIN=summasoft.ru
   ```

3. Run the live test:

   ```bash
   go test -tags integration ./providers/dns/cloudruevolution/... \
     -v -run TestLivePresentCleanUp
   ```

Expected: TXT record created, observed via public resolver
(`dns01.PreCheckFn` against `8.8.8.8`), then cleaned up; final assertion
that the rrset no longer exists.

If NS propagation has not completed the test logs a clear skip message.

## Optional: end-to-end lego CLI against Let's Encrypt staging

```bash
./lego --email test@metrica.pro --dns cloudruevolution \
       --server https://acme-staging-v02.api.letsencrypt.org/directory \
       -d cert-test.summasoft.ru run
```

Expected: staging certificate issued; TXT record visible during challenge,
removed afterwards.
