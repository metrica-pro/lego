# cloudruevolution — Cloud.ru Evolution DNS provider

DNS-01 challenge provider for lego that uses the Cloud.ru Evolution DNS public-zone API
(`https://dns.api.cloud.ru/v1/publicZones`).

## Prerequisites

1. A Cloud.ru project with Evolution DNS enabled.
2. An IAM API key with permission to manage DNS records in that project.
3. Your domain's NS records pointing to `evo-cns01.cloud.ru.` / `evo-cns02.cloud.ru.`.

## Credentials

| Variable | Required | Description |
|---|---|---|
| `CLOUDRU_EVOLUTION_KEY_ID` | yes | IAM API key ID |
| `CLOUDRU_EVOLUTION_SECRET` | yes | IAM API key secret |
| `CLOUDRU_EVOLUTION_PROJECT_ID` | yes | Cloud.ru project UUID |

All variables also support a `_FILE` suffix — the value will be read from the named file:

```sh
CLOUDRU_EVOLUTION_SECRET_FILE=/run/secrets/cloudru-secret
```

## Optional tuning

| Variable | Default | Description |
|---|---|---|
| `CLOUDRU_EVOLUTION_TTL` | `120` | TXT record TTL (seconds) |
| `CLOUDRU_EVOLUTION_PROPAGATION_TIMEOUT` | `5m` | DNS propagation wait |
| `CLOUDRU_EVOLUTION_POLLING_INTERVAL` | `5s` | DNS polling interval |
| `CLOUDRU_EVOLUTION_HTTP_TIMEOUT` | `30s` | HTTP request timeout |
| `CLOUDRU_EVOLUTION_OPERATION_TIMEOUT` | `5m` | Async-operation poll timeout |
| `CLOUDRU_EVOLUTION_SEQUENCE_INTERVAL` | `500ms` | Delay between sequential wildcard challenges |
| `CLOUDRU_EVOLUTION_API_ENDPOINT` | `https://dns.api.cloud.ru` | Override DNS API base URL |
| `CLOUDRU_EVOLUTION_AUTH_ENDPOINT` | `https://iam.api.cloud.ru/api/v1/auth/token` | Override IAM token URL |

## lego CLI

```sh
export CLOUDRU_EVOLUTION_KEY_ID=<key-id>
export CLOUDRU_EVOLUTION_SECRET=<secret>
export CLOUDRU_EVOLUTION_PROJECT_ID=<project-uuid>

# Single domain
lego --email you@example.com --dns cloudruevolution \
     -d example.com run

# Wildcard (apex + wildcard in one request — lego handles sequencing automatically)
lego --email you@example.com --dns cloudruevolution \
     -d '*.example.com' -d example.com run
```

## Docker (metrica-pro/lego image)

```sh
docker run --rm \
  -e CLOUDRU_EVOLUTION_KEY_ID=<key-id> \
  -e CLOUDRU_EVOLUTION_SECRET=<secret> \
  -e CLOUDRU_EVOLUTION_PROJECT_ID=<project-uuid> \
  -v /etc/lego:/etc/lego \
  ghcr.io/metrica-pro/lego:latest \
  --email you@example.com --dns cloudruevolution \
  --path /etc/lego \
  -d '*.example.com' -d example.com run
```

## cert-manager (Kubernetes)

See [`specs/001-cloudruevolution-provider/deployment.md`](../../../specs/001-cloudruevolution-provider/deployment.md)
for a full cert-manager + lego-webhook setup guide.

## Troubleshooting

**"could not find zone"** — check that the domain's NS records are already delegated to
Cloud.ru Evolution DNS and that `CLOUDRU_EVOLUTION_PROJECT_ID` matches the project that owns
the zone.

**401 errors** — the provider automatically refreshes the IAM token on 401 and retries once.
If errors persist, verify the key ID / secret and that the key is not expired.

**Timeout on operation poll** — Cloud.ru async operations normally complete in 10–60 s.
Increase `CLOUDRU_EVOLUTION_OPERATION_TIMEOUT` if needed. Default is 5 min.
